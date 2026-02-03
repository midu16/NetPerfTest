/*
Package main provides the entry point for the eBPF Stress Test Program.

This program attaches eBPF programs to network hooks (XDP, TC, Socket)
and performs N loop iterations per packet to simulate CPU overhead.

Maps are pinned to /sys/fs/bpf/ebpf-stress/ for the ebpf-exporter
to read and export Prometheus metrics independently.

Usage:

	ebpf-stress --hook xdp --interface eth0 --loops 1000

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
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/midu16/NetPerfTest/ebpf-stress/pkg/ebpf"
	"github.com/spf13/cobra"
)

var (
	version   = "2.0.0"
	buildTime = "unknown"
)

// Config holds the application configuration
type Config struct {
	Hook          string
	Interface     string
	Loops         uint32
	Duration      time.Duration
	Verbose       bool
	PinMaps       bool
	StatsInterval time.Duration
}

// Global variables
var (
	config  Config
	loader  *ebpf.Loader
	rootCmd *cobra.Command
)

func init() {
	rootCmd = &cobra.Command{
		Use:   "ebpf-stress",
		Short: "eBPF Stress Test Program",
		Long: `eBPF Stress Test Program - Configurable N Loops Per Packet

This program attaches eBPF programs to network hooks and performs
N loop iterations per packet to simulate CPU overhead. 

Maps are pinned to /sys/fs/bpf/ebpf-stress/ for the ebpf-exporter
to read statistics and expose Prometheus metrics.

Examples:
  # XDP hook with 1000 loops on eth0
  ebpf-stress --hook xdp --interface eth0 --loops 1000

  # TC ingress hook with pinned maps for exporter
  ebpf-stress --hook tc-ingress --interface eth0 --loops 500 --pin-maps

  # Run for 5 minutes then exit
  ebpf-stress --hook xdp --interface lo --loops 1000 --duration 5m`,
		RunE: run,
	}

	// Flags
	rootCmd.Flags().StringVarP(&config.Hook, "hook", "H", "xdp",
		"eBPF hook type: xdp, tc-ingress, tc-egress, socket")
	rootCmd.Flags().StringVarP(&config.Interface, "interface", "i", "lo",
		"Network interface to attach to")
	rootCmd.Flags().Uint32VarP(&config.Loops, "loops", "n", 1000,
		"Number of loop iterations per packet (max 2500)")
	rootCmd.Flags().DurationVarP(&config.Duration, "duration", "d", 0,
		"Duration to run (0 = infinite)")
	rootCmd.Flags().BoolVarP(&config.Verbose, "verbose", "v", false,
		"Enable verbose output")
	rootCmd.Flags().BoolVar(&config.PinMaps, "pin-maps", true,
		"Pin maps to /sys/fs/bpf/ebpf-stress/ for ebpf-exporter")
	rootCmd.Flags().DurationVar(&config.StatsInterval, "stats-interval", 5*time.Second,
		"Interval for printing statistics")

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
	printBanner()

	if err := validateConfig(); err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	printConfig()

	// Create eBPF loader
	ebpfConfig := &ebpf.Config{
		Hook:      ebpf.HookType(config.Hook),
		Interface: config.Interface,
		Loops:     config.Loops,
		PinMaps:   config.PinMaps,
	}
	loader = ebpf.NewLoader(ebpfConfig)

	// Load and attach eBPF program
	fmt.Println("\n📦 Loading eBPF program...")
	if err := loader.LoadAndAttach(); err != nil {
		return fmt.Errorf("failed to load eBPF program: %w", err)
	}
	fmt.Println("✓ eBPF program loaded and attached")

	if config.PinMaps {
		fmt.Printf("✓ Maps pinned to %s\n", ebpf.GetPinPath())
		fmt.Println("  Run ebpf-exporter to expose Prometheus metrics")
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

	fmt.Println("\n" + "═══════════════════════════════════════════════════════════")
	fmt.Println("🚀 eBPF Stress Test Running")
	fmt.Println("   Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════")

	// Statistics ticker
	statsTicker := time.NewTicker(config.StatsInterval)
	defer statsTicker.Stop()

	startTime := time.Now()

	// Main loop
	for {
		select {
		case <-sigChan:
			fmt.Println("\n\n⚠  Interrupt received, shutting down...")
			cancel()
			cleanup(startTime)
			return nil

		case <-durationChan:
			fmt.Printf("\n\n✓ Duration %s completed\n", config.Duration)
			cancel()
			cleanup(startTime)
			return nil

		case <-statsTicker.C:
			if err := printStats(startTime); err != nil && config.Verbose {
				fmt.Printf("Warning: Failed to collect stats: %v\n", err)
			}

		case <-ctx.Done():
			cleanup(startTime)
			return nil
		}
	}
}

func validateConfig() error {
	validHooks := map[string]bool{
		"xdp":        true,
		"tc-ingress": true,
		"tc-egress":  true,
		"socket":     true,
	}
	if !validHooks[config.Hook] {
		return fmt.Errorf("invalid hook type: %s", config.Hook)
	}

	if config.Interface == "" {
		return fmt.Errorf("interface name is required")
	}

	if config.Loops == 0 {
		return fmt.Errorf("loops must be greater than 0")
	}
	if config.Loops > 2500 {
		fmt.Printf("Warning: loops=%d exceeds maximum (2500), will be capped\n", config.Loops)
		config.Loops = 2500
	}

	return nil
}

func printBanner() {
	fmt.Println(`
╔══════════════════════════════════════════════════════════════════╗
║     eBPF Stress Test Program                                     ║
║     Configurable N Loops Per Packet                              ║
║                                                                  ║
║     Maps pinned for ebpf-exporter integration                    ║
║     Reference: https://github.com/midu16/NetPerfTest             ║
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
	fmt.Printf("   Pin Maps:    %v\n", config.PinMaps)
}

func printStats(startTime time.Time) error {
	stats, err := loader.GetStats()
	if err != nil {
		return err
	}

	elapsed := time.Since(startTime).Seconds()
	pps := float64(stats.PacketsProcessed) / elapsed

	fmt.Printf("\n[%s] Statistics:\n", time.Now().Format("15:04:05"))
	fmt.Printf("   Packets:     %d (%.2f pps)\n", stats.PacketsProcessed, pps)
	fmt.Printf("   Total Loops: %d\n", stats.TotalLoops)
	fmt.Printf("   Avg Loops:   %.2f per packet\n", stats.LoopsPerPacket)
	fmt.Printf("   Elapsed:     %.1fs\n", elapsed)

	return nil
}

func cleanup(startTime time.Time) {
	fmt.Println("\n🧹 Cleaning up...")

	// Print final statistics
	if stats, err := loader.GetStats(); err == nil {
		elapsed := time.Since(startTime).Seconds()
		fmt.Println("\n📊 Final Statistics:")
		fmt.Printf("   Duration:       %.1fs\n", elapsed)
		fmt.Printf("   Total Packets:  %d\n", stats.PacketsProcessed)
		fmt.Printf("   Total Loops:    %d\n", stats.TotalLoops)
		fmt.Printf("   Avg Loops/Pkt:  %.2f\n", stats.LoopsPerPacket)
		fmt.Printf("   Avg Packets/s:  %.2f\n", float64(stats.PacketsProcessed)/elapsed)
	}

	// Detach eBPF program
	if err := loader.Detach(); err != nil {
		fmt.Printf("Warning: Failed to detach eBPF program: %v\n", err)
	} else {
		fmt.Println("✓ eBPF program detached")
	}

	fmt.Println("✓ Cleanup complete")
}
