/*
Package ebpf provides the eBPF program loader and manager.

This package handles loading, attaching, and detaching eBPF programs
for the stress test application. It supports multiple hook types:
  - XDP (eXpress Data Path)
  - TC (Traffic Control) ingress/egress
  - Socket filters

Maps are pinned to /sys/fs/bpf/ebpf-stress/ to allow the ebpf-exporter
component to read statistics independently.
*/
package ebpf

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Pin path for eBPF maps - used by ebpf-exporter to read stats
const BPFPinPath = "/sys/fs/bpf/ebpf-stress"

// HookType represents the type of eBPF hook
type HookType string

const (
	HookXDP       HookType = "xdp"
	HookTCIngress HookType = "tc-ingress"
	HookTCEgress  HookType = "tc-egress"
	HookSocket    HookType = "socket"
)

// Config holds the configuration for the eBPF program loader
type Config struct {
	Hook      HookType
	Interface string
	Loops     uint32
	Program   string // Path to compiled eBPF object file
	PinMaps   bool   // Whether to pin maps for ebpf-exporter
}

// Stats represents statistics from the eBPF program
type Stats struct {
	PacketsProcessed uint64
	TotalLoops       uint64
	TotalBytes       uint64
	TotalTimeNs      uint64
	LoopsPerPacket   float64
	AvgTimePerPacket float64
}

// Loader manages the lifecycle of eBPF programs
type Loader struct {
	config     *Config
	collection *ebpf.Collection
	xdpLink    link.Link
	tcLink     interface{} // TC uses netlink, not link.Link
	socketLink link.Link

	// Maps for configuration
	loopCountMap *ebpf.Map
	enabledMap   *ebpf.Map

	// Maps for statistics
	packetsMap *ebpf.Map
	loopsMap   *ebpf.Map
	bytesMap   *ebpf.Map
	timeMap    *ebpf.Map

	// TC-specific state
	tcIface   *netlink.Link
	tcIngress bool
}

// NewLoader creates a new eBPF program loader
func NewLoader(config *Config) *Loader {
	if config.Program == "" {
		config.Program = "bpf/obj/stress.o"
	}
	if config.PinMaps {
		// Create pin directory if it doesn't exist
		os.MkdirAll(BPFPinPath, 0755)
	}
	return &Loader{
		config: config,
	}
}

// LoadAndAttach loads the eBPF program and attaches it to the specified hook
func (l *Loader) LoadAndAttach() error {
	// Remove memlock limit for eBPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("failed to remove memlock limit: %w", err)
	}

	// Load eBPF program
	spec, err := l.loadSpec()
	if err != nil {
		return fmt.Errorf("failed to load eBPF spec: %w", err)
	}

	// Set up collection options for map pinning
	opts := ebpf.CollectionOptions{}
	if l.config.PinMaps {
		opts.Maps.PinPath = BPFPinPath
	}

	// Load collection
	coll, err := ebpf.NewCollectionWithOptions(spec, opts)
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}
	l.collection = coll

	// Make pinned maps world-readable so ebpf-exporter can read them without root
	if l.config.PinMaps {
		makeMapsReadable()
	}

	// Get configuration maps
	l.loopCountMap = coll.Maps["config_loop_count"]
	l.enabledMap = coll.Maps["config_enabled"]

	// Get statistics maps
	l.packetsMap = coll.Maps["stats_packets"]
	l.loopsMap = coll.Maps["stats_loops"]
	l.bytesMap = coll.Maps["stats_bytes"]
	l.timeMap = coll.Maps["stats_time_ns"]

	if l.loopCountMap == nil {
		return fmt.Errorf("loop count map not found in eBPF program")
	}

	// Set loop count
	key := uint32(0)
	if err := l.loopCountMap.Put(key, l.config.Loops); err != nil {
		return fmt.Errorf("failed to set loop count: %w", err)
	}

	// Enable the program
	if l.enabledMap != nil {
		enabled := uint32(1)
		if err := l.enabledMap.Put(key, enabled); err != nil {
			return fmt.Errorf("failed to enable program: %w", err)
		}
	}

	// Attach program
	if err := l.attachProgram(); err != nil {
		l.Detach()
		return fmt.Errorf("failed to attach program: %w", err)
	}

	return nil
}

// loadSpec loads the eBPF collection spec from file
func (l *Loader) loadSpec() (*ebpf.CollectionSpec, error) {
	paths := []string{
		l.config.Program,
		"bpf/obj/stress.o",
		"./bpf/obj/stress.o",
		"../bpf/obj/stress.o",
		"/usr/share/ebpf-stress/stress.o",
	}

	var lastErr error
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		spec, err := ebpf.LoadCollectionSpec(path)
		if err != nil {
			lastErr = err
			continue
		}
		return spec, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed to load eBPF collection: %w", lastErr)
	}
	return nil, fmt.Errorf("eBPF object file not found")
}

// attachProgram attaches the eBPF program to the appropriate hook
func (l *Loader) attachProgram() error {
	switch l.config.Hook {
	case HookXDP:
		return l.attachXDP()
	case HookTCIngress:
		return l.attachTC(true)
	case HookTCEgress:
		return l.attachTC(false)
	case HookSocket:
		return l.attachSocket()
	default:
		return fmt.Errorf("unsupported hook type: %s", l.config.Hook)
	}
}

// attachXDP attaches the program to XDP hook
func (l *Loader) attachXDP() error {
	prog := l.collection.Programs["xdp_stress_prog"]
	if prog == nil {
		return fmt.Errorf("xdp_stress_prog not found in eBPF collection")
	}

	iface, err := netlink.LinkByName(l.config.Interface)
	if err != nil {
		return fmt.Errorf("failed to get interface %s: %w", l.config.Interface, err)
	}

	opts := link.XDPOptions{
		Program:   prog,
		Interface: iface.Attrs().Index,
		Flags:     link.XDPGenericMode,
	}

	xdpLink, err := link.AttachXDP(opts)
	if err != nil {
		return fmt.Errorf("failed to attach XDP program: %w", err)
	}

	l.xdpLink = xdpLink
	return nil
}

// attachTC attaches the program to TC hook
func (l *Loader) attachTC(ingress bool) error {
	progName := "tc_stress_prog"
	prog := l.collection.Programs[progName]
	if prog == nil {
		return fmt.Errorf("%s not found in eBPF collection", progName)
	}

	iface, err := netlink.LinkByName(l.config.Interface)
	if err != nil {
		return fmt.Errorf("failed to get interface %s: %w", l.config.Interface, err)
	}

	progFD := prog.FD()
	if progFD < 0 {
		return fmt.Errorf("invalid program file descriptor")
	}

	// TC requires clsact qdisc
	clsactQdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: iface.Attrs().Index,
			Handle:    netlink.MakeHandle(0xFFFF, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}

	_ = netlink.QdiscDel(clsactQdisc)

	if err := netlink.QdiscAdd(clsactQdisc); err != nil {
		return fmt.Errorf("failed to add clsact qdisc: %w", err)
	}

	var parent uint32
	if ingress {
		parent = netlink.HANDLE_MIN_INGRESS
	} else {
		parent = netlink.HANDLE_MIN_EGRESS
	}

	filter := &netlink.BpfFilter{
		FilterAttrs: netlink.FilterAttrs{
			LinkIndex: iface.Attrs().Index,
			Parent:    parent,
			Handle:    1,
			Protocol:  unix.ETH_P_ALL,
			Priority:  1,
		},
		Fd:           progFD,
		Name:         progName,
		DirectAction: true,
	}

	if err := netlink.FilterAdd(filter); err != nil {
		_ = netlink.QdiscDel(clsactQdisc)
		return fmt.Errorf("failed to add TC filter: %w", err)
	}

	l.tcLink = filter
	l.tcIface = &iface
	l.tcIngress = ingress

	return nil
}

// attachSocket attaches the program to socket filter
func (l *Loader) attachSocket() error {
	return fmt.Errorf("socket filter requires socket FD - use XDP or TC instead")
}

// SetLoopCount updates the loop count dynamically
func (l *Loader) SetLoopCount(loops uint32) error {
	if l.loopCountMap == nil {
		return fmt.Errorf("loop count map not initialized")
	}
	key := uint32(0)
	return l.loopCountMap.Put(key, loops)
}

// SetEnabled enables or disables the stress program
func (l *Loader) SetEnabled(enabled bool) error {
	if l.enabledMap == nil {
		return nil
	}
	key := uint32(0)
	val := uint32(0)
	if enabled {
		val = 1
	}
	return l.enabledMap.Put(key, val)
}

// Detach detaches the eBPF program from the hook
func (l *Loader) Detach() error {
	var errs []error

	_ = l.SetEnabled(false)

	if l.xdpLink != nil {
		if err := l.xdpLink.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to detach XDP: %w", err))
		}
		l.xdpLink = nil
	}

	if l.tcLink != nil && l.tcIface != nil {
		if filter, ok := l.tcLink.(*netlink.BpfFilter); ok {
			if err := netlink.FilterDel(filter); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove TC filter: %w", err))
			}
		}
		l.tcLink = nil
		l.tcIface = nil
	}

	if l.socketLink != nil {
		if err := l.socketLink.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to detach socket: %w", err))
		}
		l.socketLink = nil
	}

	if l.collection != nil {
		l.collection.Close()
		l.collection = nil
	}

	// Clean up pinned maps
	if l.config.PinMaps {
		_ = os.RemoveAll(BPFPinPath)
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during detach: %v", errs)
	}

	return nil
}

// GetStats retrieves statistics from the eBPF program
func (l *Loader) GetStats() (*Stats, error) {
	if l.collection == nil {
		return nil, fmt.Errorf("collection not initialized")
	}

	key := uint32(0)
	stats := &Stats{}

	if l.packetsMap != nil {
		packets, err := l.sumPerCPUMap(l.packetsMap, key)
		if err == nil {
			stats.PacketsProcessed = packets
		}
	}

	if l.loopsMap != nil {
		loops, err := l.sumPerCPUMap(l.loopsMap, key)
		if err == nil {
			stats.TotalLoops = loops
		}
	}

	if l.bytesMap != nil {
		bytes, err := l.sumPerCPUMap(l.bytesMap, key)
		if err == nil {
			stats.TotalBytes = bytes
		}
	}

	if l.timeMap != nil {
		timeNs, err := l.sumPerCPUMap(l.timeMap, key)
		if err == nil {
			stats.TotalTimeNs = timeNs
		}
	}

	if stats.PacketsProcessed > 0 {
		stats.LoopsPerPacket = float64(stats.TotalLoops) / float64(stats.PacketsProcessed)
		stats.AvgTimePerPacket = float64(stats.TotalTimeNs) / float64(stats.PacketsProcessed)
	}

	return stats, nil
}

// sumPerCPUMap reads all CPU values from a per-CPU array map and sums them
func (l *Loader) sumPerCPUMap(m *ebpf.Map, key uint32) (uint64, error) {
	var values []uint64
	if err := m.Lookup(key, &values); err != nil {
		var singleValue uint64
		if err := m.Lookup(key, &singleValue); err != nil {
			return 0, err
		}
		return singleValue, nil
	}

	var total uint64
	for _, v := range values {
		total += v
	}
	return total, nil
}

// GetPinPath returns the path where maps are pinned
func GetPinPath() string {
	return BPFPinPath
}

// GetMapPaths returns paths to individual pinned maps
func GetMapPaths() map[string]string {
	return map[string]string{
		"config_loop_count": filepath.Join(BPFPinPath, "config_loop_count"),
		"config_enabled":    filepath.Join(BPFPinPath, "config_enabled"),
		"stats_packets":     filepath.Join(BPFPinPath, "stats_packets"),
		"stats_loops":       filepath.Join(BPFPinPath, "stats_loops"),
		"stats_bytes":       filepath.Join(BPFPinPath, "stats_bytes"),
		"stats_time_ns":     filepath.Join(BPFPinPath, "stats_time_ns"),
	}
}

// makeMapsReadable makes pinned BPF maps world-readable so ebpf-exporter
// can read them without requiring root privileges
func makeMapsReadable() {
	mapNames := []string{
		"config_loop_count",
		"config_enabled",
		"stats_packets",
		"stats_loops",
		"stats_bytes",
		"stats_time_ns",
	}

	// Make the directory readable
	os.Chmod(BPFPinPath, 0755)

	// Make each map file readable
	for _, name := range mapNames {
		path := filepath.Join(BPFPinPath, name)
		os.Chmod(path, 0644)
	}
}
