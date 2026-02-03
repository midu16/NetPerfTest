/*
Package main provides the entry point for the eBPF Metrics Exporter.

This program reads eBPF maps pinned by ebpf-stress and exports
Prometheus metrics for monitoring.

Usage:

	ebpf-exporter --config ebpf-exporter.yaml

Reference: https://github.com/midu16/NetPerfTest
*/
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	version   = "2.1.0"
	buildTime = "unknown"
)

// Default pin path for eBPF maps
const DefaultPinPath = "/sys/fs/bpf/ebpf-stress"

// YAMLConfig represents the YAML configuration file structure
type YAMLConfig struct {
	Server struct {
		Port         int    `yaml:"port"`
		PollInterval string `yaml:"poll_interval"`
	} `yaml:"server"`
	EBPFStress struct {
		Enabled bool   `yaml:"enabled"`
		PinPath string `yaml:"pin_path"`
	} `yaml:"ebpf_stress"`
	EBPF struct {
		PinPath string `yaml:"pin_path"`
	} `yaml:"ebpf"`
	LatencyMeasurement struct {
		Enabled    bool     `yaml:"enabled"`
		PinPath    string   `yaml:"pin_path"`
		Interfaces []string `yaml:"interfaces"`
	} `yaml:"latency_measurement"`
	Interfaces []struct {
		Name    string `yaml:"name"`
		Enabled bool   `yaml:"enabled"`
	} `yaml:"interfaces"`
	Metrics struct {
		PacketsTotal          bool `yaml:"packets_total"`
		LoopsTotal            bool `yaml:"loops_total"`
		BytesTotal            bool `yaml:"bytes_total"`
		ProcessingTimeNsTotal bool `yaml:"processing_time_ns_total"`
		PacketsPerSecond      bool `yaml:"packets_per_second"`
		LoopsPerSecond        bool `yaml:"loops_per_second"`
		LoopsPerPacket        bool `yaml:"loops_per_packet"`
		AvgTimePerPacketNs    bool `yaml:"avg_time_per_packet_ns"`
		UptimeSeconds         bool `yaml:"uptime_seconds"`
		ConfiguredLoops       bool `yaml:"configured_loops"`
		KernelStackLatency    bool `yaml:"kernel_stack_latency"`
		LatencyHistogram      bool `yaml:"latency_histogram"`
	} `yaml:"metrics"`
	Logging struct {
		Verbose bool   `yaml:"verbose"`
		Format  string `yaml:"format"`
	} `yaml:"logging"`
}

// Config holds the application configuration
type Config struct {
	Port              int
	PinPath           string
	PollInterval      time.Duration
	Verbose           bool
	ConfigFile        string
	Interfaces        []string
	YAMLConfig        *YAMLConfig
	LatencyEnabled    bool
	LatencyPinPath    string
	LatencyInterfaces []string
}

// InterfaceStats holds per-interface statistics
type InterfaceStats struct {
	Packets     uint64
	Loops       uint64
	Bytes       uint64
	TimeNs      uint64
	LastPackets uint64
	LastLoops   uint64
	LastBytes   uint64
	LastTimeNs  uint64
	LastCheck   time.Time
}

// Metrics holds Prometheus metrics
type Metrics struct {
	// ebpf-stress metrics
	packetsTotal    *prometheus.GaugeVec
	loopsTotal      *prometheus.GaugeVec
	bytesTotal      *prometheus.GaugeVec
	timeNsTotal     *prometheus.GaugeVec
	loopsPerPacket  *prometheus.GaugeVec
	avgTimePerPkt   *prometheus.GaugeVec
	packetsPerSec   *prometheus.GaugeVec
	loopsPerSec     *prometheus.GaugeVec
	bytesPerSec     *prometheus.GaugeVec
	uptimeSeconds   prometheus.Gauge
	configuredLoops prometheus.Gauge
	exporterUp      prometheus.Gauge
	interfacesUp    *prometheus.GaugeVec

	// Kernel stack latency metrics (independent measurement)
	kernelStackPacketsTotal   *prometheus.GaugeVec
	kernelStackBytesTotal     *prometheus.GaugeVec
	kernelStackLatencyNsTotal *prometheus.GaugeVec
	kernelStackLatencyMinNs   *prometheus.GaugeVec
	kernelStackLatencyMaxNs   *prometheus.GaugeVec
	kernelStackLatencyAvgNs   *prometheus.GaugeVec
	kernelStackHistogram      *prometheus.GaugeVec
	kernelStackCollectorUp    prometheus.Gauge
}

// Collector reads from eBPF maps
type Collector struct {
	config  *Config
	metrics *Metrics
	mu      sync.RWMutex

	// eBPF maps from ebpf-stress
	packetsMap   *ebpf.Map
	loopsMap     *ebpf.Map
	bytesMap     *ebpf.Map
	timeMap      *ebpf.Map
	loopCountMap *ebpf.Map

	// eBPF maps for kernel stack latency measurement
	latencyPacketsMap   *ebpf.Map
	latencyNsMap        *ebpf.Map
	latencyHistogramMap *ebpf.Map
	latencyInterfaceMap *ebpf.Map

	// Per-interface stats tracking
	interfaceStats map[string]*InterfaceStats

	// Tracking
	startTime            time.Time
	latencyMapsAvailable bool
}

var (
	config    Config
	collector *Collector
	rootCmd   *cobra.Command
)

func init() {
	rootCmd = &cobra.Command{
		Use:   "ebpf-exporter",
		Short: "eBPF Metrics Exporter",
		Long: `eBPF Metrics Exporter - Prometheus Exporter for eBPF Stress Test

This program reads eBPF maps pinned by ebpf-stress and exports
Prometheus metrics for monitoring and alerting.

Examples:
  # Start with config file
  ebpf-exporter --config ebpf-exporter.yaml

  # Start exporter on default port 9091
  ebpf-exporter

  # Use custom port and pin path
  ebpf-exporter --port 9092 --pin-path /sys/fs/bpf/custom

  # Monitor specific interfaces
  ebpf-exporter --interfaces lo,eth0`,
		RunE: run,
	}

	// Flags
	rootCmd.Flags().StringVarP(&config.ConfigFile, "config", "c", "",
		"Path to YAML configuration file")
	rootCmd.Flags().IntVarP(&config.Port, "port", "p", 9091,
		"Port for Prometheus metrics endpoint")
	rootCmd.Flags().StringVar(&config.PinPath, "pin-path", DefaultPinPath,
		"Path to pinned eBPF maps")
	rootCmd.Flags().DurationVar(&config.PollInterval, "poll-interval", 5*time.Second,
		"Interval for reading eBPF maps")
	rootCmd.Flags().BoolVarP(&config.Verbose, "verbose", "v", false,
		"Enable verbose output")
	rootCmd.Flags().StringSliceVar(&config.Interfaces, "interfaces", nil,
		"Comma-separated list of interfaces to monitor (empty = all)")

	// Version command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("ebpf-exporter version %s (built %s)\n", version, buildTime)
		},
	})
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func loadYAMLConfig(path string) (*YAMLConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var yamlConfig YAMLConfig
	if err := yaml.Unmarshal(data, &yamlConfig); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &yamlConfig, nil
}

func applyYAMLConfig(yamlConfig *YAMLConfig) {
	if yamlConfig.Server.Port > 0 {
		config.Port = yamlConfig.Server.Port
	}
	if yamlConfig.Server.PollInterval != "" {
		if d, err := time.ParseDuration(yamlConfig.Server.PollInterval); err == nil {
			config.PollInterval = d
		}
	}
	// Handle ebpf_stress config (new format)
	if yamlConfig.EBPFStress.PinPath != "" {
		config.PinPath = yamlConfig.EBPFStress.PinPath
	}
	// Fall back to ebpf config (old format)
	if yamlConfig.EBPF.PinPath != "" && config.PinPath == "" {
		config.PinPath = yamlConfig.EBPF.PinPath
	}
	if yamlConfig.Logging.Verbose {
		config.Verbose = true
	}

	// Latency measurement settings
	config.LatencyEnabled = yamlConfig.LatencyMeasurement.Enabled
	if yamlConfig.LatencyMeasurement.PinPath != "" {
		config.LatencyPinPath = yamlConfig.LatencyMeasurement.PinPath
	} else {
		config.LatencyPinPath = "/sys/fs/bpf/ebpf-exporter"
	}
	config.LatencyInterfaces = yamlConfig.LatencyMeasurement.Interfaces

	// Extract interface names
	for _, iface := range yamlConfig.Interfaces {
		if iface.Enabled && iface.Name != "" {
			config.Interfaces = append(config.Interfaces, iface.Name)
		}
	}
}

func run(cmd *cobra.Command, args []string) error {
	// Load YAML config if specified
	if config.ConfigFile != "" {
		yamlConfig, err := loadYAMLConfig(config.ConfigFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		config.YAMLConfig = yamlConfig
		applyYAMLConfig(yamlConfig)
	} else {
		// Try to load default config file
		defaultConfigs := []string{
			"ebpf-exporter.yaml",
			"/etc/ebpf-exporter/ebpf-exporter.yaml",
		}
		for _, path := range defaultConfigs {
			if yamlConfig, err := loadYAMLConfig(path); err == nil {
				fmt.Printf("Loaded config from: %s\n", path)
				config.YAMLConfig = yamlConfig
				applyYAMLConfig(yamlConfig)
				break
			}
		}
	}

	printBanner()

	// Get list of interfaces to monitor
	interfaces := getInterfacesToMonitor()
	fmt.Printf("   Interfaces:    %v\n", interfaces)

	// Initialize metrics
	metrics := newMetrics()

	// Register ebpf-stress metrics
	prometheus.MustRegister(
		metrics.packetsTotal,
		metrics.loopsTotal,
		metrics.bytesTotal,
		metrics.timeNsTotal,
		metrics.loopsPerPacket,
		metrics.avgTimePerPkt,
		metrics.packetsPerSec,
		metrics.loopsPerSec,
		metrics.bytesPerSec,
		metrics.uptimeSeconds,
		metrics.configuredLoops,
		metrics.exporterUp,
		metrics.interfacesUp,
	)

	// Register kernel stack latency metrics
	prometheus.MustRegister(
		metrics.kernelStackPacketsTotal,
		metrics.kernelStackBytesTotal,
		metrics.kernelStackLatencyNsTotal,
		metrics.kernelStackLatencyMinNs,
		metrics.kernelStackLatencyMaxNs,
		metrics.kernelStackLatencyAvgNs,
		metrics.kernelStackHistogram,
		metrics.kernelStackCollectorUp,
	)

	// Create collector
	collector = &Collector{
		config:         &config,
		metrics:        metrics,
		startTime:      time.Now(),
		interfaceStats: make(map[string]*InterfaceStats),
	}

	// Initialize per-interface stats
	for _, iface := range interfaces {
		collector.interfaceStats[iface] = &InterfaceStats{
			LastCheck: time.Now(),
		}
	}

	// Try to open ebpf-stress pinned maps
	if err := collector.openMaps(); err != nil {
		fmt.Printf("Warning: Could not open ebpf-stress maps: %v\n", err)
		fmt.Println("  → Run ebpf-stress with --pin-maps to enable stress metrics")
		metrics.exporterUp.Set(0)
	} else {
		fmt.Println("✓ Connected to ebpf-stress maps")
		metrics.exporterUp.Set(1)
	}

	// Try to open kernel stack latency maps (if enabled)
	if config.LatencyEnabled {
		if err := collector.openLatencyMaps(); err != nil {
			fmt.Printf("Warning: Could not open latency maps: %v\n", err)
			fmt.Println("  → Run 'sudo make build-all' and restart with sudo to enable latency measurement")
			metrics.kernelStackCollectorUp.Set(0)
		} else {
			fmt.Println("✓ Connected to kernel stack latency maps")
			metrics.kernelStackCollectorUp.Set(1)
			collector.latencyMapsAvailable = true
		}
	} else {
		fmt.Println("  Kernel stack latency measurement: disabled (set latency_measurement.enabled: true in config)")
		metrics.kernelStackCollectorUp.Set(0)
	}

	// Set up signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start metrics collector goroutine
	go collector.run(ctx)

	// Start HTTP server
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ready", readyHandler)
	mux.HandleFunc("/config", configHandler)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.Port),
		Handler: mux,
	}

	fmt.Printf("\n✓ Prometheus metrics available at http://localhost:%d/metrics\n", config.Port)
	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println("  Press Ctrl+C to stop")
	fmt.Println("═══════════════════════════════════════════════════════════")

	// Start server in goroutine
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	// Wait for signal
	<-sigChan
	fmt.Println("\n\n⚠  Interrupt received, shutting down...")

	// Shutdown gracefully
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		fmt.Printf("HTTP server shutdown error: %v\n", err)
	}

	cancel()
	collector.close()

	fmt.Println("✓ Exporter stopped")
	return nil
}

func getInterfacesToMonitor() []string {
	// If specific interfaces configured, use those
	if len(config.Interfaces) > 0 {
		// Check for wildcard
		for _, iface := range config.Interfaces {
			if iface == "*" {
				return getAllNetworkInterfaces()
			}
		}
		return config.Interfaces
	}

	// Default: return common interfaces that might exist
	return []string{"lo", "eth0", "ens192", "enp0s3"}
}

func getAllNetworkInterfaces() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return []string{"lo"}
	}

	var names []string
	for _, iface := range interfaces {
		// Skip down interfaces
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		names = append(names, iface.Name)
	}
	return names
}

func newMetrics() *Metrics {
	return &Metrics{
		packetsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_packets_total",
				Help: "Total packets processed by eBPF program",
			},
			[]string{"interface", "hook"},
		),
		loopsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_loops_total",
				Help: "Total loop iterations performed",
			},
			[]string{"interface", "hook"},
		),
		bytesTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_bytes_total",
				Help: "Total bytes processed",
			},
			[]string{"interface", "hook"},
		),
		timeNsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_processing_time_ns_total",
				Help: "Total processing time in nanoseconds (time spent in softIRQ/interrupt context)",
			},
			[]string{"interface", "hook"},
		),
		loopsPerPacket: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_loops_per_packet",
				Help: "Average loops per packet",
			},
			[]string{"interface"},
		),
		avgTimePerPkt: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_avg_time_per_packet_ns",
				Help: "Average processing time per packet in nanoseconds",
			},
			[]string{"interface"},
		),
		packetsPerSec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_packets_per_second",
				Help: "Packets processed per second",
			},
			[]string{"interface"},
		),
		loopsPerSec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_loops_per_second",
				Help: "Loop iterations per second",
			},
			[]string{"interface"},
		),
		bytesPerSec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_bytes_per_second",
				Help: "Bytes processed per second",
			},
			[]string{"interface"},
		),
		uptimeSeconds: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_uptime_seconds",
				Help: "Exporter uptime in seconds",
			},
		),
		configuredLoops: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_configured_loops",
				Help: "Configured loop count per packet",
			},
		),
		exporterUp: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ebpf_exporter_up",
				Help: "Whether the exporter is connected to eBPF maps (1 = yes, 0 = no)",
			},
		),
		interfacesUp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ebpf_stress_interface_up",
				Help: "Whether eBPF program is attached to interface (1 = yes, 0 = no)",
			},
			[]string{"interface"},
		),

		// Kernel stack latency metrics (independent measurement)
		kernelStackPacketsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_packets_total",
				Help: "Total packets measured for kernel stack latency",
			},
			[]string{"interface"},
		),
		kernelStackBytesTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_bytes_total",
				Help: "Total bytes processed through kernel stack",
			},
			[]string{"interface"},
		),
		kernelStackLatencyNsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_ns_total",
				Help: "Total packet latency in nanoseconds (time in kernel stack)",
			},
			[]string{"interface"},
		),
		kernelStackLatencyMinNs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_min_ns",
				Help: "Minimum observed packet latency in nanoseconds",
			},
			[]string{"interface"},
		),
		kernelStackLatencyMaxNs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_max_ns",
				Help: "Maximum observed packet latency in nanoseconds",
			},
			[]string{"interface"},
		),
		kernelStackLatencyAvgNs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_avg_ns",
				Help: "Average packet latency in nanoseconds (cost per packet in kernel stack)",
			},
			[]string{"interface"},
		),
		kernelStackHistogram: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_histogram",
				Help: "Histogram of packet latencies through kernel stack",
			},
			[]string{"bucket"},
		),
		kernelStackCollectorUp: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "kernel_stack_collector_up",
				Help: "Whether the kernel stack latency collector is running (1 = yes, 0 = no)",
			},
		),
	}
}

func (c *Collector) openMaps() error {
	mapPaths := map[string]**ebpf.Map{
		"stats_packets":     &c.packetsMap,
		"stats_loops":       &c.loopsMap,
		"stats_bytes":       &c.bytesMap,
		"stats_time_ns":     &c.timeMap,
		"config_loop_count": &c.loopCountMap,
	}

	for name, mapPtr := range mapPaths {
		path := filepath.Join(c.config.PinPath, name)
		m, err := ebpf.LoadPinnedMap(path, nil)
		if err != nil {
			return fmt.Errorf("failed to load %s: %w", name, err)
		}
		*mapPtr = m
	}

	return nil
}

// openLatencyMaps opens the kernel stack latency measurement maps
func (c *Collector) openLatencyMaps() error {
	pinPath := c.config.LatencyPinPath
	if pinPath == "" {
		pinPath = "/sys/fs/bpf/ebpf-exporter"
	}

	mapPaths := map[string]**ebpf.Map{
		"global_packets":    &c.latencyPacketsMap,
		"global_latency_ns": &c.latencyNsMap,
		"latency_histogram": &c.latencyHistogramMap,
	}

	for name, mapPtr := range mapPaths {
		path := filepath.Join(pinPath, name)
		m, err := ebpf.LoadPinnedMap(path, nil)
		if err != nil {
			return fmt.Errorf("failed to load %s: %w", name, err)
		}
		*mapPtr = m
	}

	return nil
}

func (c *Collector) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Close ebpf-stress maps
	if c.packetsMap != nil {
		c.packetsMap.Close()
	}
	if c.loopsMap != nil {
		c.loopsMap.Close()
	}
	if c.bytesMap != nil {
		c.bytesMap.Close()
	}
	if c.timeMap != nil {
		c.timeMap.Close()
	}
	if c.loopCountMap != nil {
		c.loopCountMap.Close()
	}

	// Close latency maps
	if c.latencyPacketsMap != nil {
		c.latencyPacketsMap.Close()
	}
	if c.latencyNsMap != nil {
		c.latencyNsMap.Close()
	}
	if c.latencyHistogramMap != nil {
		c.latencyHistogramMap.Close()
	}
}

func (c *Collector) run(ctx context.Context) {
	ticker := time.NewTicker(c.config.PollInterval)
	defer ticker.Stop()

	// Initial collection
	c.collect()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

func (c *Collector) collect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Try to reconnect if maps are not open
	if c.packetsMap == nil {
		if err := c.openMaps(); err != nil {
			c.metrics.exporterUp.Set(0)
			return
		}
		c.metrics.exporterUp.Set(1)
		if config.Verbose {
			fmt.Println("Reconnected to eBPF maps")
		}
	}

	// Update uptime
	c.metrics.uptimeSeconds.Set(time.Since(c.startTime).Seconds())

	// Read global stats
	key := uint32(0)

	packets := c.sumPerCPUMap(c.packetsMap, key)
	loops := c.sumPerCPUMap(c.loopsMap, key)
	bytes := c.sumPerCPUMap(c.bytesMap, key)
	timeNs := c.sumPerCPUMap(c.timeMap, key)

	// Read config
	var configLoops uint32
	if c.loopCountMap != nil {
		_ = c.loopCountMap.Lookup(key, &configLoops)
	}
	c.metrics.configuredLoops.Set(float64(configLoops))

	// Detect which hook is in use (we'll default to "xdp" for now)
	// In a more advanced version, this could be read from a map
	hook := "xdp"

	// Update per-interface metrics
	now := time.Now()
	for ifaceName, stats := range c.interfaceStats {
		// For now, all stats are global (shared across interfaces when using single pin path)
		// In per-interface mode, each interface would have its own pin path

		// Update totals
		c.metrics.packetsTotal.WithLabelValues(ifaceName, hook).Set(float64(packets))
		c.metrics.loopsTotal.WithLabelValues(ifaceName, hook).Set(float64(loops))
		c.metrics.bytesTotal.WithLabelValues(ifaceName, hook).Set(float64(bytes))
		c.metrics.timeNsTotal.WithLabelValues(ifaceName, hook).Set(float64(timeNs))

		// Calculate rates
		elapsed := now.Sub(stats.LastCheck).Seconds()
		if elapsed > 0 && packets > 0 {
			// Mark interface as up if we have packets
			c.metrics.interfacesUp.WithLabelValues(ifaceName).Set(1)

			pps := float64(packets-stats.LastPackets) / elapsed
			lps := float64(loops-stats.LastLoops) / elapsed
			bps := float64(bytes-stats.LastBytes) / elapsed

			c.metrics.packetsPerSec.WithLabelValues(ifaceName).Set(pps)
			c.metrics.loopsPerSec.WithLabelValues(ifaceName).Set(lps)
			c.metrics.bytesPerSec.WithLabelValues(ifaceName).Set(bps)

			// Calculate averages
			if packets > 0 {
				c.metrics.loopsPerPacket.WithLabelValues(ifaceName).Set(float64(loops) / float64(packets))
				c.metrics.avgTimePerPkt.WithLabelValues(ifaceName).Set(float64(timeNs) / float64(packets))
			}
		} else {
			c.metrics.interfacesUp.WithLabelValues(ifaceName).Set(0)
		}

		// Update tracking for rate calculation
		stats.LastPackets = packets
		stats.LastLoops = loops
		stats.LastBytes = bytes
		stats.LastTimeNs = timeNs
		stats.LastCheck = now
	}

	if config.Verbose {
		fmt.Printf("[%s] packets=%d loops=%d bytes=%d time_ns=%d\n",
			time.Now().Format("15:04:05"), packets, loops, bytes, timeNs)
	}

	// Collect kernel stack latency metrics if available
	c.collectLatencyMetrics()
}

// collectLatencyMetrics reads the kernel stack latency maps and updates metrics
func (c *Collector) collectLatencyMetrics() {
	if !c.latencyMapsAvailable {
		return
	}

	key := uint32(0)

	// Read global latency stats
	latencyPackets := c.sumPerCPUMap(c.latencyPacketsMap, key)
	latencyNs := c.sumPerCPUMap(c.latencyNsMap, key)

	// Update global metrics for each configured interface
	for ifaceName := range c.interfaceStats {
		c.metrics.kernelStackPacketsTotal.WithLabelValues(ifaceName).Set(float64(latencyPackets))
		c.metrics.kernelStackLatencyNsTotal.WithLabelValues(ifaceName).Set(float64(latencyNs))

		// Calculate average latency (cost per packet)
		if latencyPackets > 0 {
			avgNs := float64(latencyNs) / float64(latencyPackets)
			c.metrics.kernelStackLatencyAvgNs.WithLabelValues(ifaceName).Set(avgNs)
		}
	}

	// Read histogram buckets
	histogramBuckets := []string{
		"0-1us", "1-2us", "2-4us", "4-8us", "8-16us", "16-32us", "32-64us",
		"64-128us", "128-256us", "256-512us", "512-1024us", "1-2ms",
		"2-4ms", "4-8ms", "8-16ms", "16ms+",
	}

	for i, bucket := range histogramBuckets {
		count := c.sumPerCPUMap(c.latencyHistogramMap, uint32(i))
		c.metrics.kernelStackHistogram.WithLabelValues(bucket).Set(float64(count))
	}

	if config.Verbose && latencyPackets > 0 {
		avgUs := float64(latencyNs) / float64(latencyPackets) / 1000.0
		fmt.Printf("[%s] kernel_stack: packets=%d avg_latency=%.2fus\n",
			time.Now().Format("15:04:05"), latencyPackets, avgUs)
	}
}

func (c *Collector) sumPerCPUMap(m *ebpf.Map, key uint32) uint64 {
	if m == nil {
		return 0
	}

	var values []uint64
	if err := m.Lookup(key, &values); err != nil {
		var singleValue uint64
		if err := m.Lookup(key, &singleValue); err != nil {
			return 0
		}
		return singleValue
	}

	var total uint64
	for _, v := range values {
		total += v
	}
	return total
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	collector.mu.RLock()
	isReady := collector.packetsMap != nil
	collector.mu.RUnlock()

	if isReady {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Ready")
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "Not Ready - eBPF maps not available")
	}
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, `{
  "port": %d,
  "pin_path": "%s",
  "poll_interval": "%s",
  "interfaces": %v,
  "verbose": %t
}`, config.Port, config.PinPath, config.PollInterval, config.Interfaces, config.Verbose)
}

func printBanner() {
	fmt.Println(`
╔══════════════════════════════════════════════════════════════════╗
║     eBPF Metrics Exporter v2.1                                   ║
║     Prometheus Exporter for eBPF Stress Test                     ║
║                                                                  ║
║     Reads pinned maps from ebpf-stress                           ║
║     Reference: https://github.com/midu16/NetPerfTest             ║
╚══════════════════════════════════════════════════════════════════╝`)

	fmt.Println("\n📋 Configuration:")
	fmt.Printf("   Port:          %d\n", config.Port)
	fmt.Printf("   Pin Path:      %s\n", config.PinPath)
	fmt.Printf("   Poll Interval: %s\n", config.PollInterval)
	if config.ConfigFile != "" {
		fmt.Printf("   Config File:   %s\n", config.ConfigFile)
	}
}
