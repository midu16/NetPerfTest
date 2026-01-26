/*
AF_PACKET Socket Stress Tool - Combined High PPS + Socket Multiplication

This program combines:
1. Native AF_PACKET socket creation (100+ sockets)
2. High-performance traffic generation (1M+ PPS)
3. Real-time CPU softirq monitoring

This accurately replicates the AF_PACKET performance issue where
packet delivery to multiple AF_PACKET sockets causes high softirq.

Usage:
  afpacket-stress --sockets 200 --pps 500000 --duration 120s

Based on: RHEL-83393 AF_PACKET socket performance degradation
*/
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Configuration flags
var (
	socketCount    = flag.Int("sockets", 200, "Number of AF_PACKET sockets to create")
	targetPPS      = flag.Int("pps", 500000, "Target packets per second")
	duration       = flag.Duration("duration", 120*time.Second, "Test duration")
	packetSize     = flag.Int("size", 64, "Packet size in bytes")
	workers        = flag.Int("workers", 0, "Number of traffic generator workers (0 = auto)")
	iface          = flag.String("interface", "lo", "Network interface for AF_PACKET sockets")
	maxRate        = flag.Bool("max", false, "Maximum rate mode (ignore PPS limit)")
	statsInterval  = flag.Duration("stats-interval", 2*time.Second, "Statistics reporting interval")
	progressiveAdd = flag.Bool("progressive", true, "Progressively add sockets during test")
	socketBatch    = flag.Int("socket-batch", 50, "Number of sockets to add per batch in progressive mode")
	batchDelay     = flag.Duration("batch-delay", 5*time.Second, "Delay between socket batches")
	verbose        = flag.Bool("verbose", false, "Verbose output")
)

// Statistics
var (
	packetsSent    uint64
	bytesSent      uint64
	packetsRecv    uint64
	bytesRecv      uint64
	activeSockets  int32
	peakSoftIRQ    float64
	avgSoftIRQ     float64
	softIRQSamples uint64
	startTime      time.Time
)

// AF_PACKET socket constants
const (
	ETH_P_ALL = 0x0003
)

// AFPacketSocket wraps a raw AF_PACKET socket
type AFPacketSocket struct {
	fd        int
	id        int
	ifaceIdx  int
	stopChan  chan struct{}
	bytesRecv uint64
	pktsRecv  uint64
}

func main() {
	flag.Parse()

	if *workers <= 0 {
		*workers = runtime.NumCPU()
	}

	printBanner()
	printConfig()

	// Increase file descriptor limit
	if err := increaseRlimit(); err != nil {
		fmt.Printf("Warning: Could not increase file descriptor limit: %v\n", err)
	}

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	stopChan := make(chan struct{})
	var wg sync.WaitGroup

	// Get interface index
	ifaceIdx, err := getInterfaceIndex(*iface)
	if err != nil {
		fmt.Printf("Error: Failed to get interface index for %s: %v\n", *iface, err)
		os.Exit(1)
	}
	fmt.Printf("Interface %s has index %d\n\n", *iface, ifaceIdx)

	// Create AF_PACKET sockets
	sockets := make([]*AFPacketSocket, 0, *socketCount)
	socketMu := sync.Mutex{}

	startTime = time.Now()

	// Start traffic generators first
	fmt.Printf("🚀 Starting %d traffic generator workers...\n", *workers)
	ppsPerWorker := *targetPPS / *workers

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

	// Create sockets (either all at once or progressively)
	if *progressiveAdd {
		// Progressive mode: add sockets in batches
		wg.Add(1)
		go func() {
			defer wg.Done()
			progressiveSocketCreation(ifaceIdx, &sockets, &socketMu, stopChan)
		}()
	} else {
		// Create all sockets at once
		fmt.Printf("📦 Creating %d AF_PACKET sockets...\n", *socketCount)
		for i := 0; i < *socketCount; i++ {
			sock, err := createAFPacketSocket(i+1, ifaceIdx)
			if err != nil {
				if *verbose {
					fmt.Printf("  Failed to create socket %d: %v\n", i+1, err)
				}
				continue
			}
			sockets = append(sockets, sock)
			atomic.AddInt32(&activeSockets, 1)

			// Start receiver goroutine
			wg.Add(1)
			go func(s *AFPacketSocket) {
				defer wg.Done()
				receivePackets(s, stopChan)
			}(sock)

			if (i+1)%50 == 0 {
				fmt.Printf("  Progress: %d/%d sockets created\n", i+1, *socketCount)
			}
		}
		fmt.Printf("✓ Created %d AF_PACKET sockets\n\n", len(sockets))
	}

	// Start statistics reporter
	wg.Add(1)
	go func() {
		defer wg.Done()
		reportStats(stopChan)
	}()

	// Start CPU softirq monitor
	wg.Add(1)
	go func() {
		defer wg.Done()
		monitorCPUSoftIRQ(stopChan)
	}()

	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Println("  Test running... Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Println()

	// Wait for completion
	select {
	case <-sigChan:
		fmt.Println("\n⚠️  Interrupt received, stopping...")
	case <-time.After(*duration):
		fmt.Println("\n✓ Duration complete, stopping...")
	}

	close(stopChan)

	// Give goroutines time to stop
	time.Sleep(500 * time.Millisecond)

	// Close all sockets
	fmt.Println("Closing AF_PACKET sockets...")
	socketMu.Lock()
	for _, sock := range sockets {
		if sock != nil {
			syscall.Close(sock.fd)
		}
	}
	socketMu.Unlock()

	// Wait for all goroutines
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		fmt.Println("Timeout waiting for goroutines to finish")
	}

	printFinalStats(len(sockets))
}

func printBanner() {
	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║     AF_PACKET Socket Stress Tool                                 ║")
	fmt.Println("║     High PPS + Socket Multiplication                             ║")
	fmt.Println("║     Replicates RHEL-83393 Performance Degradation                ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
	fmt.Println()
}

func printConfig() {
	fmt.Printf("Configuration:\n")
	fmt.Printf("  Interface:       %s\n", *iface)
	fmt.Printf("  AF_PACKET Sockets: %d\n", *socketCount)
	if *maxRate {
		fmt.Printf("  Traffic Mode:    MAXIMUM RATE\n")
	} else {
		fmt.Printf("  Target PPS:      %d packets/sec\n", *targetPPS)
	}
	fmt.Printf("  Packet Size:     %d bytes\n", *packetSize)
	fmt.Printf("  Workers:         %d\n", *workers)
	fmt.Printf("  Duration:        %s\n", *duration)
	fmt.Printf("  Progressive:     %v\n", *progressiveAdd)
	if *progressiveAdd {
		fmt.Printf("  Socket Batch:    %d sockets every %s\n", *socketBatch, *batchDelay)
	}
	fmt.Println()
}

func increaseRlimit() error {
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
		return err
	}

	// Try to set to maximum
	rlim.Cur = rlim.Max
	if rlim.Cur < 65536 {
		rlim.Cur = 65536
		rlim.Max = 65536
	}

	return syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rlim)
}

func getInterfaceIndex(name string) (int, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, err
	}
	return iface.Index, nil
}

// createAFPacketSocket creates a raw AF_PACKET socket bound to the interface
func createAFPacketSocket(id, ifaceIdx int) (*AFPacketSocket, error) {
	// Create AF_PACKET socket with SOCK_RAW
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(ETH_P_ALL)))
	if err != nil {
		return nil, fmt.Errorf("socket creation failed: %v", err)
	}

	// Set socket to non-blocking
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("set nonblock failed: %v", err)
	}

	// Bind to interface
	addr := syscall.SockaddrLinklayer{
		Protocol: htons(ETH_P_ALL),
		Ifindex:  ifaceIdx,
	}

	if err := syscall.Bind(fd, &addr); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind failed: %v", err)
	}

	// Set receive buffer size (smaller to reduce memory footprint)
	bufSize := 1024 * 64 // 64KB per socket
	syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, bufSize)

	return &AFPacketSocket{
		fd:       fd,
		id:       id,
		ifaceIdx: ifaceIdx,
		stopChan: make(chan struct{}),
	}, nil
}

// htons converts a 16-bit value from host to network byte order
func htons(i uint16) uint16 {
	return (i<<8)&0xff00 | i>>8
}

// receivePackets reads packets from an AF_PACKET socket
func receivePackets(sock *AFPacketSocket, stopChan <-chan struct{}) {
	buf := make([]byte, 65536)

	for {
		select {
		case <-stopChan:
			return
		default:
			// Use recvfrom with timeout behavior via poll
			n, _, err := syscall.Recvfrom(sock.fd, buf, syscall.MSG_DONTWAIT)
			if err != nil {
				if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
					// No data available, sleep briefly
					time.Sleep(100 * time.Microsecond)
					continue
				}
				// Other errors - socket may be closed
				return
			}

			if n > 0 {
				atomic.AddUint64(&sock.pktsRecv, 1)
				atomic.AddUint64(&sock.bytesRecv, uint64(n))
				atomic.AddUint64(&packetsRecv, 1)
				atomic.AddUint64(&bytesRecv, uint64(n))
			}
		}
	}
}

// progressiveSocketCreation adds sockets in batches during the test
func progressiveSocketCreation(ifaceIdx int, sockets *[]*AFPacketSocket, mu *sync.Mutex, stopChan <-chan struct{}) {
	currentCount := 0
	targetCount := *socketCount
	batchSize := *socketBatch

	fmt.Printf("📦 Progressive socket creation: %d sockets in batches of %d\n", targetCount, batchSize)

	var wg sync.WaitGroup

	for currentCount < targetCount {
		select {
		case <-stopChan:
			wg.Wait()
			return
		default:
		}

		// Calculate batch size
		remaining := targetCount - currentCount
		if remaining < batchSize {
			batchSize = remaining
		}

		fmt.Printf("  Creating batch: %d -> %d sockets\n", currentCount, currentCount+batchSize)

		// Create batch of sockets
		for i := 0; i < batchSize; i++ {
			sock, err := createAFPacketSocket(currentCount+i+1, ifaceIdx)
			if err != nil {
				if *verbose {
					fmt.Printf("    Failed to create socket %d: %v\n", currentCount+i+1, err)
				}
				continue
			}

			mu.Lock()
			*sockets = append(*sockets, sock)
			mu.Unlock()
			atomic.AddInt32(&activeSockets, 1)

			// Start receiver goroutine
			wg.Add(1)
			go func(s *AFPacketSocket) {
				defer wg.Done()
				receivePackets(s, stopChan)
			}(sock)
		}

		currentCount += batchSize
		fmt.Printf("  ✓ Active sockets: %d\n", atomic.LoadInt32(&activeSockets))

		// Wait before next batch
		select {
		case <-stopChan:
			wg.Wait()
			return
		case <-time.After(*batchDelay):
		}
	}

	fmt.Printf("✓ All %d sockets created\n\n", targetCount)
	wg.Wait()
}

// sendPacketsMaxRate sends packets as fast as possible
func sendPacketsMaxRate(workerID int, stopChan <-chan struct{}) {
	// Create UDP connection to localhost
	addr := fmt.Sprintf("127.0.0.1:%d", 9999+workerID)
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

	// Send as fast as possible in bursts
	for {
		select {
		case <-stopChan:
			return
		default:
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
	addr := fmt.Sprintf("127.0.0.1:%d", 9999+workerID)
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

	// Batched sending for high PPS
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

// reportStats periodically reports statistics
func reportStats(stopChan <-chan struct{}) {
	ticker := time.NewTicker(*statsInterval)
	defer ticker.Stop()

	var lastPacketsSent, lastPacketsRecv uint64
	lastTime := time.Now()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			currentPacketsSent := atomic.LoadUint64(&packetsSent)
			currentPacketsRecv := atomic.LoadUint64(&packetsRecv)
			currentTime := time.Now()
			currentSockets := atomic.LoadInt32(&activeSockets)

			elapsed := currentTime.Sub(lastTime).Seconds()
			sendPPS := float64(currentPacketsSent-lastPacketsSent) / elapsed
			recvPPS := float64(currentPacketsRecv-lastPacketsRecv) / elapsed

			// Get current softirq
			siPercent := getSoftIRQPercent()

			fmt.Printf("[%s] Sockets: %3d | TX: %8.0f pps | RX: %8.0f pps | SoftIRQ: %5.1f%% | Peak SI: %5.1f%%\n",
				time.Now().Format("15:04:05"),
				currentSockets,
				sendPPS,
				recvPPS,
				siPercent,
				peakSoftIRQ)

			lastPacketsSent = currentPacketsSent
			lastPacketsRecv = currentPacketsRecv
			lastTime = currentTime
		}
	}
}

// monitorCPUSoftIRQ monitors CPU softirq percentage
func monitorCPUSoftIRQ(stopChan <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			si := getSoftIRQPercent()
			if si > peakSoftIRQ {
				peakSoftIRQ = si
			}

			// Update running average
			samples := atomic.AddUint64(&softIRQSamples, 1)
			avgSoftIRQ = (avgSoftIRQ*float64(samples-1) + si) / float64(samples)
		}
	}
}

// getSoftIRQPercent returns current CPU softirq percentage using mpstat
func getSoftIRQPercent() float64 {
	// Use mpstat to get softirq percentage
	cmd := exec.Command("mpstat", "1", "1")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	// Parse mpstat output for %soft column
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "all") && !strings.Contains(line, "CPU") {
			fields := strings.Fields(line)
			// %soft is typically the 7th column (0-indexed: 6)
			for i, field := range fields {
				if i >= 6 && i <= 8 {
					if val, err := strconv.ParseFloat(field, 64); err == nil && val > 0 && val <= 100 {
						return val
					}
				}
			}
		}
	}
	return 0
}

// printFinalStats prints final statistics
func printFinalStats(socketCount int) {
	elapsed := time.Since(startTime).Seconds()
	totalSent := atomic.LoadUint64(&packetsSent)
	totalRecv := atomic.LoadUint64(&packetsRecv)
	totalBytesSent := atomic.LoadUint64(&bytesSent)
	totalBytesRecv := atomic.LoadUint64(&bytesRecv)

	avgSendPPS := float64(totalSent) / elapsed
	avgRecvPPS := float64(totalRecv) / elapsed

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Println("                         FINAL STATISTICS                           ")
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Printf("  Duration:           %.1f seconds\n", elapsed)
	fmt.Printf("  AF_PACKET Sockets:  %d\n", socketCount)
	fmt.Println()
	fmt.Println("  Traffic Generation:")
	fmt.Printf("    Packets Sent:     %d\n", totalSent)
	fmt.Printf("    Data Sent:        %.2f MB\n", float64(totalBytesSent)/1024/1024)
	fmt.Printf("    Average PPS:      %.0f packets/sec\n", avgSendPPS)
	fmt.Printf("    Throughput:       %.2f Mbps\n", float64(totalBytesSent)*8/elapsed/1000000)
	fmt.Println()
	fmt.Println("  AF_PACKET Reception:")
	fmt.Printf("    Packets Received: %d\n", totalRecv)
	fmt.Printf("    Data Received:    %.2f MB\n", float64(totalBytesRecv)/1024/1024)
	fmt.Printf("    Average PPS:      %.0f packets/sec\n", avgRecvPPS)
	fmt.Println()
	fmt.Println("  CPU SoftIRQ (Critical Metric):")
	fmt.Printf("    Peak SoftIRQ:     %.1f%%\n", peakSoftIRQ)
	fmt.Printf("    Average SoftIRQ:  %.1f%%\n", avgSoftIRQ)
	fmt.Println()

	// Verdict
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	if peakSoftIRQ >= 50 {
		fmt.Println("  ✅ AF_PACKET ISSUE REPLICATED!")
		fmt.Printf("     Peak softirq %.1f%% exceeded 50%% threshold\n", peakSoftIRQ)
	} else if avgSoftIRQ >= 30 {
		fmt.Println("  ⚠️  PARTIAL REPLICATION")
		fmt.Printf("     Average softirq %.1f%% is elevated but below 50%% threshold\n", avgSoftIRQ)
		fmt.Println("     Try: --sockets 500 --max")
	} else {
		fmt.Println("  ❌ ISSUE NOT REPLICATED")
		fmt.Printf("     SoftIRQ %.1f%% is below threshold\n", peakSoftIRQ)
		fmt.Println("     Try: --sockets 500 --pps 1000000 --max")
	}
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Println()
}

// Ensure unsafe import is used (for potential future memory-mapped ring buffer)
var _ = unsafe.Sizeof(0)
