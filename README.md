# NetPerfTest - AF_PACKET Socket Analysis

## The Problem

When multiple AF_PACKET sockets are opened on a Kubernetes/OpenShift node (e.g., from tcpdump instances), they cause reserved CPUs to spend **90-100% time in system interrupt (si)**, leading to:

- **Network throughput degradation** (30-70% reduction)
- **High latency** between pods
- **Connection closures** under load
- **CPU overload** on reserved CPUs handling network interrupts

**Root Cause:** Kernel functions `packet_rcv()` and `consume_skb()` create excessive overhead when copying packets to multiple AF_PACKET socket buffers.

**Formula:** `CPU load = PPS × (fixed-cost + per-af-socket-cost × nb-of-af-sockets)`

---

## Quick Start

### Prerequisites

- OpenShift/Kubernetes cluster with Prometheus monitoring
- `oc` or `kubectl` CLI tool
- Kubeconfig access to target cluster

### Deploy the Replicator

```bash
# Deploy AF_PACKET socket replicator
oc --kubeconfig=./kubeconfig-sno1 apply -f replicator.yaml

# Verify deployment
oc get pods -n af-packet-replicator -o wide

# Expected pods:
# - iperf-server (network performance server)
# - iperf-client (network performance client)
# - af-socket-replicator (creates AF_PACKET sockets)
# - perf-monitor (monitors CPU si% metrics)
```

### Monitor the Issue

Access Prometheus and run the key queries:

```bash
# Port-forward to Prometheus
oc port-forward -n openshift-monitoring svc/prometheus-k8s 9090:9090
```

Open http://localhost:9090 and run:

```promql
# System interrupt time on target node
100 * rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m])

# Network throughput
rate(container_network_receive_bytes_total{
  namespace="af-packet-replicator",
  pod="iperf-client"
}[5m])
```

**Expected Results:**
- CPU si% starts at ~0.5-5%
- After deploying replicator with 3000 sockets + high PPS traffic: **90-100% si%**
- Network throughput degradation: **30-70% reduction**


---

## Key Components

### 1. AF_PACKET Socket Replicator (`replicator.yaml`)

The main deployment that replicates the issue:

- **iperf-server**: Network performance test server (baseline throughput measurement)
- **iperf-client**: Network performance test client (measures degradation)
- **af-socket-replicator**: Creates 3000 AF_PACKET sockets on loopback interface with high PPS traffic
- **perf-monitor**: Monitors CPU si% on reserved CPUs

**Configuration Highlights:**
```yaml
# 3000 AF_PACKET sockets on loopback interface
# 250 concurrent TCP streams (50 clients × 5 streams)
# 64-byte packets for maximum PPS
# Loopback traffic generator for highest local packet rate
```

### 2. Metrics Collector (`af_packet_reproducer/`)

Go-based tool that:
- Creates configurable number of AF_PACKET sockets
- Generates real network traffic
- Monitors CPU softirq time
- Generates PNG visualizations
- Tracks active socket count

**Usage:**
```bash
cd af_packet_reproducer
sudo ./af_packet_reproducer \
  -sockets=1000 \
  -duration=60 \
  -dir=./metrics \
  -interface=lo \
  -verbose
```

---

## Prometheus Metrics Guide

### Critical Metrics for AF_PACKET Issue Detection

#### 1. System Interrupt Time (si%)
```promql
# CPU si% on target node (Critical: > 50%)
100 * rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m])
```

#### 2. Network Throughput
```promql
# Network throughput (Critical: < 70% baseline)
rate(container_network_receive_bytes_total{
  namespace="af-packet-replicator",
  pod="iperf-client"
}[5m])
```

#### 3. Packet Drops
```promql
# Packet drop rate (Critical: > 0)
rate(container_network_receive_packets_dropped_total{
  namespace="af-packet-replicator"
}[5m])
```

#### 4. NET_RX SoftIRQ Rate
```promql
# Software interrupt rate (Monitor for increases)
rate(node_softirqs_total{
  softirq="NET_RX",
  instance=~".*ocp-sno1.*"
}[5m])
```

#### 5. Top CPUs by System Interrupt
```promql
# Identify most affected CPUs
topk(10, 100 * rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m]))
```

**📖 For complete Prometheus queries and analysis:** See [`Markdown/AF_PACKET_SOCKET_METRICS_ANALYSIS.md`](Markdown/AF_PACKET_SOCKET_METRICS_ANALYSIS.md)

---

## Replication Workflow

### Step 1: Pre-Deployment Baseline

```bash
# Establish baseline metrics before replication
oc port-forward -n openshift-monitoring svc/prometheus-k8s 9090:9090
```

Run baseline queries in Prometheus (see Metrics Guide above).

### Step 2: Deploy Replicator

```bash
oc apply -f replicator.yaml
```

### Step 3: Progressive Testing

The replicator automatically creates 3000 AF_PACKET sockets and generates maximum PPS:

- **Phase 1 (0-60s)**: Socket creation in progress
- **Phase 2 (60-120s)**: Full load with 3000 sockets + 250 TCP streams
- **Phase 3 (120s+)**: Sustained overload - CPU si% reaches 90-100%

### Step 4: Monitor Metrics

Watch Prometheus queries in real-time:
- CPU si% increases from ~5% → 90-100%
- Network throughput degrades by 30-70%
- System becomes slow/unresponsive

### Step 5: Collect Evidence

```bash
# If cluster is responsive
cd deployments
./collect_evidence.sh

# Review results
cd ../REPLICATION_PROOF
cat EVIDENCE_SUMMARY.txt
grep 'CPU0:' perf-monitor.log | awk -F'CPU0:|%si' '{print $2}' | awk -F',' '{print $1}' | sort -rn | head -20
```

### Step 6: Cleanup

```bash
# Delete the replicator namespace
oc delete namespace af-packet-replicator
```

---

## Success Criteria

| Metric | Baseline | With AF_PACKET Issue | Status |
|--------|----------|---------------------|--------|
| CPU si% | 0.5-5% | **90-100%** | ✅ Replicated |
| Network Throughput | 100% | **30-70%** | ✅ Replicated |
| Packet Drops | 0 | **> 0** | ✅ Observed |
| System Responsiveness | Normal | **Degraded/Unresponsive** | ✅ Confirmed |