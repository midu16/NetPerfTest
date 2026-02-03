# eBPF Metrics Exporter

Prometheus exporter for eBPF-based network metrics with two operational modes:

1. **Mode 1**: Read metrics from `ebpf-stress` (stress testing)
2. **Mode 2**: Independent kernel stack latency measurement (cost per packet)

## Overview

This component can:
- Read eBPF maps pinned by `ebpf-stress` and export stress test metrics
- **Independently measure kernel stack cost per packet** using its own eBPF programs
- Export per-interface latency metrics showing how long packets spend in the kernel

The independent latency measurement is useful for identifying packet processing overhead regardless of AF_PACKET issues, nf_table rules, or other kernel-level costs.

## Architecture

```
┌─────────────────────────────────────┐
│  /sys/fs/bpf/ebpf-stress/           │
│  - stats_packets (per-CPU array)    │
│  - stats_loops                      │
│  - stats_bytes                      │
│  - stats_time_ns  ← interrupt time  │
│  - config_loop_count                │
└─────────────┬───────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│      ebpf-exporter                  │
│                                     │
│  Reads pinned maps                  │
│  Per-interface metrics              │
│  YAML configuration                 │
│                                     │
│  HTTP endpoints:                    │
│    - /metrics                       │
│    - /health                        │
│    - /ready                         │
│    - /config                        │
└─────────────┬───────────────────────┘
              │
              ▼
┌─────────────────────────────────────┐
│       Prometheus                    │
│                                     │
│  Scrapes metrics                    │
│  Stores time series                 │
└─────────────────────────────────────┘
```

## Quick Start

### Mode 1: Read ebpf-stress metrics
```bash
# Build Go binary
make build

# Run (requires ebpf-stress running with --pin-maps)
./bin/ebpf-exporter --config ebpf-exporter.yaml

# View metrics
curl http://localhost:9091/metrics
```

### Mode 2: Independent Kernel Stack Latency Measurement
```bash
# Build eBPF program + Go binary
make build-all

# Run as root (required to attach eBPF programs)
sudo ./bin/ebpf-exporter --config ebpf-exporter.yaml

# View kernel stack cost metrics
curl -s http://localhost:9091/metrics | grep kernel_stack
```

## Configuration

### YAML Configuration File

Create `ebpf-exporter.yaml`:

```yaml
# Server configuration
server:
  port: 9091
  poll_interval: 5s

# eBPF map configuration
ebpf:
  pin_path: /sys/fs/bpf/ebpf-stress

# Interface configuration
# Use "*" to auto-discover all interfaces
interfaces:
  - name: lo
    enabled: true
  - name: eth0
    enabled: true
  - name: ens192
    enabled: true

# Metrics configuration
metrics:
  packets_total: true
  loops_total: true
  bytes_total: true
  processing_time_ns_total: true
  packets_per_second: true
  loops_per_second: true

# Logging
logging:
  verbose: false
  format: text
```

### CLI Options

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | - | Path to YAML configuration file |
| `--port` | `9091` | Prometheus metrics port |
| `--pin-path` | `/sys/fs/bpf/ebpf-stress` | Path to pinned maps |
| `--poll-interval` | `5s` | Map reading interval |
| `--interfaces` | - | Comma-separated interfaces (e.g., `lo,eth0`) |
| `--verbose` | `false` | Enable verbose output |

## Metrics Exposed

### Per-Interface Metrics (with `interface` and `hook` labels)

| Metric | Type | Description |
|--------|------|-------------|
| `ebpf_stress_packets_total` | Gauge | Total packets processed |
| `ebpf_stress_loops_total` | Gauge | Total loop iterations |
| `ebpf_stress_bytes_total` | Gauge | Total bytes processed |
| `ebpf_stress_processing_time_ns_total` | Gauge | **Total time in softIRQ/interrupt** |

### Per-Interface Rate Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `ebpf_stress_packets_per_second` | Gauge | Packets/sec rate |
| `ebpf_stress_loops_per_second` | Gauge | Loops/sec rate |
| `ebpf_stress_bytes_per_second` | Gauge | Bytes/sec rate |
| `ebpf_stress_loops_per_packet` | Gauge | Average loops per packet |
| `ebpf_stress_avg_time_per_packet_ns` | Gauge | **Avg interrupt time per packet** |

### Global Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `ebpf_stress_uptime_seconds` | Gauge | Exporter uptime |
| `ebpf_stress_configured_loops` | Gauge | Configured loop count |
| `ebpf_exporter_up` | Gauge | Connection status (1=connected) |
| `ebpf_stress_interface_up` | Gauge | Per-interface eBPF attachment status |

## Mode 2: Kernel Stack Latency Metrics

When `latency_measurement.enabled: true`, the exporter attaches its own eBPF programs to measure the actual cost per packet traversing the kernel network stack.

### Metrics Exported

| Metric | Description |
|--------|-------------|
| `kernel_stack_latency_avg_ns` | **Average cost per packet** in nanoseconds |
| `kernel_stack_latency_min_ns` | Minimum observed latency |
| `kernel_stack_latency_max_ns` | Maximum observed latency |
| `kernel_stack_latency_ns_total` | Total time packets spent in kernel |
| `kernel_stack_packets_total` | Total packets measured |
| `kernel_stack_latency_histogram` | Latency distribution buckets |
| `kernel_stack_xdp_packets_total` | Packets seen at XDP layer |
| `kernel_stack_tc_ingress_packets_total` | Packets at TC ingress |
| `kernel_stack_tc_egress_packets_total` | Packets at TC egress |

### How It Works

1. **XDP Program** (earliest hook) records packet arrival timestamp
2. **TC Programs** calculate time delta from XDP arrival
3. **Statistics** are aggregated per-interface and globally
4. **Histogram** shows distribution of latencies

This measures the actual kernel processing time per packet, which includes:
- Network driver processing
- XDP/TC processing
- Protocol stack overhead
- Queue delays
- Any additional processing (nf_tables, eBPF, etc.)

### Configuration

```yaml
latency_measurement:
  enabled: true
  pin_path: /sys/fs/bpf/ebpf-exporter
  interfaces:
    - lo
    - eth0
```

## Determining Interrupt/SoftIRQ Time

The key metrics for analyzing packet processing time in interrupt context:

### 1. Total Interrupt Time
```promql
ebpf_stress_processing_time_ns_total{interface="eth0"}
```

### 2. Average Time Per Packet (in softIRQ)
```promql
ebpf_stress_avg_time_per_packet_ns{interface="eth0"}
```

### 3. Interrupt Time Per Second
```promql
rate(ebpf_stress_processing_time_ns_total{interface="eth0"}[1m])
```

### 4. Percentage of Time in Interrupt (approximate)
```promql
# Time spent in interrupt per second (as fraction of 1 second)
rate(ebpf_stress_processing_time_ns_total[1m]) / 1e9 * 100
```

### Warning Thresholds

| Metric | Warning | Critical |
|--------|---------|----------|
| `avg_time_per_packet_ns` | > 10,000 (10μs) | > 100,000 (100μs) |
| Interrupt % per second | > 10% | > 50% |

## HTTP Endpoints

- `/metrics` - Prometheus metrics
- `/health` - Health check (always returns 200 OK)
- `/ready` - Readiness check (200 if connected to maps)
- `/config` - Current configuration (JSON)

## Integration

### Prometheus Configuration

```yaml
scrape_configs:
  - job_name: 'ebpf-stress'
    static_configs:
      - targets: ['localhost:9091']
    scrape_interval: 5s
```

### Grafana Dashboard Queries

```promql
# Packets per second by interface
ebpf_stress_packets_per_second

# Interrupt time per packet (microseconds)
ebpf_stress_avg_time_per_packet_ns / 1000

# Total softIRQ time rate
rate(ebpf_stress_processing_time_ns_total[1m])

# Bytes throughput
ebpf_stress_bytes_per_second

# CPU overhead percentage (approximate)
(ebpf_stress_avg_time_per_packet_ns * ebpf_stress_packets_per_second) / 1e9 * 100
```

## Usage with ebpf-stress

1. Start ebpf-stress with map pinning:
   ```bash
   sudo ebpf-stress --hook xdp --interface eth0 --loops 1000 --pin-maps
   ```

2. Start the exporter with config:
   ```bash
   ebpf-exporter --config ebpf-exporter.yaml
   ```

3. Generate traffic:
   ```bash
   traffic-gen --pps 100000 --duration 60s
   ```

4. View metrics:
   ```bash
   curl -s http://localhost:9091/metrics | grep ebpf_stress
   ```

## Example Output

```
# HELP ebpf_stress_packets_total Total packets processed by eBPF program
# TYPE ebpf_stress_packets_total gauge
ebpf_stress_packets_total{hook="xdp",interface="lo"} 1.234567e+06

# HELP ebpf_stress_processing_time_ns_total Total processing time in nanoseconds
# TYPE ebpf_stress_processing_time_ns_total gauge
ebpf_stress_processing_time_ns_total{hook="xdp",interface="lo"} 4.567890123e+10

# HELP ebpf_stress_avg_time_per_packet_ns Average processing time per packet
# TYPE ebpf_stress_avg_time_per_packet_ns gauge
ebpf_stress_avg_time_per_packet_ns{interface="lo"} 37000
```

## Requirements

- Go 1.21+
- ebpf-stress running with `--pin-maps` enabled

## Build

```bash
make build
```

## License

MIT
