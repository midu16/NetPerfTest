# Traffic Generator

High-performance traffic generation tools for eBPF stress testing.

## Overview

This component provides tools for generating network traffic to stress test eBPF programs and replicate the AF_PACKET performance issue.

## Tools

### traffic-gen

High-performance UDP traffic generator optimized for maximum packets per second (PPS).

```bash
# Generate 500k PPS for 60 seconds
./bin/traffic-gen --pps 500000 --duration 60s

# Maximum rate mode (as fast as possible)
./bin/traffic-gen --max --duration 60s

# Custom packet size
./bin/traffic-gen --pps 100000 --size 128 --duration 30s
```

#### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--pps` | `500000` | Target packets per second |
| `--max` | `false` | Maximum rate mode (ignore PPS limit) |
| `--size` | `64` | Packet size in bytes |
| `--workers` | `auto` | Number of sender goroutines |
| `--duration` | `120s` | Test duration |
| `--target` | `127.0.0.1` | Target IP address |
| `--port` | `9999` | Base UDP port |

### afpacket-stress

AF_PACKET socket stress tool that combines high PPS traffic with multiple AF_PACKET sockets to replicate the RHEL-83393 performance issue.

```bash
# Create 200 sockets with 500k PPS
sudo ./bin/afpacket-stress --sockets 200 --pps 500000 --duration 120s

# Maximum stress test
sudo ./bin/afpacket-stress --sockets 500 --max --duration 180s --progressive

# Custom interface
sudo ./bin/afpacket-stress --interface eth0 --sockets 200 --max
```

#### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--sockets` | `200` | Number of AF_PACKET sockets |
| `--pps` | `500000` | Target packets per second |
| `--max` | `false` | Maximum rate mode |
| `--interface` | `lo` | Network interface |
| `--progressive` | `true` | Add sockets progressively |
| `--socket-batch` | `50` | Sockets per batch |
| `--batch-delay` | `5s` | Delay between batches |
| `--duration` | `120s` | Test duration |

## Quick Start

```bash
# Build
make build

# Run traffic generator
./bin/traffic-gen --pps 100000 --duration 30s

# Run AF_PACKET stress test (requires root)
sudo ./bin/afpacket-stress --sockets 200 --max --duration 60s
```

## Usage with ebpf-stress

1. Start ebpf-stress on the target interface:
   ```bash
   sudo ebpf-stress --hook xdp --interface lo --loops 1000
   ```

2. Start the exporter:
   ```bash
   ebpf-exporter --port 9091
   ```

3. Generate traffic:
   ```bash
   ./bin/traffic-gen --max --duration 60s
   ```

4. View metrics:
   ```bash
   curl http://localhost:9091/metrics
   ```

## AF_PACKET Issue Replication

The `afpacket-stress` tool replicates the issue described in RHEL-83393 where multiple AF_PACKET sockets cause excessive CPU softirq.

### Mechanism

1. Creates N raw AF_PACKET sockets bound to an interface
2. Generates high PPS UDP traffic to localhost
3. Each packet is delivered to ALL AF_PACKET sockets
4. This multiplication causes CPU softirq saturation

### Success Criteria

- Peak SoftIRQ >= 50%: Issue replicated
- Avg SoftIRQ >= 30%: Partial replication
- Lower values: May need more sockets or higher PPS

## Build

```bash
make build
```

## License

MIT
