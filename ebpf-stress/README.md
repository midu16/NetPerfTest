# eBPF Stress Test Program

Creates eBPF hooks on network interfaces to simulate CPU overhead per packet.

## Overview

This component attaches eBPF programs to network hooks (XDP, TC, or Socket) and performs N configurable loop iterations per packet to simulate kernel CPU overhead.

Maps are pinned to `/sys/fs/bpf/ebpf-stress/` for the `ebpf-exporter` component to read and export Prometheus metrics.

## Architecture

```
┌──────────────────┐     ┌─────────────────────────────┐
│   ebpf-stress    │     │   /sys/fs/bpf/ebpf-stress/  │
│                  │────▶│   - stats_packets           │
│  Loads & attaches│     │   - stats_loops             │
│  eBPF programs   │     │   - stats_bytes             │
└──────────────────┘     │   - stats_time_ns           │
                         └─────────────────────────────┘
                                      │
                                      ▼
                         ┌─────────────────────────────┐
                         │      ebpf-exporter          │
                         │                             │
                         │  Reads pinned maps          │
                         │  Exposes /metrics           │
                         └─────────────────────────────┘
```

## Quick Start

```bash
# Build
make build

# Run with XDP hook on loopback
sudo ./bin/ebpf-stress --hook xdp --interface lo --loops 1000

# Run with TC ingress hook
sudo ./bin/ebpf-stress --hook tc-ingress --interface eth0 --loops 500
```

## Options

| Flag | Default | Description |
|------|---------|-------------|
| `--hook` | `xdp` | Hook type: xdp, tc-ingress, tc-egress, socket |
| `--interface` | `lo` | Network interface to attach to |
| `--loops` | `1000` | Loop iterations per packet (max 2500) |
| `--duration` | `0` | Duration to run (0 = infinite) |
| `--pin-maps` | `true` | Pin maps for ebpf-exporter |
| `--verbose` | `false` | Enable verbose output |

## Hook Types

- **XDP**: Earliest hook point, highest performance
- **TC Ingress**: Traffic Control ingress path
- **TC Egress**: Traffic Control egress path  
- **Socket**: Socket filter (requires socket FD)

## Integration with ebpf-exporter

1. Start ebpf-stress with `--pin-maps` (default)
2. Run ebpf-exporter to read pinned maps
3. Scrape metrics at `http://localhost:9091/metrics`

## Requirements

- Linux kernel 5.4+
- clang (for BPF compilation)
- Go 1.21+
- Root privileges (for eBPF operations)

## Build

```bash
# Build everything
make build

# Build only eBPF program
make build-bpf

# Build only Go binary
make build-go

# Install to system
sudo make install
```

## License

GPL-2.0 (eBPF code), MIT (Go code)
