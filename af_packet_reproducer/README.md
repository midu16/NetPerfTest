# AF_PACKET Socket Issue Reproducer

Go-based reproducer for the AF_PACKET socket performance degradation issue.

## Overview

This program creates multiple AF_PACKET sockets (via tcpdump) to demonstrate the kernel performance degradation that occurs when many af_packet sockets are open simultaneously.

## Reference

- **RHEL Bug**: https://issues.redhat.com/browse/RHEL-83393
- **Original Reproducer**: https://github.com/mrVectorz/randoms/tree/master/af_packet_issue
- **Airtel Case**: 04076665

## Building

```bash
go build -o af_packet_reproducer main.go
```

## Usage

```bash
# Create 200 sockets (default) - kubeconfig is required
./af_packet_reproducer -kubeconfig=./kubeconfig-sno1

# Create 500 sockets
./af_packet_reproducer -kubeconfig=./kubeconfig-sno1 -sockets=500

# Create sockets for 60 seconds then cleanup
./af_packet_reproducer -kubeconfig=./kubeconfig-sno1 -sockets=300 -duration=60

# Verbose output
./af_packet_reproducer -kubeconfig=./kubeconfig-sno1 -sockets=100 -verbose

# Custom capture directory
./af_packet_reproducer -kubeconfig=./kubeconfig-sno1 -sockets=200 -dir=/tmp/captures

# Collect metrics every 10 seconds and save charts to custom directory
./af_packet_reproducer -kubeconfig=./kubeconfig-sno1 -sockets=500 -metrics-interval=10 -metrics-dir=/tmp/metrics

# Run for 60 seconds with metrics collection from cluster Prometheus
./af_packet_reproducer -kubeconfig=./kubeconfig-sno1 -sockets=300 -duration=60 -metrics-dir=/tmp/metrics

# Run with verbose output and custom metrics directory
./af_packet_reproducer -kubeconfig=./kubeconfig-sno1 -sockets=200 -verbose -metrics-dir=/tmp/metrics -metrics-interval=10
```

## Flags

- `-kubeconfig <path>`: **REQUIRED** - Path to kubeconfig file (e.g., `./kubeconfig-sno1`)
- `-sockets <int>`: Number of af_packet sockets to create (default: 200)
- `-dir <path>`: Directory for packet captures (default: /data/pcaps)
- `-interface <name>`: Network interface to capture on (default: any)
- `-duration <seconds>`: Run duration in seconds, 0 = infinite (default: 0)
- `-verbose`: Enable verbose output (default: false)
- `-metrics-dir <path>`: Directory to save metrics PNG charts (default: /data/metrics)
- `-metrics-interval <seconds>`: Metrics collection interval in seconds (default: 5)

## How It Works

1. Creates N tcpdump processes, each opening an AF_PACKET socket
2. Each process captures packets to a separate file
3. Monitors the active processes
4. **Collects metrics periodically** (CPU usage, memory usage, network throughput, active socket count)
5. **Generates PNG charts** showing metrics evolution over time
6. Cleans up gracefully on interrupt (Ctrl+C) or after duration expires

## Metrics Collection

The program automatically collects comprehensive metrics from **Prometheus** at regular intervals. All metrics are queried using PromQL queries aligned with the metrics analysis document.

### CPU Metrics (from Prometheus)
- **Total CPU Usage**: Overall CPU utilization percentage
  - Query: `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`
- **System Interrupt (SoftIRQ) Time**: **CRITICAL METRIC** - Percentage of CPU time spent in softirq mode
  - Query: `100 * avg(rate(node_cpu_seconds_total{mode="softirq"}[5m]))`
- **CPU User Time**: Percentage of CPU time in user mode
  - Query: `100 * avg(rate(node_cpu_seconds_total{mode="user"}[5m]))`
- **CPU System Time**: Percentage of CPU time in system mode
  - Query: `100 * avg(rate(node_cpu_seconds_total{mode="system"}[5m]))`
- **CPU Idle Time**: Percentage of CPU time idle
  - Query: `100 * avg(rate(node_cpu_seconds_total{mode="idle"}[5m]))`

### Network Metrics (from Prometheus)
- **Network Throughput**: RX and TX rates in MB/s
  - Query: `sum(rate(node_network_receive_bytes_total{device!="lo"}[5m])) / 1024 / 1024`
- **Network Packet Rate**: RX and TX packets per second (PPS)
  - Query: `sum(rate(node_network_receive_packets_total{device!="lo"}[5m]))`
- **Network Packet Drops**: RX and TX packet drops per second
  - Query: `sum(rate(node_network_receive_drop_total{device!="lo"}[5m]))`
- **Network Errors**: RX and TX errors per second
  - Query: `sum(rate(node_network_receive_errs_total{device!="lo"}[5m]))`

### SoftIRQ Metrics (from Prometheus)
- **NET_RX SoftIRQ Rate**: Network receive softirq processing rate (per second)
  - Query: `sum(rate(node_softirqs_total{softirq="NET_RX"}[5m]))`
- **NET_TX SoftIRQ Rate**: Network transmit softirq processing rate (per second)
  - Query: `sum(rate(node_softirqs_total{softirq="NET_TX"}[5m]))`

### System Metrics
- **Active Sockets**: Number of currently active AF_PACKET sockets (local count)
- **Memory Usage**: System memory usage percentage
  - Query: `100 * (avg(node_memory_MemTotal_bytes) - avg(node_memory_MemAvailable_bytes)) / avg(node_memory_MemTotal_bytes)`

**Prometheus Configuration:**
- All metrics are automatically collected from the cluster's Prometheus instance
- The program automatically discovers Prometheus in `openshift-monitoring` or `monitoring` namespace
- Port forwarding is automatically set up to access Prometheus (localhost:9090)
- Node filtering is automatically configured based on all nodes in the cluster
- Authentication is handled automatically via kubeconfig credentials
- Uses 5-minute rate windows for all rate calculations

## Generated Charts

When the program exits (Ctrl+C or duration expires), it automatically generates PNG charts:

1. **active_sockets.png**: Active socket count over time
2. **cpu_usage.png**: Total CPU usage percentage over time
3. **cpu_softirq_time.png**: **CRITICAL** - System interrupt (softirq) time percentage over time (with 50% threshold line)
4. **cpu_breakdown.png**: CPU breakdown by mode (User, System, SoftIRQ, Idle)
5. **memory_usage.png**: Memory usage percentage over time
6. **network_throughput.png**: Network RX/TX throughput (MB/s) over time
7. **network_drops_errors.png**: Network packet drops and errors (RX/TX) over time
8. **softirq_rates.png**: SoftIRQ rates for NET_RX and NET_TX over time
9. **combined_metrics.png**: Combined overview showing sockets, CPU usage, SoftIRQ time, and memory usage

All charts are saved to the metrics directory (default: `/data/metrics`).

### Key Metrics to Monitor

**Critical Indicators of AF_PACKET Socket Issue:**
- **SoftIRQ Time > 50%**: Indicates severe network stack overload
- **SoftIRQ Time 10-50%**: Warning level - network stack under stress
- **Increasing NET_RX SoftIRQ Rate**: Correlates with socket count
- **Network Packet Drops**: May appear when CPU is overloaded
- **Network Throughput Degradation**: Measurable reduction in throughput

## Expected Impact

With increasing socket counts:

### Phase 1: 200 Sockets
- **SoftIRQ Time**: 5-10% increase (baseline degradation)
- **Throughput**: 70-90% of baseline
- **Packet Drops**: 0 or minimal
- **CPU Utilization**: 50-60% on reserved CPUs (if configured)

### Phase 2: 500 Sockets
- **SoftIRQ Time**: 15-25% increase (moderate degradation)
- **Throughput**: 50-70% of baseline
- **Packet Drops**: May start appearing
- **CPU Utilization**: 70-80% on reserved CPUs

### Phase 3: 700 Sockets
- **SoftIRQ Time**: 30-40% increase (heavy degradation)
- **Throughput**: 30-50% of baseline
- **Packet Drops**: Significant drops observed
- **CPU Utilization**: 90-100% on reserved CPUs

**Note:** The SoftIRQ time metric is the **most critical indicator** of the AF_PACKET socket issue. When SoftIRQ time exceeds 50%, it indicates severe network stack overload that can cause:
- Network throughput degradation (measured via iperf3)
- High latency between pods
- Connection closures under load
- etcd performance issues (in Kubernetes clusters)

## Container Usage

When built into the Docker image:

```bash
# Run in container
run-reproducer reproducer 300

# Check statistics
socket-stats

# Cleanup
cleanup-sockets
```

## Dependencies

- `tcpdump` (for creating AF_PACKET sockets)
- Root or CAP_NET_RAW capability

## License

Apache 2.0

