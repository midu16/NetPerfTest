/*
Package latency provides eBPF-based packet latency measurement.

This package loads its own eBPF programs to measure the time packets spend
traversing the kernel network stack, independent of any other eBPF programs.

It measures "cost per packet" by:
- Recording packet arrival time at XDP (earliest hook)
- Measuring latency at TC ingress and egress
- Building histograms of latency distribution
- Exporting per-interface statistics
*/
package latency

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/vishvananda/netlink"
)

const (
	// PinPath is where latency measurement maps are pinned
	PinPath = "/sys/fs/bpf/ebpf-exporter"

	// BPF object file path (relative to binary)
	BPFObjectPath = "bpf/obj/latency.o"

	// Histogram bucket labels (in microseconds)
	histogramBucketCount = 16
)

// Histogram bucket boundaries in microseconds
var histogramBuckets = []string{
	"0-1us", "1-2us", "2-4us", "4-8us", "8-16us", "16-32us", "32-64us",
	"64-128us", "128-256us", "256-512us", "512-1024us", "1-2ms",
	"2-4ms", "4-8ms", "8-16ms", "16ms+",
}

// InterfaceStats holds per-interface statistics from eBPF
type InterfaceStats struct {
	PacketsTotal     uint64
	BytesTotal       uint64
	LatencyNsTotal   uint64
	LatencyMinNs     uint64
	LatencyMaxNs     uint64
	XDPPackets       uint64
	TCIngressPackets uint64
	TCEgressPackets  uint64
	SoftirqTimeNs    uint64
}

// Config holds collector configuration
type Config struct {
	Interfaces   []string
	PinPath      string
	PollInterval time.Duration
	Verbose      bool
}

// Metrics holds Prometheus metrics for latency measurement
type Metrics struct {
	// Per-interface latency metrics
	packetsTotal     *prometheus.GaugeVec
	bytesTotal       *prometheus.GaugeVec
	latencyNsTotal   *prometheus.GaugeVec
	latencyMinNs     *prometheus.GaugeVec
	latencyMaxNs     *prometheus.GaugeVec
	latencyAvgNs     *prometheus.GaugeVec
	xdpPackets       *prometheus.GaugeVec
	tcIngressPackets *prometheus.GaugeVec
	tcEgressPackets  *prometheus.GaugeVec

	// Global metrics
	globalPackets   prometheus.Gauge
	globalLatencyNs prometheus.Gauge
	globalAvgNs     prometheus.Gauge

	// Histogram
	latencyHistogram *prometheus.GaugeVec

	// Status
	collectorUp        prometheus.Gauge
	attachedInterfaces *prometheus.GaugeVec
}

// Collector manages eBPF program loading and metrics collection
type Collector struct {
	config  *Config
	metrics *Metrics
	mu      sync.RWMutex

	// eBPF maps
	packetTimestamps *ebpf.Map
	interfaceStats   *ebpf.Map
	latencyHistogram *ebpf.Map
	globalPackets    *ebpf.Map
	globalLatencyNs  *ebpf.Map

	// Attached programs
	xdpLinks       map[string]link.Link
	tcIngressLinks map[string]netlink.Filter
	tcEgressLinks  map[string]netlink.Filter

	// Interface index mapping
	ifindexToName map[int]string

	// State
	running bool
}

// NewCollector creates a new latency collector
func NewCollector(config *Config) *Collector {
	if config.PinPath == "" {
		config.PinPath = PinPath
	}

	return &Collector{
		config:         config,
		metrics:        newMetrics(),
		xdpLinks:       make(map[string]link.Link),
		tcIngressLinks: make(map[string]netlink.Filter),
		tcEgressLinks:  make(map[string]netlink.Filter),
		ifindexToName:  make(map[int]string),
	}
}

// newMetrics creates Prometheus metrics
func newMetrics() *Metrics {
	return &Metrics{
		packetsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_packets_total",
				Help: "Total packets measured for latency",
			},
			[]string{"interface"},
		),
		bytesTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_bytes_total",
				Help: "Total bytes processed",
			},
			[]string{"interface"},
		),
		latencyNsTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_ns_total",
				Help: "Total packet latency in nanoseconds (time in kernel stack)",
			},
			[]string{"interface"},
		),
		latencyMinNs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_min_ns",
				Help: "Minimum observed packet latency in nanoseconds",
			},
			[]string{"interface"},
		),
		latencyMaxNs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_max_ns",
				Help: "Maximum observed packet latency in nanoseconds",
			},
			[]string{"interface"},
		),
		latencyAvgNs: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_avg_ns",
				Help: "Average packet latency in nanoseconds (cost per packet)",
			},
			[]string{"interface"},
		),
		xdpPackets: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_xdp_packets_total",
				Help: "Packets seen at XDP layer",
			},
			[]string{"interface"},
		),
		tcIngressPackets: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_tc_ingress_packets_total",
				Help: "Packets seen at TC ingress layer",
			},
			[]string{"interface"},
		),
		tcEgressPackets: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_tc_egress_packets_total",
				Help: "Packets seen at TC egress layer",
			},
			[]string{"interface"},
		),
		globalPackets: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "kernel_stack_global_packets_total",
				Help: "Total packets measured globally",
			},
		),
		globalLatencyNs: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "kernel_stack_global_latency_ns_total",
				Help: "Total latency globally in nanoseconds",
			},
		),
		globalAvgNs: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "kernel_stack_global_avg_latency_ns",
				Help: "Global average cost per packet in nanoseconds",
			},
		),
		latencyHistogram: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_latency_histogram",
				Help: "Histogram of packet latencies",
			},
			[]string{"bucket"},
		),
		collectorUp: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "kernel_stack_collector_up",
				Help: "Whether the latency collector is running (1 = yes)",
			},
		),
		attachedInterfaces: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kernel_stack_interface_attached",
				Help: "Whether eBPF is attached to interface (1 = yes)",
			},
			[]string{"interface"},
		),
	}
}

// RegisterMetrics registers all Prometheus metrics
func (c *Collector) RegisterMetrics(registry prometheus.Registerer) error {
	metrics := []prometheus.Collector{
		c.metrics.packetsTotal,
		c.metrics.bytesTotal,
		c.metrics.latencyNsTotal,
		c.metrics.latencyMinNs,
		c.metrics.latencyMaxNs,
		c.metrics.latencyAvgNs,
		c.metrics.xdpPackets,
		c.metrics.tcIngressPackets,
		c.metrics.tcEgressPackets,
		c.metrics.globalPackets,
		c.metrics.globalLatencyNs,
		c.metrics.globalAvgNs,
		c.metrics.latencyHistogram,
		c.metrics.collectorUp,
		c.metrics.attachedInterfaces,
	}

	for _, m := range metrics {
		if err := registry.Register(m); err != nil {
			return fmt.Errorf("failed to register metric: %w", err)
		}
	}

	return nil
}

// Start loads eBPF programs and attaches to interfaces
func (c *Collector) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Create pin directory
	if err := os.MkdirAll(c.config.PinPath, 0755); err != nil {
		return fmt.Errorf("failed to create pin path: %w", err)
	}

	// Try to load from pinned maps first (allows reading without attaching)
	if err := c.openPinnedMaps(); err == nil {
		c.running = true
		c.metrics.collectorUp.Set(1)
		return nil
	}

	// Load eBPF program
	spec, err := ebpf.LoadCollectionSpec(BPFObjectPath)
	if err != nil {
		// Try alternate paths
		altPaths := []string{
			"bpf/obj/latency.o",
			"../bpf/obj/latency.o",
			"/usr/share/ebpf-exporter/latency.o",
		}
		for _, path := range altPaths {
			spec, err = ebpf.LoadCollectionSpec(path)
			if err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("failed to load eBPF spec: %w", err)
		}
	}

	// Load collection with pinning
	opts := ebpf.CollectionOptions{
		Maps: ebpf.MapOptions{
			PinPath: c.config.PinPath,
		},
	}

	coll, err := ebpf.NewCollectionWithOptions(spec, opts)
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}

	// Get maps
	c.packetTimestamps = coll.Maps["packet_timestamps"]
	c.interfaceStats = coll.Maps["interface_latency_stats"]
	c.latencyHistogram = coll.Maps["latency_histogram"]
	c.globalPackets = coll.Maps["global_packets"]
	c.globalLatencyNs = coll.Maps["global_latency_ns"]

	// Make maps readable
	c.makeMapsReadable()

	// Attach to interfaces
	for _, ifname := range c.config.Interfaces {
		if err := c.attachToInterface(ifname, coll); err != nil {
			if c.config.Verbose {
				fmt.Printf("Warning: failed to attach to %s: %v\n", ifname, err)
			}
			c.metrics.attachedInterfaces.WithLabelValues(ifname).Set(0)
		} else {
			c.metrics.attachedInterfaces.WithLabelValues(ifname).Set(1)
			if c.config.Verbose {
				fmt.Printf("Attached to interface: %s\n", ifname)
			}
		}
	}

	c.running = true
	c.metrics.collectorUp.Set(1)

	return nil
}

// openPinnedMaps tries to open already-pinned maps (read-only mode)
func (c *Collector) openPinnedMaps() error {
	mapNames := []string{
		"packet_timestamps",
		"interface_latency_stats",
		"latency_histogram",
		"global_packets",
		"global_latency_ns",
	}

	maps := make(map[string]*ebpf.Map)
	for _, name := range mapNames {
		path := filepath.Join(c.config.PinPath, name)
		m, err := ebpf.LoadPinnedMap(path, nil)
		if err != nil {
			return fmt.Errorf("failed to load %s: %w", name, err)
		}
		maps[name] = m
	}

	c.packetTimestamps = maps["packet_timestamps"]
	c.interfaceStats = maps["interface_latency_stats"]
	c.latencyHistogram = maps["latency_histogram"]
	c.globalPackets = maps["global_packets"]
	c.globalLatencyNs = maps["global_latency_ns"]

	return nil
}

// attachToInterface attaches eBPF programs to an interface
func (c *Collector) attachToInterface(ifname string, coll *ebpf.Collection) error {
	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		return fmt.Errorf("interface not found: %w", err)
	}

	// Store mapping
	c.ifindexToName[iface.Index] = ifname

	// Attach XDP
	xdpProg := coll.Programs["xdp_latency_ingress"]
	if xdpProg != nil {
		xdpLink, err := link.AttachXDP(link.XDPOptions{
			Program:   xdpProg,
			Interface: iface.Index,
			Flags:     link.XDPGenericMode,
		})
		if err != nil {
			return fmt.Errorf("failed to attach XDP: %w", err)
		}
		c.xdpLinks[ifname] = xdpLink
	}

	// Attach TC programs would go here
	// (requires netlink setup which is more complex)

	return nil
}

// makeMapsReadable makes pinned maps world-readable
func (c *Collector) makeMapsReadable() {
	os.Chmod(c.config.PinPath, 0755)

	mapNames := []string{
		"packet_timestamps",
		"interface_latency_stats",
		"latency_histogram",
		"global_packets",
		"global_latency_ns",
	}

	for _, name := range mapNames {
		path := filepath.Join(c.config.PinPath, name)
		os.Chmod(path, 0644)
	}
}

// Collect reads eBPF maps and updates Prometheus metrics
func (c *Collector) Collect() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.running {
		c.metrics.collectorUp.Set(0)
		return
	}

	// Read global statistics
	globalPackets := c.sumPerCPUArray(c.globalPackets, 0)
	globalLatencyNs := c.sumPerCPUArray(c.globalLatencyNs, 0)

	c.metrics.globalPackets.Set(float64(globalPackets))
	c.metrics.globalLatencyNs.Set(float64(globalLatencyNs))

	if globalPackets > 0 {
		c.metrics.globalAvgNs.Set(float64(globalLatencyNs) / float64(globalPackets))
	}

	// Read histogram
	for i := 0; i < histogramBucketCount; i++ {
		count := c.sumPerCPUArray(c.latencyHistogram, uint32(i))
		c.metrics.latencyHistogram.WithLabelValues(histogramBuckets[i]).Set(float64(count))
	}

	// Read per-interface statistics
	if c.interfaceStats != nil {
		c.readInterfaceStats()
	}
}

// readInterfaceStats reads per-interface statistics from the hash map
func (c *Collector) readInterfaceStats() {
	// For known interfaces, try to read their stats
	for ifindex, ifname := range c.ifindexToName {
		key := uint32(ifindex)

		// Read per-CPU values and aggregate
		var values []InterfaceStats
		if err := c.interfaceStats.Lookup(key, &values); err != nil {
			continue
		}

		// Aggregate across CPUs
		var total InterfaceStats
		for _, v := range values {
			total.PacketsTotal += v.PacketsTotal
			total.BytesTotal += v.BytesTotal
			total.LatencyNsTotal += v.LatencyNsTotal
			total.XDPPackets += v.XDPPackets
			total.TCIngressPackets += v.TCIngressPackets
			total.TCEgressPackets += v.TCEgressPackets

			if v.LatencyMinNs > 0 && (total.LatencyMinNs == 0 || v.LatencyMinNs < total.LatencyMinNs) {
				total.LatencyMinNs = v.LatencyMinNs
			}
			if v.LatencyMaxNs > total.LatencyMaxNs {
				total.LatencyMaxNs = v.LatencyMaxNs
			}
		}

		// Update metrics
		c.metrics.packetsTotal.WithLabelValues(ifname).Set(float64(total.PacketsTotal))
		c.metrics.bytesTotal.WithLabelValues(ifname).Set(float64(total.BytesTotal))
		c.metrics.latencyNsTotal.WithLabelValues(ifname).Set(float64(total.LatencyNsTotal))
		c.metrics.latencyMinNs.WithLabelValues(ifname).Set(float64(total.LatencyMinNs))
		c.metrics.latencyMaxNs.WithLabelValues(ifname).Set(float64(total.LatencyMaxNs))
		c.metrics.xdpPackets.WithLabelValues(ifname).Set(float64(total.XDPPackets))
		c.metrics.tcIngressPackets.WithLabelValues(ifname).Set(float64(total.TCIngressPackets))
		c.metrics.tcEgressPackets.WithLabelValues(ifname).Set(float64(total.TCEgressPackets))

		if total.PacketsTotal > 0 {
			avgNs := float64(total.LatencyNsTotal) / float64(total.PacketsTotal)
			c.metrics.latencyAvgNs.WithLabelValues(ifname).Set(avgNs)
		}
	}
}

// sumPerCPUArray sums values from a per-CPU array map
func (c *Collector) sumPerCPUArray(m *ebpf.Map, key uint32) uint64 {
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

// Stop detaches programs and cleans up
func (c *Collector) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Detach XDP programs
	for ifname, lnk := range c.xdpLinks {
		if lnk != nil {
			lnk.Close()
		}
		c.metrics.attachedInterfaces.WithLabelValues(ifname).Set(0)
	}

	c.running = false
	c.metrics.collectorUp.Set(0)
}

// IsRunning returns whether the collector is active
func (c *Collector) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}
