package ebpf

import (
	"fmt"
	"os"

	"github.com/cilium/ebpf"
)

// LoadXDPCollection loads the XDP eBPF collection from file system
func LoadXDPCollection() (*ebpf.CollectionSpec, error) {
	// Try multiple paths (relative to current working directory and relative to binary)
	paths := []string{
		"bpf/obj/xdp.o",
		"./bpf/obj/xdp.o",
		"../../bpf/obj/xdp.o",
		"/app/bpf/obj/xdp.o", // Docker path
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			spec, err := ebpf.LoadCollectionSpec(path)
			if err == nil {
				return spec, nil
			}
		}
	}

	return nil, fmt.Errorf("failed to load XDP collection: not found in any of the expected paths: %v", paths)
}

// LoadTCCollection loads the TC eBPF collection from file system
func LoadTCCollection() (*ebpf.CollectionSpec, error) {
	paths := []string{
		"bpf/obj/tc.o",
		"./bpf/obj/tc.o",
		"../../bpf/obj/tc.o",
		"/app/bpf/obj/tc.o", // Docker path
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			spec, err := ebpf.LoadCollectionSpec(path)
			if err == nil {
				return spec, nil
			}
		}
	}

	return nil, fmt.Errorf("failed to load TC collection: not found in any of the expected paths: %v", paths)
}

// LoadSocketCollection loads the Socket eBPF collection from file system
func LoadSocketCollection() (*ebpf.CollectionSpec, error) {
	paths := []string{
		"bpf/obj/socket.o",
		"./bpf/obj/socket.o",
		"../../bpf/obj/socket.o",
		"/app/bpf/obj/socket.o", // Docker path
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			spec, err := ebpf.LoadCollectionSpec(path)
			if err == nil {
				return spec, nil
			}
		}
	}

	return nil, fmt.Errorf("failed to load Socket collection: not found in any of the expected paths: %v", paths)
}
