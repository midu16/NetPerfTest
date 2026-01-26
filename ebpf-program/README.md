# eBPF Stress Test Program

**Configurable N Loops Per Packet for Network Stack Stress Testing**

This program attaches eBPF programs to network hooks (XDP, TC, Socket) and performs N loop iterations per packet to simulate CPU overhead. It's designed to replicate the AF_PACKET socket performance degradation issue.

## Overview

The AF_PACKET socket issue causes reserved CPUs to spend 90-100% time in system interrupt (si) mode when many AF_PACKET sockets are open. This eBPF program provides a controlled way to simulate similar CPU overhead per packet.

**Formula:** `CPU load = PPS × (fixed-cost + per-loop-cost × N)`

## Features

- **Configurable Loop Count (N):** Set 1 to 10,000 iterations per packet
- **Multiple Hook Points:**
  - XDP (eXpress Data Path) - Earliest, fastest
  - TC Ingress/Egress - Traffic Control hooks
  - Socket Filter - Per-socket filtering
- **Prometheus Metrics:** Built-in metrics endpoint
- **Per-CPU Statistics:** High-performance lock-free counters
- **Dynamic Configuration:** Change loop count at runtime
- **Kernel Compatibility:** Works with kernel 5.4+ (uses verifier-friendly bounded loops)

> **Note:** For more than 10,000 loops per packet, use the kernel module in `kernel-module/` which has no verifier limits.

## Quick Start

### Prerequisites

```bash
# Install dependencies (Fedora/RHEL)
sudo dnf install clang llvm golang

# Check kernel version (5.17+ recommended for bpf_loop)
uname -r
```

### Build

```bash
cd ebpf-program
make build
```

### Run

```bash
# XDP hook with 10,000 loops per packet
sudo ./bin/ebpf-stress --hook xdp --interface eth0 --loops 10000

# TC ingress hook with metrics
sudo ./bin/ebpf-stress --hook tc-ingress --interface eth0 --loops 5000 --metrics

# Run for 5 minutes
sudo ./bin/ebpf-stress --hook xdp --interface eth0 --loops 10000 --duration 5m
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     eBPF Stress Test                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Userspace (Go)                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  main.go                                                 │   │
│  │  ├── CLI (cobra)                                        │   │
│  │  ├── Metrics Server (Prometheus)                        │   │
│  │  └── Statistics Collection                              │   │
│  └─────────────────────────────────────────────────────────┘   │
│                           │                                     │
│                           │ eBPF Maps (config, stats)           │
│                           ▼                                     │
│  Kernel (eBPF)                                                  │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  stress.c                                                │   │
│  │  ├── xdp_stress_prog    (XDP hook)                      │   │
│  │  ├── tc_stress_prog     (TC hook)                       │   │
│  │  └── socket_stress_prog (Socket filter)                 │   │
│  │                                                          │   │
│  │  Per packet:                                             │   │
│  │    1. Read N from config_loop_count map                 │   │
│  │    2. Execute N iterations (bpf_loop or bounded loop)   │   │
│  │    3. Update per-CPU statistics                         │   │
│  │    4. Return PASS (continue processing)                 │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## eBPF Maps

| Map Name | Type | Description |
|----------|------|-------------|
| `config_loop_count` | ARRAY | Loop iterations N (set from userspace) |
| `config_enabled` | ARRAY | Enable/disable flag |
| `stats_packets` | PERCPU_ARRAY | Packets processed counter |
| `stats_loops` | PERCPU_ARRAY | Total loop iterations |
| `stats_bytes` | PERCPU_ARRAY | Total bytes processed |
| `stats_time_ns` | PERCPU_ARRAY | Total processing time (ns) |

## CLI Reference

```
Usage:
  ebpf-stress [flags]

Flags:
  -H, --hook string           eBPF hook type: xdp, tc-ingress, tc-egress, socket (default "xdp")
  -i, --interface string      Network interface to attach to (default "eth0")
  -n, --loops uint32          Number of loop iterations per packet (N) (default 1000)
  -d, --duration duration     Duration to run (0 = infinite)
  -v, --verbose               Enable verbose output
      --metrics               Enable Prometheus metrics endpoint
  -p, --metrics-port int      Port for Prometheus metrics endpoint (default 9091)
      --stats-interval duration   Interval for printing statistics (default 5s)
  -h, --help                  Help for ebpf-stress

Examples:
  ebpf-stress --hook xdp --interface eth0 --loops 10000
  ebpf-stress --hook tc-ingress --interface eth0 --loops 5000 --metrics
  ebpf-stress --hook xdp --interface eth0 --loops 10000 --duration 5m
```

## Prometheus Metrics

When running with `--metrics`, the following metrics are exposed at `http://localhost:9091/metrics`:

| Metric | Type | Description |
|--------|------|-------------|
| `ebpf_stress_packets_total` | Counter | Total packets processed |
| `ebpf_stress_loops_total` | Counter | Total loop iterations performed |
| `ebpf_stress_loops_per_packet` | Gauge | Average loops per packet |
| `ebpf_stress_packets_per_second` | Gauge | Packets processed per second |
| `ebpf_stress_loops_per_second` | Gauge | Loop iterations per second |
| `ebpf_stress_uptime_seconds` | Gauge | Time since program started |
| `ebpf_stress_configured_loops` | Gauge | Configured loop count (N) |

## Kernel Compatibility

### Bounded Loops (All Kernels 5.4+)

The program uses verifier-friendly bounded nested loops that work reliably across all supported kernels. Maximum iterations: **10,000** (100 outer × 100 inner loops).

This approach was chosen over `bpf_loop()` for maximum compatibility, as `bpf_loop()` can have verification issues on some kernel configurations.

### Check Your Kernel

```bash
# Check kernel version (5.4+ required)
uname -r
```

### Need More Than 10,000 Loops?

For higher iteration counts, use the kernel module in `kernel-module/`:

```bash
cd kernel-module
make
sudo insmod stress_module.ko loop_count=100000
```

The kernel module has **no verifier limits** and can loop any number of times.

## AF_PACKET Stress Tool (Combined Approach)

The `afpacket-stress` tool combines **high PPS traffic generation** with **100+ native AF_PACKET sockets** to accurately replicate the real AF_PACKET performance issue.

### Quick Start

```bash
# Build
make build

# Run with 200 sockets + 500K PPS (progressive mode)
sudo ./bin/afpacket-stress --sockets 200 --pps 500000 --duration 120s

# Maximum stress mode: 500 sockets + max PPS
sudo ./bin/afpacket-stress --sockets 500 --max --duration 180s --progressive

# Using Makefile targets
make run-afpacket      # 200 sockets, 500K PPS
make run-afpacket-max  # 500 sockets, max PPS
```

### AF_PACKET Stress CLI Reference

```
Usage:
  afpacket-stress [flags]

Flags:
  --sockets int        Number of AF_PACKET sockets to create (default 200)
  --pps int            Target packets per second (default 500000)
  --max                Maximum rate mode (ignore PPS limit)
  --duration duration  Test duration (default 2m)
  --size int           Packet size in bytes (default 64)
  --workers int        Traffic generator workers (0 = auto)
  --interface string   Network interface for sockets (default "lo")
  --progressive        Progressively add sockets during test (default true)
  --socket-batch int   Sockets to add per batch (default 50)
  --batch-delay dur    Delay between socket batches (default 5s)
  --stats-interval dur Statistics reporting interval (default 2s)
  --verbose            Verbose output
```

### How It Works

1. **Creates native AF_PACKET sockets** using Go syscalls (no tcpdump dependency)
2. **Generates high PPS UDP traffic** to localhost (packets go through kernel stack)
3. **Each AF_PACKET socket receives a copy** of every packet (kernel overhead)
4. **Monitors CPU softirq** in real-time
5. **Reports success** when softirq exceeds 50% threshold

### Expected Results

| Sockets | PPS | Expected Softirq |
|---------|-----|------------------|
| 100 | 100K | 20-40% |
| 200 | 500K | 40-60% |
| 500 | Max | 60-80%+ |

### Success Criteria

```
═══════════════════════════════════════════════════════════════════
  ✅ AF_PACKET ISSUE REPLICATED!
     Peak softirq 72.3% exceeded 50% threshold
═══════════════════════════════════════════════════════════════════
```

## Directory Structure

```
ebpf-program/
├── bpf/                     # eBPF C source code
│   ├── stress.c            # Main stress program (XDP, TC, Socket)
│   ├── xdp.c               # Legacy XDP program
│   ├── tc.c                # Legacy TC program
│   ├── socket.c            # Legacy Socket program
│   └── obj/                # Compiled BPF objects
├── cmd/
│   ├── ebpf-stress/
│   │   └── main.go         # eBPF stress CLI
│   ├── traffic-gen/
│   │   └── main.go         # High-performance traffic generator
│   └── afpacket-stress/
│       └── main.go         # Combined AF_PACKET + PPS stress tool
├── pkg/
│   └── ebpf/
│       ├── loader.go       # eBPF program loader
│       └── loader_impl.go  # Object file loading
├── kernel-module/          # Alternative kernel module (no verifier limits)
├── bin/                    # Compiled binaries
│   ├── ebpf-stress        # eBPF stress program
│   ├── traffic-gen        # Traffic generator
│   └── afpacket-stress    # Combined AF_PACKET stress tool
├── Dockerfile              # Container build
├── Makefile               # Build system
├── go.mod                 # Go dependencies
└── README.md              # This file
```

## Use Cases

### 1. Replicate AF_PACKET Socket Issue

```bash
# Simulate the CPU overhead from 1000 AF_PACKET sockets
# Each socket adds overhead per packet, so we can simulate with loops
sudo ./bin/ebpf-stress --hook xdp --interface eth0 --loops 100000 --metrics

# Monitor CPU softirq time
watch -n 1 'mpstat -P ALL 1 1 | grep -E "CPU|all"'
```

### 2. Network Performance Baseline

```bash
# Establish baseline with low loop count
sudo ./bin/ebpf-stress --hook xdp --interface eth0 --loops 100 --duration 1m

# Increase load progressively
sudo ./bin/ebpf-stress --hook xdp --interface eth0 --loops 10000 --duration 1m
sudo ./bin/ebpf-stress --hook xdp --interface eth0 --loops 100000 --duration 1m
```

### 3. Prometheus Integration

```bash
# Run with metrics
sudo ./bin/ebpf-stress --hook xdp --interface eth0 --loops 10000 --metrics

# Query metrics
curl http://localhost:9091/metrics
curl http://localhost:9091/stats  # JSON format
```

## Comparison: eBPF vs Kernel Module

| Feature | eBPF | Kernel Module |
|---------|------|---------------|
| Max Loops | 8M (bpf_loop) or ~10K (bounded) | Unlimited |
| Safety | Verified by kernel | Can crash system |
| Installation | No reboot needed | May need reboot |
| Privileges | CAP_BPF + CAP_NET_ADMIN | Root + module loading |
| Portability | Works across kernel versions | Kernel-specific |

For unlimited loop counts without verifier restrictions, see `kernel-module/`.

## Development

### Build and Test

```bash
# Full build with linting
make all

# Run tests
make test

# Run integration tests (requires root)
make test-integration

# Check BPF program info
make bpf-info
```

### Docker

```bash
# Build container
make docker-build

# Run in container
make docker-run
```

## References

- [AF_PACKET Socket Issue Analysis](../Markdown/AF_PACKET_SOCKET_METRICS_ANALYSIS.md)
- [NetPerfTest Repository](https://github.com/midu16/NetPerfTest)
- [RHEL Bug RHEL-83393](https://issues.redhat.com/browse/RHEL-83393)
- [eBPF Documentation](https://ebpf.io/)
- [cilium/ebpf Library](https://github.com/cilium/ebpf)

## License

GPL-2.0 (required for eBPF programs)
