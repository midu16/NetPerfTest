package ebpf

import (
	"fmt"
	"os"

	"github.com/cilium/ebpf"
)

// Object file paths to search
var (
	stressPaths = []string{
		"bpf/obj/stress.o",
		"./bpf/obj/stress.o",
		"../../bpf/obj/stress.o",
		"/app/bpf/obj/stress.o", // Docker path
	}
	
	xdpPaths = []string{
		"bpf/obj/stress.o",      // Prefer stress.o (new)
		"bpf/obj/xdp.o",         // Fallback to legacy
		"./bpf/obj/stress.o",
		"./bpf/obj/xdp.o",
		"../../bpf/obj/stress.o",
		"../../bpf/obj/xdp.o",
		"/app/bpf/obj/stress.o",
		"/app/bpf/obj/xdp.o",
	}
	
	tcPaths = []string{
		"bpf/obj/stress.o",      // Prefer stress.o (new)
		"bpf/obj/tc.o",          // Fallback to legacy
		"./bpf/obj/stress.o",
		"./bpf/obj/tc.o",
		"../../bpf/obj/stress.o",
		"../../bpf/obj/tc.o",
		"/app/bpf/obj/stress.o",
		"/app/bpf/obj/tc.o",
	}
	
	socketPaths = []string{
		"bpf/obj/stress.o",      // Prefer stress.o (new)
		"bpf/obj/socket.o",      // Fallback to legacy
		"./bpf/obj/stress.o",
		"./bpf/obj/socket.o",
		"../../bpf/obj/stress.o",
		"../../bpf/obj/socket.o",
		"/app/bpf/obj/stress.o",
		"/app/bpf/obj/socket.o",
	}
)

// loadFromPaths attempts to load an eBPF collection from multiple paths
func loadFromPaths(paths []string) (*ebpf.CollectionSpec, error) {
	var lastErr error
	
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue // File doesn't exist
		}
		
		spec, err := ebpf.LoadCollectionSpec(path)
		if err != nil {
			lastErr = err
			continue
		}
		
		return spec, nil
	}
	
	if lastErr != nil {
		return nil, fmt.Errorf("failed to load eBPF collection: %w (tried paths: %v)", lastErr, paths)
	}
	
	return nil, fmt.Errorf("eBPF object file not found in any of the expected paths: %v", paths)
}

// LoadStressCollection loads the unified stress eBPF collection
func LoadStressCollection() (*ebpf.CollectionSpec, error) {
	return loadFromPaths(stressPaths)
}

// LoadXDPCollection loads the XDP eBPF collection from file system
func LoadXDPCollection() (*ebpf.CollectionSpec, error) {
	return loadFromPaths(xdpPaths)
}

// LoadTCCollection loads the TC eBPF collection from file system
func LoadTCCollection() (*ebpf.CollectionSpec, error) {
	return loadFromPaths(tcPaths)
}

// LoadSocketCollection loads the Socket eBPF collection from file system
func LoadSocketCollection() (*ebpf.CollectionSpec, error) {
	return loadFromPaths(socketPaths)
}
