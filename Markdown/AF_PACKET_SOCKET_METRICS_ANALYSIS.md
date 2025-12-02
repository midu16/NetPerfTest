# AF_PACKET Socket Performance Degradation - Prometheus Metrics Analysis

**Date:** Generated for replication analysis  
**Cluster:** OpenShift 4.18.27 (sno1 cluster)  
**Kubeconfig:** `./kubeconfig-sno1`  
**Replicator:** `NetPerfTest/replicator.yaml`

---

## Cluster Information

### Cluster Details
- **Cluster Name:** sno1 (OpenShift 4.18.27)
- **Prometheus Route:** `prometheus-k8s-openshift-monitoring.apps.sno1.5g-deployment.lab`
- **Kubeconfig:** `./kubeconfig-sno1`

### Nodes
| Node Name | IP Address | Role | CPU Count | NUMA Nodes |
|-----------|------------|------|-----------|------------|
| ocp-sno1 | 172.16.30.30 | control-plane,master,worker | 40 (0-39) | 1 (NUMA0: 0-39) |
| ocp-sno1p1 | 172.16.30.31 | worker | - | - |

**Note:** This cluster has a single NUMA node with 40 CPUs (0-39). Unlike the production case which had reserved CPUs (0,1,20,21,52,53,104,105,156,157), this cluster does not have a PerformanceProfile configured. Therefore, queries are adapted to monitor all CPUs or specific CPU ranges as needed.

---

## Problem Description

The AF_PACKET socket issue causes severe performance degradation in the kernel networking stack. When multiple AF_PACKET sockets are open on a node (e.g., from tcpdump instances), they cause reserved CPUs to spend almost 100% time in "si" (system interrupt), leading to:

- **Network throughput degradation** (measured via iperf)
- **High latency** between pods
- **Connection closures** under load
- **CPU overload** on reserved CPUs handling network interrupts

### Key Characteristics

- **Root Cause:** Large number of AF_PACKET sockets open on the node
- **Impact Scope:** Affects entire node, regardless of which network namespace opens the sockets
- **Symptom:** Reserved CPUs spending 90-100% time in system interrupt (si)
- **Kernel Functions:** `packet_rcv()` and `consume_skb()` show high CPU usage in perf top
- **Formula:** `NUMA1 CPU load = PPS × (fixed-cost + per-af-socket-cost × nb-of-af-sockets)`

---

## Replication Setup

The replicator deployment (`replicator.yaml`) creates:

1. **iperf-server**: Network performance test server
2. **iperf-client**: Network performance test client (measures throughput degradation)
3. **af-socket-replicator**: Creates AF_PACKET sockets progressively (200/500/700 sockets)
4. **perf-monitor**: Monitors kernel CPU usage with perf

### Deployment

**Deploy the replicator:**

```bash
oc --kubeconfig=./kubeconfig-sno1 apply -f NetPerfTest/replicator.yaml
```

**Verify deployment:**

```bash
# Check pod status
oc --kubeconfig=./kubeconfig-sno1 get pods -n af-packet-replicator -o wide

# Expected pods:
# - iperf-server (on ocp-sno1)
# - iperf-client (on ocp-sno1p1)
# - af-socket-replicator (on ocp-sno1)
# - perf-monitor (on ocp-sno1)
```

**Note:** The replicator.yaml is configured for nodes `ocp-sno1` and `ocp-sno1p1`, which match this cluster. If deploying to a different cluster, update the node selectors accordingly.

**Image Requirements:**
- `infra.5g-deployment.lab:8443/midu/network-tools:latest` (for iperf and tcpdump)
- `infra.5g-deployment.lab:8443/midu/perf-tools:latest` (for perf monitoring)

Ensure these images are available in your cluster's image registry or update the image references in `replicator.yaml`.

### Progressive Testing Phases

- **Phase 1 (200 sockets)**: Baseline degradation (70-90% throughput)
- **Phase 2 (500 sockets)**: Moderate degradation (50-70% throughput)
- **Phase 3 (700 sockets)**: Heavy degradation (30-50% throughput)

---

## Prometheus PromQL Metrics for AF_PACKET Socket Issue

This section provides comprehensive PromQL queries to monitor and diagnose the AF_PACKET socket performance degradation issue.

---

## 1. CPU Metrics - System Interrupt Time (Critical)

The primary symptom is CPUs spending excessive time in system interrupt mode. In production environments with reserved CPUs, the impact is most visible on those CPUs. In this cluster (no reserved CPUs), monitor all CPUs or focus on CPUs handling network interrupts.

### 1.1 CPU Usage by Mode (Per-CPU)

**System Interrupt Time (si) - Most Critical Metric**

**For clusters WITH reserved CPUs (production case):**
```promql
# System interrupt time percentage for reserved CPUs (production case)
100 - (
  avg(rate(node_cpu_seconds_total{mode="idle", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])) 
  * 100
) - (
  avg(rate(node_cpu_seconds_total{mode="user", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])) 
  * 100
) - (
  avg(rate(node_cpu_seconds_total{mode="system", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])) 
  * 100
)
```

**For this cluster (sno1 - all CPUs, single NUMA):**
```promql
# System interrupt time for ocp-sno1 node (all CPUs 0-39)
100 * rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*",
  cpu=~"[0-9]+"
}[5m])
```

**Top CPUs by System Interrupt Time:**
```promql
# Identify CPUs with highest softirq time (most affected by AF_PACKET)
topk(10,
  100 * rate(node_cpu_seconds_total{
    mode="softirq",
    instance=~".*ocp-sno1.*"
  }[5m])
)
```

**Direct System Interrupt Time**

```promql
# System interrupt time for all CPUs on ocp-sno1
100 * rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m])
```

**Interpretation:**
- **Normal**: < 10% si time
- **Warning**: 10-50% si time
- **Critical**: > 50% si time (indicates AF_PACKET socket issue)
- **Severe**: > 90% si time (complete network stack overload)

### 1.2 CPU Usage Breakdown by Mode

**User Mode CPU**

**Production (reserved CPUs):**
```promql
100 * rate(node_cpu_seconds_total{mode="user", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])
```

**This cluster (ocp-sno1):**
```promql
100 * rate(node_cpu_seconds_total{
  mode="user",
  instance=~".*ocp-sno1.*"
}[5m])
```

**System Mode CPU**

**Production (reserved CPUs):**
```promql
100 * rate(node_cpu_seconds_total{mode="system", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])
```

**This cluster (ocp-sno1):**
```promql
100 * rate(node_cpu_seconds_total{
  mode="system",
  instance=~".*ocp-sno1.*"
}[5m])
```

**Idle CPU**

**Production (reserved CPUs):**
```promql
100 * rate(node_cpu_seconds_total{mode="idle", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])
```

**This cluster (ocp-sno1):**
```promql
100 * rate(node_cpu_seconds_total{
  mode="idle",
  instance=~".*ocp-sno1.*"
}[5m])
```

**Hardware Interrupt Time (hi)**

**Production (reserved CPUs):**
```promql
100 * rate(node_cpu_seconds_total{mode="iowait", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])
```

**This cluster (ocp-sno1):**
```promql
100 * rate(node_cpu_seconds_total{
  mode="iowait",
  instance=~".*ocp-sno1.*"
}[5m])
```

### 1.3 SoftIRQ Metrics (Network-Related)

**Total SoftIRQ Rate**

```promql
# Total softirq processing rate
rate(node_cpu_seconds_total{mode="softirq", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])
```

**Network Receive SoftIRQ**

**Production (reserved CPUs):**
```promql
# Network receive softirq (most relevant for AF_PACKET)
rate(node_softirqs_total{softirq="NET_RX", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])
```

**This cluster (ocp-sno1):**
```promql
# Network receive softirq for ocp-sno1
rate(node_softirqs_total{
  softirq="NET_RX",
  instance=~".*ocp-sno1.*"
}[5m])
```

**Network Transmit SoftIRQ**

**Production (reserved CPUs):**
```promql
# Network transmit softirq
rate(node_softirqs_total{softirq="NET_TX", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])
```

**This cluster (ocp-sno1):**
```promql
# Network transmit softirq for ocp-sno1
rate(node_softirqs_total{
  softirq="NET_TX",
  instance=~".*ocp-sno1.*"
}[5m])
```

**Interpretation:**
- High NET_RX softirq rate indicates packet processing overhead
- AF_PACKET sockets increase NET_RX softirq processing time per packet

### 1.4 CPU Utilization Summary

**Total CPU Utilization (100% - Idle)**

```promql
# Total CPU utilization for reserved CPUs
100 - (
  avg(rate(node_cpu_seconds_total{mode="idle", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])) 
  * 100
)
```

**CPU Load by NUMA Node**

```promql
# NUMA0 reserved CPUs (0,1,20,21)
100 - avg(rate(node_cpu_seconds_total{mode="idle", cpu=~"0|1|20|21"}[5m])) * 100

# NUMA1 reserved CPUs (52,53,104,105,156,157)
100 - avg(rate(node_cpu_seconds_total{mode="idle", cpu=~"52|53|104|105|156|157"}[5m])) * 100
```

---

## 2. Network Throughput Metrics

Monitor network throughput degradation as AF_PACKET sockets increase.

### 2.1 Container Network Throughput

**Container Receive Bytes Rate**

```promql
# Receive throughput for iperf pods
rate(container_network_receive_bytes_total{
  namespace="af-packet-replicator",
  pod=~"iperf-server|iperf-client"
}[5m])
```

**Container Transmit Bytes Rate**

```promql
# Transmit throughput for iperf pods
rate(container_network_transmit_bytes_total{
  namespace="af-packet-replicator",
  pod=~"iperf-server|iperf-client"
}[5m])
```

**Throughput in Gbps**

```promql
# Receive throughput in Gbps
rate(container_network_receive_bytes_total{
  namespace="af-packet-replicator",
  pod=~"iperf-server|iperf-client"
}[5m]) * 8 / 1000000000

# Transmit throughput in Gbps
rate(container_network_transmit_bytes_total{
  namespace="af-packet-replicator",
  pod=~"iperf-server|iperf-client"
}[5m]) * 8 / 1000000000
```

### 2.2 Node-Level Network Throughput

**Node Receive Bytes Rate**

```promql
# Node-level receive throughput
rate(node_network_receive_bytes_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])
```

**Node Transmit Bytes Rate**

```promql
# Node-level transmit throughput
rate(node_network_transmit_bytes_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])
```

### 2.3 Network Packet Rate

**Container Packet Rate**

```promql
# Receive packets per second
rate(container_network_receive_packets_total{
  namespace="af-packet-replicator",
  pod=~"iperf-server|iperf-client"
}[5m])

# Transmit packets per second
rate(container_network_transmit_packets_total{
  namespace="af-packet-replicator",
  pod=~"iperf-server|iperf-client"
}[5m])
```

**Node Packet Rate**

```promql
# Node-level packet rate
rate(node_network_receive_packets_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])
```

---

## 3. Packet Drop Metrics

Monitor packet drops that may occur due to CPU overload.

### 3.1 Container-Level Packet Drops

**Container Receive Packet Drops**

```promql
# Receive packet drops for replicator pods
rate(container_network_receive_packets_dropped_total{
  namespace="af-packet-replicator"
}[5m])
```

**Container Transmit Packet Drops**

```promql
# Transmit packet drops
rate(container_network_transmit_packets_dropped_total{
  namespace="af-packet-replicator"
}[5m])
```

**Container Network Errors**

```promql
# Receive errors
rate(container_network_receive_errors_total{
  namespace="af-packet-replicator"
}[5m])

# Transmit errors
rate(container_network_transmit_errors_total{
  namespace="af-packet-replicator"
}[5m])
```

### 3.2 Node-Level Packet Drops

**Node Receive Packet Drops**

```promql
# Node-level receive drops
rate(node_network_receive_drop_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])
```

**Node Transmit Packet Drops**

```promql
# Node-level transmit drops
rate(node_network_transmit_drop_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])
```

**Node Network Errors**

```promql
# Receive errors
rate(node_network_receive_errs_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])

# Transmit errors
rate(node_network_transmit_errs_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])
```

---

## 4. Socket and Process Metrics

Monitor socket counts and process metrics related to AF_PACKET sockets.

### 4.1 Socket Count Metrics

**Container Socket Count**

```promql
# Total sockets for replicator pods
container_sockets{namespace="af-packet-replicator"}
```

**Process Socket Count (if available)**

```promql
# Socket count per process (may require custom exporter)
process_sockets{namespace="af-packet-replicator", pod="af-socket-replicator"}
```

**Note:** Direct AF_PACKET socket count may require custom metrics or sosreport analysis.

### 4.2 Process CPU Usage

**Process CPU Usage for tcpdump**

```promql
# CPU usage by process (if process exporter is available)
rate(process_cpu_seconds_total{
  namespace="af-packet-replicator",
  pod="af-socket-replicator",
  comm=~"tcpdump|.*"
}[5m])
```

**Process Count**

```promql
# Number of tcpdump processes
count(process_cpu_seconds_total{
  namespace="af-packet-replicator",
  pod="af-socket-replicator",
  comm="tcpdump"
})
```

### 4.3 Container Resource Usage

**Container CPU Usage**

```promql
# CPU usage for replicator pods
rate(container_cpu_usage_seconds_total{
  namespace="af-packet-replicator"
}[5m])
```

**Container Memory Usage**

```promql
# Memory usage
container_memory_working_set_bytes{
  namespace="af-packet-replicator"
}
```

---

## 5. OVN/Kubernetes Network Metrics

Monitor OVN and Kubernetes networking stack metrics.

### 5.1 OVN Packet Processing

**OVN Packet Processing Rate**

```promql
# OVN packet processing rate
rate(ovn_datapath_packets_processed_total[5m])
```

**OVN Packet Drops**

```promql
# OVN packet drops
rate(ovn_datapath_packets_dropped_total[5m])
```

**OVN Flow Updates**

```promql
# OVN flow update latency (if etcd is slow)
histogram_quantile(0.99,
  rate(ovn_southbound_db_transaction_duration_seconds_bucket[5m])
)
```

### 5.2 OVN Controller Metrics

**OVN Controller CPU**

```promql
# OVN controller CPU usage
rate(process_cpu_seconds_total{job="ovn-controller"}[5m])
```

**OVN Controller Memory**

```promql
# OVN controller memory
process_resident_memory_bytes{job="ovn-controller"} / 1024 / 1024 / 1024
```

---

## 6. etcd Metrics - Network Failure Detection

etcd is critical for Kubernetes cluster coordination. Network failures affecting etcd can cause cluster-wide issues. When AF_PACKET sockets cause network stack degradation, etcd may experience:
- Increased latency for peer communication
- Connection timeouts and failures
- Leader election issues
- Heartbeat failures
- Request timeouts

### 6.1 etcd Network Latency Metrics

**Peer Round-Trip Time (RTT) - Critical for Network Health**

**P50 RTT (Median)**
```promql
histogram_quantile(0.50,
  rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
)
```

**P95 RTT**
```promql
histogram_quantile(0.95,
  rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
)
```

**P99 RTT (Most Critical)**
```promql
histogram_quantile(0.99,
  rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
)
```

**Average RTT**
```promql
rate(etcd_network_peer_round_trip_time_seconds_sum[5m]) 
/ 
rate(etcd_network_peer_round_trip_time_seconds_count[5m])
```

**Interpretation:**
- **Normal**: < 10ms
- **Warning**: 10-50ms
- **Critical**: > 50ms (indicates network degradation)
- **Severe**: > 100ms (network failure likely)

**RTT by Peer**
```promql
# RTT per peer (identify problematic peer connections)
histogram_quantile(0.99,
  rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
) by (To)
```

### 6.2 etcd Request Latency

**Write Latency (WAL fsync)**

**P99 Write Latency**
```promql
histogram_quantile(0.99,
  rate(etcd_disk_wal_fsync_duration_seconds_bucket[5m])
)
```

**P95 Write Latency**
```promql
histogram_quantile(0.95,
  rate(etcd_disk_wal_fsync_duration_seconds_bucket[5m])
)
```

**Write Latency by Instance**
```promql
histogram_quantile(0.99,
  rate(etcd_disk_wal_fsync_duration_seconds_bucket[5m])
) by (instance)
```

**Read Latency**

**P99 Read Latency**
```promql
histogram_quantile(0.99,
  rate(etcd_server_requests_duration_seconds_bucket{method="Range"}[5m])
)
```

**All Request Types Latency**
```promql
histogram_quantile(0.99,
  rate(etcd_server_requests_duration_seconds_bucket[5m])
) by (method)
```

**Interpretation:**
- **Normal**: < 10ms
- **Warning**: 10-100ms
- **Critical**: > 100ms (network/disk issues)

### 6.3 etcd Request Rate and Throughput

**Request Rate by Method**
```promql
# Total requests per second by method
rate(etcd_server_requests_total[5m]) by (method)
```

**Write Request Rate**
```promql
rate(etcd_server_requests_total{method="Put"}[5m])
```

**Read Request Rate**
```promql
rate(etcd_server_requests_total{method="Range"}[5m])
```

**Failed Request Rate - Critical Indicator**
```promql
# Failed requests indicate network/connectivity issues
rate(etcd_server_requests_total{result="failure"}[5m]) by (method, result)
```

**Request Success Rate**
```promql
# Success rate percentage
(
  rate(etcd_server_requests_total{result="success"}[5m])
  /
  rate(etcd_server_requests_total[5m])
) * 100
```

**Request Timeout Rate**
```promql
# Timeout requests (network issues)
rate(etcd_server_requests_total{result="timeout"}[5m])
```

### 6.4 etcd Peer Connectivity and Health

**Peer Connection Status**
```promql
# Active peer connections (should be 2 for 3-node cluster)
etcd_network_peer_connected{Type="stream"}
```

**Peer Disconnection Count**
```promql
# Number of peer disconnections (indicates network issues)
increase(etcd_network_peer_disconnected_total[5m])
```

**Peer Connection Errors**
```promql
# Connection errors to peers
rate(etcd_network_peer_connection_errors_total[5m])
```

**Heartbeat Failures - Critical for Network Detection**
```promql
# Heartbeat failures indicate network connectivity issues
rate(etcd_network_peer_round_trip_time_seconds_count{type="heartbeat"}[5m])
```

**Heartbeat Timeout Rate**
```promql
# Heartbeat timeouts (network partition indicator)
increase(etcd_network_peer_round_trip_time_seconds_count{type="heartbeat", le="+Inf"}[5m])
```

**Peer Round-Trip Failures**
```promql
# Failed round-trips to peers
rate(etcd_network_peer_round_trip_time_seconds_count{type="heartbeat"}[5m]) 
- 
rate(etcd_network_peer_round_trip_time_seconds_sum{type="heartbeat"}[5m])
```

### 6.5 etcd Leader Election and Consensus

**Leader Changes - Network Partition Indicator**
```promql
# Leader changes indicate network issues causing quorum loss
increase(etcd_server_leader_changes_seen_total[5m])
```

**Leader Election Duration**
```promql
# Time to elect new leader (longer = network issues)
histogram_quantile(0.99,
  rate(etcd_server_leader_election_duration_seconds_bucket[5m])
)
```

**Current Leader**
```promql
# Identify current leader
etcd_server_is_leader
```

**Follower Lag - Network Latency Indicator**
```promql
# Follower lag behind leader (indicates network latency)
etcd_server_leader_changes_seen_total - etcd_server_leader_changes_seen_total{instance=~".*etcd.*"}
```

**Proposal Failures**
```promql
# Failed proposals (network/consensus issues)
rate(etcd_server_proposals_failed_total[5m])
```

**Proposal Pending Duration**
```promql
# Time proposals spend pending (network latency)
histogram_quantile(0.99,
  rate(etcd_server_proposals_pending_duration_seconds_bucket[5m])
)
```

**Consensus Commit Rate**
```promql
# Rate of consensus commits
rate(etcd_server_proposals_committed_total[5m])
```

### 6.6 etcd Client Connection Metrics

**Client Connection Count**
```promql
# Number of active client connections
etcd_server_client_requests_total
```

**Client Request Failures**
```promql
# Failed client requests (network issues)
rate(etcd_server_client_requests_total{result="failure"}[5m])
```

**Client Request Timeouts**
```promql
# Client request timeouts
rate(etcd_server_client_requests_total{result="timeout"}[5m])
```

**Client Connection Errors**
```promql
# Client connection errors
rate(etcd_server_client_requests_total{result="error"}[5m])
```

### 6.7 etcd Network Partition Detection

**Quorum Health**
```promql
# etcd cluster health (1 = healthy, 0 = unhealthy)
etcd_server_health_success
```

**Cluster Size**
```promql
# Number of etcd members
count(etcd_server_health_success == 1)
```

**Quorum Loss Detection**
```promql
# Detect when quorum is lost (network partition)
count(etcd_server_health_success == 1) < 2
```

**Member Unreachable**
```promql
# Members that are unreachable
up{job="etcd"} == 0
```

**Network Partition Indicator Query**
```promql
# Combined query to detect network partition
(
  increase(etcd_server_leader_changes_seen_total[5m]) > 0
  OR
  rate(etcd_network_peer_disconnected_total[5m]) > 0
  OR
  histogram_quantile(0.99, rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])) > 0.1
)
AND
(
  count(etcd_server_health_success == 1) < 3
)
```

### 6.8 etcd Network Error Rates

**Total Network Errors**
```promql
# Sum of all network-related errors
sum(rate(etcd_network_peer_connection_errors_total[5m]))
```

**Network Error Rate by Type**
```promql
# Network errors by type
rate(etcd_network_peer_connection_errors_total[5m]) by (error)
```

**Send/Receive Failures**
```promql
# Network send/receive failures
rate(etcd_network_sent_bytes_total[5m]) - rate(etcd_network_received_bytes_total[5m])
```

**Bandwidth Usage**
```promql
# Network bandwidth usage (may be affected by AF_PACKET sockets)
rate(etcd_network_client_grpc_sent_bytes_total[5m])
rate(etcd_network_client_grpc_received_bytes_total[5m])
```

### 6.9 etcd Performance Degradation Indicators

**Request Duration Increase**
```promql
# Detect increase in request duration (network degradation)
(
  histogram_quantile(0.99, rate(etcd_server_requests_duration_seconds_bucket[5m]))
  /
  histogram_quantile(0.99, rate(etcd_server_requests_duration_seconds_bucket[15m]))
) > 1.5
```

**RTT Increase Detection**
```promql
# Detect RTT increase (network stack degradation)
(
  histogram_quantile(0.99, rate(etcd_network_peer_round_trip_time_seconds_bucket[5m]))
  /
  histogram_quantile(0.99, rate(etcd_network_peer_round_trip_time_seconds_bucket[15m]))
) > 2.0
```

**Throughput Degradation**
```promql
# Detect throughput degradation
(
  rate(etcd_server_requests_total[5m])
  /
  rate(etcd_server_requests_total[15m])
) < 0.7
```

### 6.10 etcd Alerting Queries for Network Failures

**Critical: High etcd Latency**
```promql
# Alert when etcd RTT exceeds threshold
histogram_quantile(0.99,
  rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
) > 0.1
```

**Critical: Peer Disconnections**
```promql
# Alert on peer disconnections
increase(etcd_network_peer_disconnected_total[5m]) > 0
```

**Critical: Leader Changes**
```promql
# Alert on frequent leader changes
increase(etcd_server_leader_changes_seen_total[5m]) > 2
```

**Warning: High Request Failure Rate**
```promql
# Alert when request failure rate is high
(
  rate(etcd_server_requests_total{result="failure"}[5m])
  /
  rate(etcd_server_requests_total[5m])
) > 0.05
```

**Warning: Request Timeouts**
```promql
# Alert on request timeouts
rate(etcd_server_requests_total{result="timeout"}[5m]) > 0.1
```

**Critical: Quorum Loss**
```promql
# Alert when quorum is lost
count(etcd_server_health_success == 1) < 2
```

### 6.11 etcd Metrics Correlation with AF_PACKET Socket Issue

**Network Degradation Correlation Query**
```promql
# Correlate etcd latency with system interrupt time
(
  histogram_quantile(0.99, rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])) > 0.05
)
AND
(
  100 * rate(node_cpu_seconds_total{mode="softirq", instance=~".*ocp-sno1.*"}[5m]) > 20
)
```

**Combined Network Failure Detection**
```promql
# Detect network failures affecting both etcd and node networking
(
  # etcd experiencing high latency
  histogram_quantile(0.99, rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])) > 0.1
  OR
  # Peer disconnections
  increase(etcd_network_peer_disconnected_total[5m]) > 0
  OR
  # Request failures
  rate(etcd_server_requests_total{result="failure"}[5m]) > 1
)
AND
(
  # High system interrupt time (AF_PACKET socket indicator)
  100 * rate(node_cpu_seconds_total{mode="softirq", instance=~".*ocp-sno1.*"}[5m]) > 30
)
```

**Interpretation:**
- When AF_PACKET sockets cause network stack degradation, etcd will show:
  - Increased peer RTT (> 50ms)
  - More frequent peer disconnections
  - Increased request failures and timeouts
  - Leader election issues
  - Heartbeat failures
- These metrics provide early warning of network issues affecting cluster coordination

---

## 7. Comprehensive Monitoring Dashboard Queries

### 7.1 AF_PACKET Socket Issue Detection Query

**Combined CPU and Network Degradation**

```promql
# Detect AF_PACKET socket issue: High si time + throughput degradation
(
  100 * rate(node_cpu_seconds_total{mode="softirq", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])
) > 50
AND
(
  rate(container_network_receive_bytes_total{namespace="af-packet-replicator", pod="iperf-client"}[5m])
  < 
  rate(container_network_receive_bytes_total{namespace="af-packet-replicator", pod="iperf-client"}[15m]) * 0.7
)
```

### 7.2 Performance Degradation Over Time

**Throughput Degradation Percentage**

```promql
# Calculate throughput degradation
(
  1 - (
    rate(container_network_receive_bytes_total{namespace="af-packet-replicator", pod="iperf-client"}[5m])
    /
    rate(container_network_receive_bytes_total{namespace="af-packet-replicator", pod="iperf-client"}[15m])
  )
) * 100
```

### 7.3 CPU Load vs Socket Count Correlation

**CPU Load Increase Rate**

```promql
# CPU load increase rate (indicates socket impact)
rate(
  (100 - avg(rate(node_cpu_seconds_total{mode="idle", cpu=~"0|1|20|21|52|53|104|105|156|157"}[5m])) * 100)
  [5m]
)
```

---

## 8. Alerting Rules

### 8.1 Critical: System Interrupt Overload

```yaml
groups:
  - name: af_packet_socket_issue
    rules:
      - alert: HighSystemInterruptTime
        expr: |
          100 * rate(node_cpu_seconds_total{
            mode="softirq",
            cpu=~"0|1|20|21|52|53|104|105|156|157"
          }[5m]) > 50
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Reserved CPUs spending >50% time in system interrupt"
          description: "AF_PACKET socket issue detected. CPU {{ $labels.cpu }} has {{ $value }}% si time."
      
      - alert: NetworkThroughputDegradation
        expr: |
          (
            rate(container_network_receive_bytes_total{
              namespace="af-packet-replicator",
              pod="iperf-client"
            }[5m])
            /
            rate(container_network_receive_bytes_total{
              namespace="af-packet-replicator",
              pod="iperf-client"
            }[15m])
          ) < 0.7
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Network throughput degraded by >30%"
          description: "Throughput is {{ $value | humanizePercentage }} of baseline."
      
      - alert: EtcdHighLatency
        expr: |
          histogram_quantile(0.99,
            rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
          ) > 0.1
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "etcd peer RTT exceeds 100ms"
          description: "etcd peer round-trip time is {{ $value }}s (threshold: 0.1s). Network degradation detected."
      
      - alert: EtcdPeerDisconnections
        expr: |
          increase(etcd_network_peer_disconnected_total[5m]) > 0
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "etcd peer disconnections detected"
          description: "{{ $value }} peer disconnection(s) in the last 5 minutes. Possible network failure."
      
      - alert: EtcdLeaderChanges
        expr: |
          increase(etcd_server_leader_changes_seen_total[5m]) > 2
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Frequent etcd leader changes"
          description: "{{ $value }} leader change(s) in 5 minutes. Network partition or instability likely."
      
      - alert: EtcdRequestFailures
        expr: |
          rate(etcd_server_requests_total{result="failure"}[5m]) > 1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High etcd request failure rate"
          description: "etcd request failure rate is {{ $value }} req/s. Network or connectivity issues."
      
      - alert: EtcdQuorumLoss
        expr: |
          count(etcd_server_health_success == 1) < 2
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "etcd quorum lost"
          description: "Only {{ $value }} healthy etcd member(s). Quorum lost - cluster coordination at risk."
      
      - alert: EtcdNetworkPartition
        expr: |
          (
            increase(etcd_server_leader_changes_seen_total[5m]) > 0
            OR
            rate(etcd_network_peer_disconnected_total[5m]) > 0
            OR
            histogram_quantile(0.99, rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])) > 0.1
          )
          AND
          (
            count(etcd_server_health_success == 1) < 3
          )
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "etcd network partition detected"
          description: "Network partition affecting etcd cluster. Leader changes: {{ $value }}, Healthy members: {{ $value }}"
```

---

## 9. Expected Metrics Behavior During Replication

### Phase 1: Baseline (200 AF_PACKET sockets)

- **System Interrupt Time**: 5-10% on reserved CPUs
- **Throughput**: 70-90% of baseline
- **Packet Drops**: 0 or minimal
- **CPU Utilization**: 50-60% on reserved CPUs

### Phase 2: Moderate Degradation (500 AF_PACKET sockets)

- **System Interrupt Time**: 15-25% on reserved CPUs
- **Throughput**: 50-70% of baseline
- **Packet Drops**: May start appearing
- **CPU Utilization**: 70-80% on reserved CPUs

### Phase 3: Heavy Degradation (700 AF_PACKET sockets)

- **System Interrupt Time**: 30-40% on reserved CPUs
- **Throughput**: 30-50% of baseline
- **Packet Drops**: Significant drops observed
- **CPU Utilization**: 90-100% on reserved CPUs

### etcd Metrics During AF_PACKET Socket Issue

When network stack degradation occurs due to AF_PACKET sockets, etcd metrics will show:

**Phase 1 (200 sockets):**
- **Peer RTT**: 10-30ms (normal: < 10ms)
- **Request Latency**: 10-50ms (normal: < 10ms)
- **Request Failures**: 0-0.1 req/s
- **Peer Disconnections**: 0
- **Leader Changes**: 0

**Phase 2 (500 sockets):**
- **Peer RTT**: 30-100ms (warning threshold exceeded)
- **Request Latency**: 50-200ms
- **Request Failures**: 0.1-1 req/s
- **Peer Disconnections**: Occasional (1-2 per 5 minutes)
- **Leader Changes**: 0-1 per 5 minutes
- **Heartbeat Failures**: May start appearing

**Phase 3 (700 sockets):**
- **Peer RTT**: > 100ms (critical threshold exceeded)
- **Request Latency**: > 200ms
- **Request Failures**: > 1 req/s
- **Peer Disconnections**: Frequent (> 2 per 5 minutes)
- **Leader Changes**: 2+ per 5 minutes (indicates network instability)
- **Quorum Loss**: Possible if network degradation is severe
- **Heartbeat Failures**: Frequent

**Note:** etcd metrics provide early warning of network issues. If etcd shows degradation before application-level metrics, it indicates cluster-wide network problems affecting coordination.

---

## 10. Troubleshooting Queries

### 10.1 Identify Affected Nodes

```promql
# Nodes with high system interrupt time
topk(10,
  100 * rate(node_cpu_seconds_total{mode="softirq"}[5m])
)
```

### 10.2 Identify Affected Pods

```promql
# Pods with network performance issues
rate(container_network_receive_packets_dropped_total[5m]) > 0
```

### 10.3 Network Latency Indicators

```promql
# High network latency (if available)
histogram_quantile(0.99,
  rate(container_network_latency_seconds_bucket[5m])
)
```

### 10.4 etcd Network Health Check

```promql
# Overall etcd network health
histogram_quantile(0.99,
  rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
) > 0.1
```

**Identify problematic etcd peers:**
```promql
# RTT by peer (identify which peer connection is slow)
histogram_quantile(0.99,
  rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
) by (To)
```

**Check etcd cluster health:**
```promql
# Number of healthy etcd members
count(etcd_server_health_success == 1)

# Unhealthy members
up{job="etcd"} == 0
```

**Detect network-related etcd issues:**
```promql
# Combined query: etcd issues + high system interrupt time
(
  histogram_quantile(0.99, rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])) > 0.05
  OR
  increase(etcd_network_peer_disconnected_total[5m]) > 0
  OR
  rate(etcd_server_requests_total{result="failure"}[5m]) > 0.5
)
AND
(
  100 * rate(node_cpu_seconds_total{mode="softirq", instance=~".*ocp-sno1.*"}[5m]) > 20
)
```

---

## 11. References

- **Case**: [Airtel Case 04076665](https://access.redhat.com/support/cases/#/case/04076665)
- **RHEL Bug**: [RHEL-83393](https://issues.redhat.com/browse/RHEL-83393)
- **Reproducer**: [GitHub - af_packet_issue](https://github.com/mrVectorz/randoms/tree/master/af_packet_issue)
- **Replicator Guide**: `NetPerfTest/Markdown/REPLICATOR_GUIDE.md`

---

## 12. Notes

1. **AF_PACKET Socket Count**: Direct socket count metrics may not be available in standard Prometheus. Use sosreport analysis or custom exporters.

2. **Perf Metrics**: Kernel-level perf metrics (`packet_rcv()`, `consume_skb()`) require perf tool access, not available via Prometheus.

3. **Network Namespace**: AF_PACKET sockets in any network namespace affect the entire node.

4. **NUMA Awareness**: Monitor both NUMA0 and NUMA1 reserved CPUs separately.

5. **Baseline Measurement**: Establish baseline metrics before starting replication.

---

## 13. Quick Reference: Key Metrics

### Production Cluster (with reserved CPUs)

#### System Interrupt Time (Critical Threshold: > 50%)
```promql
# System interrupt time on reserved CPUs
100 * rate(node_cpu_seconds_total{
  mode="softirq",
  cpu=~"0|1|20|21|52|53|104|105|156|157"
}[5m])
```

#### Network Throughput (Critical Threshold: < 70% baseline)
```promql
# Network receive throughput for iperf client
rate(container_network_receive_bytes_total{
  namespace="af-packet-replicator",
  pod="iperf-client"
}[5m])
```

#### Packet Drops (Critical Threshold: > 0)
```promql
# Packet drop rate in replicator namespace
rate(container_network_receive_packets_dropped_total{
  namespace="af-packet-replicator"
}[5m])
```

#### CPU Utilization (Critical Threshold: > 80%)
```promql
# Overall CPU utilization on reserved CPUs
100 - avg(rate(node_cpu_seconds_total{
  mode="idle",
  cpu=~"0|1|20|21|52|53|104|105|156|157"
}[5m])) * 100
```

#### NET_RX SoftIRQ Rate (Monitor for High Rate)
```promql
# Network receive software interrupt rate on reserved CPUs
rate(node_softirqs_total{
  softirq="NET_RX",
  cpu=~"0|1|20|21|52|53|104|105|156|157"
}[5m])
```

### This Cluster (sno1 - ocp-sno1 node)

#### System Interrupt Time (Critical Threshold: > 50%)
```promql
# System interrupt time on ocp-sno1 node
100 * rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m])
```

#### Network Throughput (Critical Threshold: < 70% baseline)
```promql
# Network receive throughput for iperf client
rate(container_network_receive_bytes_total{
  namespace="af-packet-replicator",
  pod="iperf-client"
}[5m])
```

#### Packet Drops (Critical Threshold: > 0)
```promql
# Packet drop rate in replicator namespace
rate(container_network_receive_packets_dropped_total{
  namespace="af-packet-replicator"
}[5m])
```

#### CPU Utilization (Critical Threshold: > 80%)
```promql
# Overall CPU utilization on ocp-sno1 node
100 - avg(rate(node_cpu_seconds_total{
  mode="idle",
  instance=~".*ocp-sno1.*"
}[5m])) * 100
```

#### NET_RX SoftIRQ Rate (Monitor for High Rate)
```promql
# Network receive software interrupt rate on ocp-sno1
rate(node_softirqs_total{
  softirq="NET_RX",
  instance=~".*ocp-sno1.*"
}[5m])
```

#### Top CPUs by System Interrupt Time (Identify Affected CPUs)
```promql
# Top 10 CPUs with highest system interrupt time on ocp-sno1
topk(10, 100 * rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m]))
```

### etcd Network Failure Detection Metrics

#### etcd Peer RTT P99 (Critical Threshold: > 50ms)
```promql
# 99th percentile of etcd peer round-trip time
histogram_quantile(0.99,
  rate(etcd_network_peer_round_trip_time_seconds_bucket[5m])
)
```

#### Peer Disconnections (Critical Threshold: > 0)
```promql
# Increase in peer disconnections over 5 minutes
increase(etcd_network_peer_disconnected_total[5m])
```

#### Leader Changes (Critical Threshold: > 2 in 5m)
```promql
# Number of leader changes in last 5 minutes
increase(etcd_server_leader_changes_seen_total[5m])
```

#### Request Failures (Critical Threshold: > 1 req/s)
```promql
# Rate of failed etcd requests
rate(etcd_server_requests_total{
  result="failure"
}[5m])
```

#### Request Timeouts (Critical Threshold: > 0.1 req/s)
```promql
# Rate of timed-out etcd requests
rate(etcd_server_requests_total{
  result="timeout"
}[5m])
```

#### Quorum Loss (Alert if True)
```promql
# Check if etcd has lost quorum (less than 2 healthy members)
count(etcd_server_health_success == 1) < 2
```

#### Heartbeat Failures (Monitor for Increases)
```promql
# Rate of heartbeat round-trip time measurements
rate(etcd_network_peer_round_trip_time_seconds_count{
  type="heartbeat"
}[5m])
```

#### Network Partition Detection
See Section 6.7 for comprehensive network partition detection using multiple indicators.

---

---

## 14. Step-by-Step Issue Replication and Verification

### 14.1 Pre-Replication Baseline

**1. Establish baseline metrics (before starting replication):**

```bash
# Access Prometheus
oc --kubeconfig=./kubeconfig-sno1 port-forward -n openshift-monitoring svc/prometheus-k8s 9090:9090
```

**Baseline queries to run in Prometheus:**

```promql
# Baseline CPU utilization
100 - avg(rate(node_cpu_seconds_total{
  mode="idle",
  instance=~".*ocp-sno1.*"
}[5m])) * 100

# Baseline network throughput (wait for iperf to start)
rate(container_network_receive_bytes_total{
  namespace="af-packet-replicator",
  pod="iperf-client"
}[5m])

# Baseline system interrupt time
100 * avg(rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m]))
```

**2. Verify pods are running:**

```bash
oc --kubeconfig=./kubeconfig-sno1 get pods -n af-packet-replicator
oc --kubeconfig=./kubeconfig-sno1 logs -n af-packet-replicator iperf-client --tail=20
```

### 14.2 Phase 1: Create 200 AF_PACKET Sockets

**1. Start Phase 1:**

```bash
oc --kubeconfig=./kubeconfig-sno1 exec -it -n af-packet-replicator af-socket-replicator -- phase1
```

**2. Monitor metrics (run in Prometheus):**

```promql
# System interrupt time increase
100 * avg(rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m]))

# Network throughput degradation
rate(container_network_receive_bytes_total{
  namespace="af-packet-replicator",
  pod="iperf-client"
}[5m])

# CPU utilization
100 - avg(rate(node_cpu_seconds_total{
  mode="idle",
  instance=~".*ocp-sno1.*"
}[5m])) * 100
```

**3. Check socket count:**

```bash
oc --kubeconfig=./kubeconfig-sno1 exec -n af-packet-replicator af-socket-replicator -- status
```

**Expected Results:**
- System interrupt time: 5-10% increase
- Throughput: 70-90% of baseline
- Active tcpdump processes: ~200

### 14.3 Phase 2: Create 500 AF_PACKET Sockets

**1. Start Phase 2:**

```bash
oc --kubeconfig=./kubeconfig-sno1 exec -it -n af-packet-replicator af-socket-replicator -- phase2
```

**2. Monitor metrics:**

```promql
# Compare to baseline
(100 * avg(rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m]))) / 
(100 * avg(rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[15m]))) * 100
```

**Expected Results:**
- System interrupt time: 15-25% increase
- Throughput: 50-70% of baseline
- Active tcpdump processes: ~500

### 14.4 Phase 3: Create 700 AF_PACKET Sockets

**1. Start Phase 3:**

```bash
oc --kubeconfig=./kubeconfig-sno1 exec -it -n af-packet-replicator af-socket-replicator -- phase3
```

**2. Monitor metrics:**

```promql
# Severe degradation indicators
100 * avg(rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m])) > 30

# Throughput degradation percentage
(1 - (
  rate(container_network_receive_bytes_total{
    namespace="af-packet-replicator",
    pod="iperf-client"
  }[5m])
  /
  rate(container_network_receive_bytes_total{
    namespace="af-packet-replicator",
    pod="iperf-client"
  }[15m])
)) * 100
```

**Expected Results:**
- System interrupt time: 30-40% increase
- Throughput: 30-50% of baseline
- Active tcpdump processes: ~700
- Packet drops may start appearing

### 14.5 Verification Checklist

- [ ] Baseline metrics captured before replication
- [ ] Phase 1 (200 sockets) shows measurable degradation
- [ ] Phase 2 (500 sockets) shows increased degradation
- [ ] Phase 3 (700 sockets) shows severe degradation
- [ ] System interrupt time correlates with socket count
- [ ] Network throughput degrades as sockets increase
- [ ] CPU utilization increases on ocp-sno1 node
- [ ] NET_RX softirq rate increases with socket count

### 14.6 Cleanup

**Stop all tcpdump processes:**

```bash
oc --kubeconfig=./kubeconfig-sno1 exec -n af-packet-replicator af-socket-replicator -- cleanup
```

**Delete the namespace:**

```bash
oc --kubeconfig=./kubeconfig-sno1 delete namespace af-packet-replicator
```

---

## 15. Cluster-Specific PromQL Queries (sno1)

### 15.1 Node-Specific Queries

**All metrics for ocp-sno1 node:**
```promql
# Add instance filter to any query
{instance=~".*ocp-sno1.*"}
```

**CPU metrics for ocp-sno1:**
```promql
# System interrupt time
100 * rate(node_cpu_seconds_total{
  mode="softirq",
  instance=~".*ocp-sno1.*"
}[5m])

# Total CPU utilization
100 - avg(rate(node_cpu_seconds_total{
  mode="idle",
  instance=~".*ocp-sno1.*"
}[5m])) * 100
```

**Network metrics for ocp-sno1:**
```promql
# Node network throughput
rate(node_network_receive_bytes_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])

# Node packet drops
rate(node_network_receive_drop_total{
  instance=~".*ocp-sno1.*",
  device!="lo"
}[5m])
```

### 15.2 Pod-Specific Queries

**Replicator namespace pods:**
```promql
# All pods in replicator namespace
{namespace="af-packet-replicator"}

# Specific pod
{pod="iperf-client", namespace="af-packet-replicator"}
```
