# AF_PACKET Socket Issue Reproduction - Evaluation Report

**Date:** December 1, 2025  
**Test Configuration:** 200 sockets, 60 seconds duration  
**Reference:** Airtel Case 04076665, RHEL-83393

---

## Test Execution Summary

### Configuration
- **Sockets Created:** 200 AF_PACKET sockets
- **Duration:** 60 seconds
- **Metrics Collection Interval:** 5 seconds
- **Capture Directory:** `/tmp/pcaps`
- **Metrics Directory:** `/tmp/metrics`

### Execution Results

**Socket Creation:**
- Successfully created 200 AF_PACKET sockets in 2.07 seconds
- All 200 sockets remained active throughout the test period
- No socket failures or drops observed

**Metrics Collected:**
- Total data points: ~15-20 samples (collected every 5 seconds)
- Charts generated: 5 PNG files (active_sockets.png, cpu_usage.png, memory_usage.png, network_throughput.png, combined_metrics.png)

---

## Metrics Analysis

### 1. CPU Usage

**Observed Values:**
- Baseline (before sockets): ~8.82%
- During test (200 sockets): 8.82% - 8.83%
- **Change:** Negligible (< 0.01% increase)

**Expected Behavior (from Case 04076665):**
- Production case showed reserved CPUs spending **90-100% time in "si" (system interrupt)**
- With 200 sockets, expected noticeable CPU overhead in `packet_rcv()` and `consume_skb()` functions
- Expected CPU utilization increase on reserved CPUs handling network interrupts

**Analysis:**
- ❌ **Issue NOT clearly demonstrated** - CPU usage remained stable
- The metric collected is overall CPU usage, not specifically system interrupt (si) time
- Test environment differs from production:
  - No reserved CPUs configured
  - No PerformanceProfile isolating CPUs
  - Lower network traffic load
  - Different NUMA topology (single NUMA vs. dual NUMA in production)

### 2. Memory Usage

**Observed Values:**
- Baseline: ~24.38%
- During test: 24.33% - 24.73%
- **Change:** Minimal (~0.4% variation)

**Expected Behavior:**
- Memory usage should remain relatively stable
- AF_PACKET sockets primarily affect CPU, not memory

**Analysis:**
- ✅ **As Expected** - Memory usage stable, no memory-related issues observed

### 3. Network Throughput

**Observed Values:**
- RX: 0.00 - 0.12 MB/s (very low)
- TX: 0.00 - 2.19 MB/s (low)
- Most samples: 0.00 MB/s

**Expected Behavior:**
- Production case showed network throughput degradation (30-50% reduction with 700 sockets)
- With 200 sockets, expected 70-90% of baseline throughput

**Analysis:**
- ⚠️ **Cannot Evaluate** - Test environment has minimal network traffic
- No iperf test running to measure throughput degradation
- Low baseline traffic makes degradation measurement impossible
- Need concurrent iperf test to measure impact

### 4. Active Socket Count

**Observed Values:**
- Created: 200 sockets
- Maintained: 200/200 active throughout test
- **Stability:** 100% (no socket failures)

**Expected Behavior:**
- Sockets should remain active
- Socket count should correlate with performance degradation

**Analysis:**
- ✅ **As Expected** - All sockets remained active
- Socket creation successful, demonstrating ability to open 200 AF_PACKET sockets

---

## Comparison with Problem Statement

### Key Points from Airtel Case 04076665:

1. **Root Cause:** Large number of AF_PACKET sockets open on the node
   - ✅ **Confirmed:** Successfully created 200 sockets

2. **Symptom:** Reserved CPUs spending 90-100% time in "si" (system interrupt)
   - ❌ **Not Observed:** CPU usage remained stable at ~8.83%
   - **Reason:** Test environment lacks reserved CPUs and high network load

3. **Impact Scope:** Affects entire node, regardless of network namespace
   - ⚠️ **Cannot Verify:** Test ran on single system without multiple namespaces

4. **Kernel Functions:** `packet_rcv()` and `consume_skb()` show high CPU usage
   - ❌ **Not Measured:** Requires `perf top` or `perf record` to observe
   - Current metrics don't capture kernel function-level CPU usage

5. **Formula:** `NUMA1 CPU load = PPS × (fixed-cost + per-af-socket-cost × nb-of-af-sockets)`
   - ⚠️ **Cannot Validate:** No reserved CPUs, no NUMA1, minimal PPS (packets per second)

---

## Limitations of Current Test

### Environment Differences:

1. **No Reserved CPUs:**
   - Production: 8 reserved CPUs (0,1,20,21,52,53,104,105,156,157)
   - Test: All CPUs available, no isolation
   - **Impact:** Cannot observe CPU overload on specific reserved CPUs

2. **No PerformanceProfile:**
   - Production: PerformanceProfile configured with CPU isolation
   - Test: Standard CPU scheduling
   - **Impact:** Interrupts not pinned to specific CPUs

3. **Low Network Load:**
   - Production: High throughput (400-500 Gbps mentioned)
   - Test: Minimal traffic (0-2 MB/s)
   - **Impact:** Cannot measure throughput degradation

4. **No Concurrent iperf Test:**
   - Production: iperf server/client running to measure degradation
   - Test: Only AF_PACKET socket creation
   - **Impact:** No baseline throughput to compare against

5. **Single NUMA Node:**
   - Production: Dual NUMA (NUMA0 and NUMA1)
   - Test: Single NUMA node
   - **Impact:** Cannot observe NUMA-specific effects

6. **Missing Kernel-Level Metrics:**
   - Production: `perf top` showing `packet_rcv()` and `consume_skb()` overhead
   - Test: Only system-level CPU usage
   - **Impact:** Cannot see kernel function-level impact

---

## Recommendations for Better Reproduction

### To Properly Demonstrate the Issue:

1. **Add System Interrupt Time Metric:**
   ```go
   // Read /proc/stat and calculate si (softirq) time specifically
   // Monitor: 100 * rate(node_cpu_seconds_total{mode="softirq"}[5m])
   ```

2. **Run Concurrent iperf Test:**
   - Start iperf server and client before creating sockets
   - Measure baseline throughput
   - Create sockets and measure throughput degradation
   - Compare: baseline vs. with-sockets throughput

3. **Use perf for Kernel-Level Analysis:**
   - Run `perf top -C <reserved-cpus>` during socket creation
   - Look for `packet_rcv()` and `consume_skb()` functions
   - Measure CPU percentage spent in these functions

4. **Test with Reserved CPUs:**
   - Configure PerformanceProfile with reserved CPUs
   - Monitor specific reserved CPUs (not overall CPU)
   - Observe system interrupt time on reserved CPUs

5. **Increase Socket Count:**
   - Test with 500 and 700 sockets (as per case phases)
   - Observe progressive degradation

6. **Generate Network Load:**
   - Use iperf or similar tool to generate high throughput
   - Measure PPS (packets per second)
   - Correlate PPS with socket count and CPU load

---

## Conclusion

### Test Results Summary:

✅ **Successfully Demonstrated:**
- Ability to create 200 AF_PACKET sockets simultaneously
- Socket stability (all sockets remained active)
- Metrics collection and chart generation working correctly

❌ **Failed to Demonstrate:**
- CPU overhead on reserved CPUs (no reserved CPUs configured)
- System interrupt time increase (metric not collected)
- Network throughput degradation (no concurrent iperf test)
- Kernel function-level impact (`packet_rcv()`, `consume_skb()`)

⚠️ **Partially Demonstrated:**
- Socket creation mechanism works as expected
- Infrastructure for metrics collection is functional

### Alignment with Problem Statement:

The test **successfully replicated the socket creation** aspect of the issue but **failed to demonstrate the performance degradation** due to:

1. **Environment differences** (no reserved CPUs, no PerformanceProfile)
2. **Missing metrics** (system interrupt time, kernel function CPU usage)
3. **Insufficient load** (no concurrent network throughput test)
4. **Different topology** (single NUMA vs. dual NUMA)

### Next Steps:

1. Enhance metrics collection to include system interrupt (si) time
2. Add concurrent iperf test to measure throughput degradation
3. Test with PerformanceProfile and reserved CPUs if possible
4. Increase socket count to 500 and 700 to observe progressive degradation
5. Add perf integration to capture kernel function-level metrics

---

## Generated Artifacts

### Charts Generated:
- `/tmp/metrics/active_sockets.png` - Socket count over time
- `/tmp/metrics/cpu_usage.png` - CPU usage over time
- `/tmp/metrics/memory_usage.png` - Memory usage over time
- `/tmp/metrics/network_throughput.png` - Network RX/TX over time
- `/tmp/metrics/combined_metrics.png` - Combined overview

### Log Files:
- `/tmp/reproducer_output.log` - Complete test output

### Capture Files:
- `/tmp/pcaps/capture_*.pcap` - 200 packet capture files (if traffic present)

---

**Report Generated:** December 1, 2025  
**Test Tool:** `af_packet_reproducer` v1.0  
**Reference:** Airtel Case 04076665, RHEL-83393
