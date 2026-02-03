/*
High-Performance Traffic Generator for eBPF Stress Testing

This program generates maximum-rate UDP traffic to stress test eBPF programs.
Optimized for high PPS (packets per second) on loopback interface.

Usage:

	traffic-gen --pps 500000 --workers 16 --duration 60s
*/
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	pps        = flag.Int("pps", 500000, "Target packets per second (500k default)")
	duration   = flag.Duration("duration", 120*time.Second, "Test duration")
	packetSize = flag.Int("size", 64, "Packet size in bytes")
	workers    = flag.Int("workers", 0, "Number of sender goroutines (0 = auto)")
	port       = flag.Int("port", 9999, "Base UDP port")
	targetIP   = flag.String("target", "127.0.0.1", "Target IP address")
	maxRate    = flag.Bool("max", false, "Maximum rate mode (ignore PPS limit)")
)

var (
	packetsSent uint64
	bytesSent   uint64
	startTime   time.Time
)

func main() {
	flag.Parse()

	// Auto-detect worker count
	if *workers <= 0 {
		*workers = runtime.NumCPU()
	}

	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║     High-Performance Traffic Generator                           ║")
	fmt.Println("║     For eBPF Stress Testing                                      ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	fmt.Printf("Configuration:\n")
	fmt.Printf("  Target:      %s:%d-%d\n", *targetIP, *port, *port+*workers-1)
	if *maxRate {
		fmt.Printf("  Mode:        MAXIMUM RATE (no limit)\n")
	} else {
		fmt.Printf("  PPS Target:  %d packets/sec\n", *pps)
	}
	fmt.Printf("  Packet Size: %d bytes\n", *packetSize)
	fmt.Printf("  Workers:     %d (one per CPU)\n", *workers)
	fmt.Printf("  Duration:    %s\n", *duration)
	fmt.Println()

	// Increase GOMAXPROCS to use all CPUs
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create stop channel
	stopChan := make(chan struct{})

	// Start workers
	var wg sync.WaitGroup
	ppsPerWorker := *pps / *workers

	fmt.Printf("🚀 Starting %d workers...\n", *workers)
	startTime = time.Now()

	for i := 0; i < *workers; i++ {
		wg.Add(1)
		if *maxRate {
			go func(workerID int) {
				defer wg.Done()
				sendPacketsMaxRate(workerID, stopChan)
			}(i)
		} else {
			go func(workerID int) {
				defer wg.Done()
				sendPacketsRateLimited(workerID, ppsPerWorker, stopChan)
			}(i)
		}
	}

	// Start stats reporter
	go reportStats(stopChan)

	fmt.Println("✓ Traffic generator running")
	fmt.Println()

	// Wait for duration or interrupt
	select {
	case <-sigChan:
		fmt.Println("\n⚠️  Interrupt received, stopping...")
	case <-time.After(*duration):
		fmt.Println("\n✓ Duration complete, stopping...")
	}

	close(stopChan)
	wg.Wait()

	// Final stats
	elapsed := time.Since(startTime).Seconds()
	totalPackets := atomic.LoadUint64(&packetsSent)
	totalBytes := atomic.LoadUint64(&bytesSent)
	actualPPS := float64(totalPackets) / elapsed

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Println("                         FINAL STATISTICS                          ")
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Printf("  Duration:      %.1f seconds\n", elapsed)
	fmt.Printf("  Total Packets: %d\n", totalPackets)
	fmt.Printf("  Total Data:    %.2f MB\n", float64(totalBytes)/1024/1024)
	fmt.Printf("  Actual PPS:    %.0f packets/sec\n", actualPPS)
	fmt.Printf("  Throughput:    %.2f Mbps\n", float64(totalBytes)*8/elapsed/1000000)
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Println()
}

// sendPacketsMaxRate sends packets as fast as possible (no rate limiting)
func sendPacketsMaxRate(workerID int, stopChan <-chan struct{}) {
	addr := fmt.Sprintf("%s:%d", *targetIP, *port+workerID)
	conn, err := net.Dial("udp", addr)
	if err != nil {
		fmt.Printf("Worker %d: Failed to create connection: %v\n", workerID, err)
		return
	}
	defer conn.Close()

	// Create packet data
	data := make([]byte, *packetSize)
	for i := range data {
		data[i] = byte((i + workerID) % 256)
	}

	// Send as fast as possible
	for {
		select {
		case <-stopChan:
			return
		default:
			// Burst send - 100 packets at a time
			for i := 0; i < 100; i++ {
				n, err := conn.Write(data)
				if err == nil {
					atomic.AddUint64(&packetsSent, 1)
					atomic.AddUint64(&bytesSent, uint64(n))
				}
			}
		}
	}
}

// sendPacketsRateLimited sends packets at a controlled rate
func sendPacketsRateLimited(workerID int, targetPPS int, stopChan <-chan struct{}) {
	addr := fmt.Sprintf("%s:%d", *targetIP, *port+workerID)
	conn, err := net.Dial("udp", addr)
	if err != nil {
		fmt.Printf("Worker %d: Failed to create connection: %v\n", workerID, err)
		return
	}
	defer conn.Close()

	data := make([]byte, *packetSize)
	for i := range data {
		data[i] = byte((i + workerID) % 256)
	}

	if targetPPS <= 0 {
		targetPPS = 10000
	}

	// For high PPS, use batching
	batchSize := 10
	batchInterval := time.Second * time.Duration(batchSize) / time.Duration(targetPPS)
	if batchInterval < time.Microsecond {
		batchInterval = time.Microsecond
		batchSize = targetPPS / 1000000
		if batchSize < 1 {
			batchSize = 1
		}
		if batchSize > 1000 {
			batchSize = 1000
		}
	}

	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			for i := 0; i < batchSize; i++ {
				n, err := conn.Write(data)
				if err == nil {
					atomic.AddUint64(&packetsSent, 1)
					atomic.AddUint64(&bytesSent, uint64(n))
				}
			}
		}
	}
}

func reportStats(stopChan <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastPackets uint64
	lastTime := time.Now()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			currentPackets := atomic.LoadUint64(&packetsSent)
			currentTime := time.Now()

			elapsed := currentTime.Sub(lastTime).Seconds()
			currentPPS := float64(currentPackets-lastPackets) / elapsed
			totalElapsed := currentTime.Sub(startTime).Seconds()
			avgPPS := float64(currentPackets) / totalElapsed

			fmt.Printf("  [%s] Packets: %10d | Current: %8.0f pps | Avg: %8.0f pps\n",
				time.Now().Format("15:04:05"),
				currentPackets,
				currentPPS,
				avgPPS)

			lastPackets = currentPackets
			lastTime = currentTime
		}
	}
}
