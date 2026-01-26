/*
Package ebpf provides the eBPF program loader and manager.

This package handles loading, attaching, and detaching eBPF programs
for the stress test application. It supports multiple hook types:
  - XDP (eXpress Data Path)
  - TC (Traffic Control) ingress/egress
  - Socket filters

The loader manages eBPF maps for configuration and statistics collection.
*/
package ebpf

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

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
		// Default to stress.o which contains all programs
		config.Program = "bpf/obj/stress.o"
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

	// Load collection
	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}
	l.collection = coll

	// Get configuration maps
	l.loopCountMap = coll.Maps["config_loop_count"]
	l.enabledMap = coll.Maps["config_enabled"]
	
	// Get statistics maps
	l.packetsMap = coll.Maps["stats_packets"]
	l.loopsMap = coll.Maps["stats_loops"]
	l.bytesMap = coll.Maps["stats_bytes"]
	l.timeMap = coll.Maps["stats_time_ns"]

	// Fallback to old map names for backwards compatibility
	if l.loopCountMap == nil {
		l.loopCountMap = coll.Maps["loop_count_map"]
	}
	if l.packetsMap == nil {
		l.packetsMap = coll.Maps["packets_processed_map"]
	}
	if l.loopsMap == nil {
		l.loopsMap = coll.Maps["total_loops_map"]
	}

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
	switch l.config.Hook {
	case HookXDP:
		return LoadXDPCollection()
	case HookTCIngress, HookTCEgress:
		return LoadTCCollection()
	case HookSocket:
		return LoadSocketCollection()
	default:
		return nil, fmt.Errorf("unsupported hook type: %s", l.config.Hook)
	}
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
		Flags:     link.XDPGenericMode, // Use generic mode for compatibility
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

	// Get program file descriptor
	progFD := prog.FD()
	if progFD < 0 {
		return fmt.Errorf("invalid program file descriptor")
	}

	// TC requires clsact qdisc to be attached to the interface first
	// clsact is a special qdisc that allows attaching BPF programs
	clsactQdisc := &netlink.GenericQdisc{
		QdiscAttrs: netlink.QdiscAttrs{
			LinkIndex: iface.Attrs().Index,
			Handle:    netlink.MakeHandle(0xFFFF, 0),
			Parent:    netlink.HANDLE_CLSACT,
		},
		QdiscType: "clsact",
	}

	// Delete existing clsact qdisc if present (ignore errors)
	_ = netlink.QdiscDel(clsactQdisc)
	
	// Add clsact qdisc
	if err := netlink.QdiscAdd(clsactQdisc); err != nil {
		return fmt.Errorf("failed to add clsact qdisc to %s: %w (try using --hook xdp instead)", l.config.Interface, err)
	}

	// Determine parent handle for ingress/egress
	var parent uint32
	if ingress {
		parent = netlink.HANDLE_MIN_INGRESS
	} else {
		parent = netlink.HANDLE_MIN_EGRESS
	}

	// Create BPF filter
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
		// Cleanup qdisc on failure
		_ = netlink.QdiscDel(clsactQdisc)
		return fmt.Errorf("failed to add TC filter: %w", err)
	}

	// Store interface and direction for cleanup
	l.tcLink = filter
	l.tcIface = &iface
	l.tcIngress = ingress

	return nil
}

// attachSocket attaches the program to socket filter
func (l *Loader) attachSocket() error {
	// Socket filter attachment requires a socket file descriptor
	// This is typically used with raw sockets for packet capture
	return fmt.Errorf("socket filter attachment requires socket FD - use XDP or TC instead")
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
		return nil // Silently ignore if map doesn't exist
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

	// Disable the program first
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

	// Read packets processed (per-CPU map - need to sum all CPUs)
	if l.packetsMap != nil {
		packets, err := l.sumPerCPUMap(l.packetsMap, key)
		if err == nil {
			stats.PacketsProcessed = packets
		}
	}

	// Read total loops
	if l.loopsMap != nil {
		loops, err := l.sumPerCPUMap(l.loopsMap, key)
		if err == nil {
			stats.TotalLoops = loops
		}
	}

	// Read total bytes
	if l.bytesMap != nil {
		bytes, err := l.sumPerCPUMap(l.bytesMap, key)
		if err == nil {
			stats.TotalBytes = bytes
		}
	}

	// Read total time
	if l.timeMap != nil {
		timeNs, err := l.sumPerCPUMap(l.timeMap, key)
		if err == nil {
			stats.TotalTimeNs = timeNs
		}
	}

	// Calculate averages
	if stats.PacketsProcessed > 0 {
		stats.LoopsPerPacket = float64(stats.TotalLoops) / float64(stats.PacketsProcessed)
		stats.AvgTimePerPacket = float64(stats.TotalTimeNs) / float64(stats.PacketsProcessed)
	}

	return stats, nil
}

// sumPerCPUMap reads all CPU values from a per-CPU array map and sums them
func (l *Loader) sumPerCPUMap(m *ebpf.Map, key uint32) (uint64, error) {
	// For per-CPU maps, we need to iterate through all possible CPUs
	// The map returns a slice of values, one per CPU
	var values []uint64
	if err := m.Lookup(key, &values); err != nil {
		// Try single value lookup as fallback
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

// WaitForInterrupt waits for interrupt signal and detaches
func (l *Loader) WaitForInterrupt() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nDetaching eBPF program...")
	l.Detach()
}
