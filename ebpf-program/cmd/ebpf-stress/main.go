/*
Package main provides the entry point for the eBPF Stress Test Program.

This program attaches eBPF programs to network hooks (XDP, TC, Socket)
and performs N loop iterations per packet to simulate CPU overhead.

The primary use case is replicating the AF_PACKET socket performance
degradation issue where kernel CPUs spend excessive time in softirq
handling network packets.

Usage:

	ebpf-stress --hook xdp --interface eth0 --loops 10000

Hooks:
  - xdp:        eXpress Data Path (earliest, fastest)
  - tc-ingress: Traffic Control ingress
  - tc-egress:  Traffic Control egress
  - socket:     Socket filter

Reference: https://github.com/midu16/NetPerfTest
*/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/telco-core/ebpf-stress/pkg/ebpf"
)

var (
	version   = "1.0.0"
	buildTime = "unknown"
)

// Config holds the application configuration
type Config struct {
	Hook           string
	Interface      string
	Loops          uint32
	Duration       time.Duration
	Verbose        bool
	MetricsPort    int
	StatsInterval  time.Duration
	EnableMetrics  bool
	PrometheusURL  string
}

// Statistics holds runtime statistics
type Statistics struct {
	mu              sync.RWMutex
	StartTime       time.Time
	PacketsTotal    uint64
	LoopsTotal      uint64
	BytesTotal      uint64
	TimeNsTotal     uint64
	AvgLoopsPerPkt  float64
	AvgTimePerPkt   float64
	PacketsPerSec   float64
	LoopsPerSec     float64
}

// Global variables
var (
	config     Config
	stats      Statistics
	loader     *ebpf.Loader
	rootCmd    *cobra.Command
)

func init() {
	rootCmd = &cobra.Command{
		Use:   "ebpf-stress",
		Short: "eBPF Stress Test Program",
		Long: `eBPF Stress Test Program - Configurable N loops per packet

This program attaches eBPF programs to network hooks and performs
N loop iterations per packet to simulate CPU overhead. Useful for
replicating AF_PACKET socket performance degradation issues.

Examples:
  # XDP hook with 10000 loops on eth0
  ebpf-stress --hook xdp --interface eth0 --loops 10000

  # TC ingress hook with metrics endpoint
  ebpf-stress --hook tc-ingress --interface eth0 --loops 5000 --metrics

  # Run for 5 minutes then exit
  ebpf-stress --hook xdp --interface eth0 --loops 10000 --duration 5m`,
		RunE: run,
	}

	// Flags
	rootCmd.Flags().StringVarP(&config.Hook, "hook", "H", "xdp",
		"eBPF hook type: xdp, tc-ingress, tc-egress, socket")
	rootCmd.Flags().StringVarP(&config.Interface, "interface", "i", "eth0",
		"Network interface to attach to")
	rootCmd.Flags().Uint32VarP(&config.Loops, "loops", "n", 1000,
		"Number of loop iterations per packet (N)")
	rootCmd.Flags().DurationVarP(&config.Duration, "duration", "d", 0,
		"Duration to run (0 = infinite)")
	rootCmd.Flags().BoolVarP(&config.Verbose, "verbose", "v", false,
		"Enable verbose output")
	rootCmd.Flags().IntVarP(&config.MetricsPort, "metrics-port", "p", 9091,
		"Port for Prometheus metrics endpoint")
	rootCmd.Flags().DurationVar(&config.StatsInterval, "stats-interval", 5*time.Second,
		"Interval for printing statistics")
	rootCmd.Flags().BoolVar(&config.EnableMetrics, "metrics", false,
		"Enable Prometheus metrics endpoint")
	rootCmd.Flags().StringVar(&config.PrometheusURL, "prometheus", "",
		"Prometheus URL for pushing metrics")

	// Version command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("ebpf-stress version %s (built %s)\n", version, buildTime)
		},
	})
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	// Print banner
	printBanner()

	// Validate configuration
	if err := validateConfig(); err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	// Print configuration
	printConfig()

	// Initialize statistics
	stats.StartTime = time.Now()

	// Create eBPF loader
	ebpfConfig := &ebpf.Config{
		Hook:      ebpf.HookType(config.Hook),
		Interface: config.Interface,
		Loops:     config.Loops,
	}
	loader = ebpf.NewLoader(ebpfConfig)

	// Load and attach eBPF program
	fmt.Println("\n📦 Loading eBPF program...")
	if err := loader.LoadAndAttach(); err != nil {
		return fmt.Errorf("failed to load eBPF program: %w", err)
	}
	fmt.Println("✓ eBPF program loaded and attached")

	// Start metrics server if enabled
	var metricsServer *http.Server
	if config.EnableMetrics {
		metricsServer = startMetricsServer()
		fmt.Printf("✓ Metrics server started on :%d\n", config.MetricsPort)
	}

	// Set up signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Set up duration timer if specified
	var durationChan <-chan time.Time
	if config.Duration > 0 {
		durationChan = time.After(config.Duration)
		fmt.Printf("⏱  Will run for %s\n", config.Duration)
	}

	// Print startup summary
	fmt.Println("\n" + "═══════════════════════════════════════════════════════════")
	fmt.Println("🚀 eBPF Stress Test Running")
	fmt.Println("   Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════")

	// Statistics ticker
	statsTicker := time.NewTicker(config.StatsInterval)
	defer statsTicker.Stop()

	// Main loop
	for {
		select {
		case <-sigChan:
			fmt.Println("\n\n⚠  Interrupt received, shutting down...")
			cancel()
			cleanup(metricsServer)
			return nil

		case <-durationChan:
			fmt.Printf("\n\n✓ Duration %s completed\n", config.Duration)
			cancel()
			cleanup(metricsServer)
			return nil

		case <-statsTicker.C:
			if err := collectAndPrintStats(); err != nil && config.Verbose {
				fmt.Printf("Warning: Failed to collect stats: %v\n", err)
			}

		case <-ctx.Done():
			cleanup(metricsServer)
			return nil
		}
	}
}

func validateConfig() error {
	// Validate hook type
	validHooks := map[string]bool{
		"xdp":        true,
		"tc-ingress": true,
		"tc-egress":  true,
		"socket":     true,
	}
	if !validHooks[config.Hook] {
		return fmt.Errorf("invalid hook type: %s (valid: xdp, tc-ingress, tc-egress, socket)", config.Hook)
	}

	// Validate interface
	if config.Interface == "" {
		return fmt.Errorf("interface name is required")
	}

	// Validate loops
	if config.Loops == 0 {
		return fmt.Errorf("loops must be greater than 0")
	}
	if config.Loops > 2500 {
		fmt.Printf("Warning: loops=%d exceeds eBPF maximum (2,500), will be capped\n", config.Loops)
		fmt.Printf("         For higher values, use the kernel module in kernel-module/\n")
		config.Loops = 2500
	}

	return nil
}

func printBanner() {
	fmt.Println(`
╔══════════════════════════════════════════════════════════════════╗
║     eBPF Stress Test Program                                    ║
║     Configurable N Loops Per Packet                             ║
║                                                                  ║
║     Based on AF_PACKET socket issue analysis                    ║
║     Reference: https://github.com/midu16/NetPerfTest            ║
╚══════════════════════════════════════════════════════════════════╝`)
}

func printConfig() {
	fmt.Println("\n📋 Configuration:")
	fmt.Printf("   Hook:        %s\n", config.Hook)
	fmt.Printf("   Interface:   %s\n", config.Interface)
	fmt.Printf("   Loops:       %d (per packet)\n", config.Loops)
	if config.Duration > 0 {
		fmt.Printf("   Duration:    %s\n", config.Duration)
	} else {
		fmt.Printf("   Duration:    infinite (until Ctrl+C)\n")
	}
	fmt.Printf("   Verbose:     %v\n", config.Verbose)
	if config.EnableMetrics {
		fmt.Printf("   Metrics:     http://localhost:%d/metrics\n", config.MetricsPort)
	}
}

func collectAndPrintStats() error {
	ebpfStats, err := loader.GetStats()
	if err != nil {
		return err
	}

	stats.mu.Lock()
	elapsed := time.Since(stats.StartTime).Seconds()
	
	// Calculate rates
	pps := float64(ebpfStats.PacketsProcessed) / elapsed
	lps := float64(ebpfStats.TotalLoops) / elapsed

	stats.PacketsTotal = ebpfStats.PacketsProcessed
	stats.LoopsTotal = ebpfStats.TotalLoops
	stats.AvgLoopsPerPkt = ebpfStats.LoopsPerPacket
	stats.PacketsPerSec = pps
	stats.LoopsPerSec = lps
	stats.mu.Unlock()

	// Print statistics
	fmt.Printf("\n[%s] Statistics:\n", time.Now().Format("15:04:05"))
	fmt.Printf("   Packets:     %d (%.2f pps)\n", ebpfStats.PacketsProcessed, pps)
	fmt.Printf("   Total Loops: %d (%.2f loops/s)\n", ebpfStats.TotalLoops, lps)
	fmt.Printf("   Avg Loops:   %.2f per packet\n", ebpfStats.LoopsPerPacket)
	fmt.Printf("   Elapsed:     %.1fs\n", elapsed)

	return nil
}

func cleanup(metricsServer *http.Server) {
	fmt.Println("\n🧹 Cleaning up...")

	// Print final statistics
	if ebpfStats, err := loader.GetStats(); err == nil {
		elapsed := time.Since(stats.StartTime).Seconds()
		fmt.Println("\n📊 Final Statistics:")
		fmt.Printf("   Duration:       %.1fs\n", elapsed)
		fmt.Printf("   Total Packets:  %d\n", ebpfStats.PacketsProcessed)
		fmt.Printf("   Total Loops:    %d\n", ebpfStats.TotalLoops)
		fmt.Printf("   Avg Loops/Pkt:  %.2f\n", ebpfStats.LoopsPerPacket)
		fmt.Printf("   Avg Packets/s:  %.2f\n", float64(ebpfStats.PacketsProcessed)/elapsed)
	}

	// Detach eBPF program
	if err := loader.Detach(); err != nil {
		fmt.Printf("Warning: Failed to detach eBPF program: %v\n", err)
	} else {
		fmt.Println("✓ eBPF program detached")
	}

	// Stop metrics server
	if metricsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metricsServer.Shutdown(ctx); err != nil {
			fmt.Printf("Warning: Metrics server shutdown error: %v\n", err)
		}
	}

	fmt.Println("✓ Cleanup complete")
}

func startMetricsServer() *http.Server {
	mux := http.NewServeMux()

	// Prometheus metrics endpoint
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		stats.mu.RLock()
		defer stats.mu.RUnlock()

		elapsed := time.Since(stats.StartTime).Seconds()

		fmt.Fprintf(w, "# HELP ebpf_stress_packets_total Total packets processed\n")
		fmt.Fprintf(w, "# TYPE ebpf_stress_packets_total counter\n")
		fmt.Fprintf(w, "ebpf_stress_packets_total{hook=\"%s\",interface=\"%s\"} %d\n",
			config.Hook, config.Interface, stats.PacketsTotal)

		fmt.Fprintf(w, "# HELP ebpf_stress_loops_total Total loop iterations performed\n")
		fmt.Fprintf(w, "# TYPE ebpf_stress_loops_total counter\n")
		fmt.Fprintf(w, "ebpf_stress_loops_total{hook=\"%s\",interface=\"%s\"} %d\n",
			config.Hook, config.Interface, stats.LoopsTotal)

		fmt.Fprintf(w, "# HELP ebpf_stress_loops_per_packet Average loops per packet\n")
		fmt.Fprintf(w, "# TYPE ebpf_stress_loops_per_packet gauge\n")
		fmt.Fprintf(w, "ebpf_stress_loops_per_packet{hook=\"%s\",interface=\"%s\"} %.2f\n",
			config.Hook, config.Interface, stats.AvgLoopsPerPkt)

		fmt.Fprintf(w, "# HELP ebpf_stress_packets_per_second Packets processed per second\n")
		fmt.Fprintf(w, "# TYPE ebpf_stress_packets_per_second gauge\n")
		fmt.Fprintf(w, "ebpf_stress_packets_per_second{hook=\"%s\",interface=\"%s\"} %.2f\n",
			config.Hook, config.Interface, stats.PacketsPerSec)

		fmt.Fprintf(w, "# HELP ebpf_stress_loops_per_second Loop iterations per second\n")
		fmt.Fprintf(w, "# TYPE ebpf_stress_loops_per_second gauge\n")
		fmt.Fprintf(w, "ebpf_stress_loops_per_second{hook=\"%s\",interface=\"%s\"} %.2f\n",
			config.Hook, config.Interface, stats.LoopsPerSec)

		fmt.Fprintf(w, "# HELP ebpf_stress_uptime_seconds Time since program started\n")
		fmt.Fprintf(w, "# TYPE ebpf_stress_uptime_seconds gauge\n")
		fmt.Fprintf(w, "ebpf_stress_uptime_seconds %.2f\n", elapsed)

		fmt.Fprintf(w, "# HELP ebpf_stress_configured_loops Configured loop count per packet\n")
		fmt.Fprintf(w, "# TYPE ebpf_stress_configured_loops gauge\n")
		fmt.Fprintf(w, "ebpf_stress_configured_loops %d\n", config.Loops)
	})

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	// Stats JSON endpoint
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats.mu.RLock()
		defer stats.mu.RUnlock()

		data := map[string]interface{}{
			"hook":            config.Hook,
			"interface":       config.Interface,
			"configured_loops": config.Loops,
			"packets_total":   stats.PacketsTotal,
			"loops_total":     stats.LoopsTotal,
			"avg_loops_per_pkt": stats.AvgLoopsPerPkt,
			"packets_per_sec": stats.PacketsPerSec,
			"loops_per_sec":   stats.LoopsPerSec,
			"uptime_seconds":  time.Since(stats.StartTime).Seconds(),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.MetricsPort),
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			fmt.Printf("Metrics server error: %v\n", err)
		}
	}()

	return server
}
