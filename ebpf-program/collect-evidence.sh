#!/bin/bash
#
# AF_PACKET Issue Evidence Collection Script
#
# Captures all relevant logs and metrics for documenting
# the AF_PACKET socket performance degradation issue.
#

set -e

# Configuration
OUTPUT_DIR="${1:-evidence_$(date +%Y%m%d_%H%M%S)}"
DURATION="${2:-30}"  # Seconds to capture

echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║     AF_PACKET Issue Evidence Collection                         ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""
echo "Output directory: $OUTPUT_DIR"
echo "Capture duration: ${DURATION}s"
echo ""

# Create output directory
mkdir -p "$OUTPUT_DIR"
cd "$OUTPUT_DIR"

echo "📊 Collecting evidence..."

# 1. System information
echo "  [1/10] System information..."
{
    echo "=== System Info ==="
    uname -a
    echo ""
    echo "=== CPU Info ==="
    lscpu | head -20
    echo ""
    echo "=== Memory ==="
    free -h
    echo ""
    echo "=== Uptime ==="
    uptime
} > system_info.txt

# 2. Current CPU stats snapshot
echo "  [2/10] CPU snapshot..."
mpstat -P ALL 1 1 > cpu_snapshot.txt

# 3. CPU stats over time
echo "  [3/10] CPU stats (${DURATION}s)..."
mpstat -P ALL 1 $DURATION > cpu_stats_timeseries.txt &
MPSTAT_PID=$!

# 4. Softirq counters
echo "  [4/10] Softirq counters..."
{
    echo "=== Before ==="
    cat /proc/softirqs
    sleep $DURATION
    echo ""
    echo "=== After ==="
    cat /proc/softirqs
} > softirq_counters.txt &
SOFTIRQ_PID=$!

# 5. Network statistics
echo "  [5/10] Network statistics..."
{
    echo "=== /proc/net/dev ==="
    cat /proc/net/dev
    echo ""
    echo "=== AF_PACKET sockets ==="
    cat /proc/net/packet 2>/dev/null || echo "No AF_PACKET sockets or permission denied"
    echo ""
    echo "=== AF_PACKET count ==="
    wc -l /proc/net/packet 2>/dev/null || echo "0"
    echo ""
    echo "=== Network interface stats ==="
    ip -s link show
} > network_stats.txt

# 6. eBPF program info
echo "  [6/10] eBPF program info..."
{
    echo "=== Loaded BPF Programs ==="
    sudo bpftool prog list 2>/dev/null || echo "bpftool not available or permission denied"
    echo ""
    echo "=== BPF Maps ==="
    sudo bpftool map list 2>/dev/null || echo "bpftool not available or permission denied"
} > bpf_info.txt

# 7. eBPF metrics (if available)
echo "  [7/10] eBPF metrics..."
{
    echo "=== Prometheus Metrics ==="
    curl -s http://localhost:9091/metrics 2>/dev/null || echo "Metrics endpoint not available"
    echo ""
    echo "=== JSON Stats ==="
    curl -s http://localhost:9091/stats 2>/dev/null || echo "Stats endpoint not available"
} > ebpf_metrics.txt

# 8. Kernel logs
echo "  [8/10] Kernel logs..."
{
    echo "=== Recent kernel messages ==="
    dmesg -T 2>/dev/null | tail -100 || dmesg | tail -100
} > kernel_logs.txt

# 9. Process list
echo "  [9/10] Process information..."
{
    echo "=== Top processes by CPU ==="
    ps aux --sort=-%cpu | head -20
    echo ""
    echo "=== Traffic generator processes ==="
    pgrep -af "traffic-gen" || echo "None"
    echo ""
    echo "=== eBPF stress processes ==="
    pgrep -af "ebpf-stress" || echo "None"
} > process_info.txt

# 10. Wait for background jobs
echo "  [10/10] Waiting for captures to complete..."
wait $MPSTAT_PID 2>/dev/null || true
wait $SOFTIRQ_PID 2>/dev/null || true

# Generate summary
echo ""
echo "📝 Generating summary..."
{
    echo "═══════════════════════════════════════════════════════════════════"
    echo "        AF_PACKET Issue Replication - Evidence Summary"
    echo "═══════════════════════════════════════════════════════════════════"
    echo ""
    echo "Collection Time: $(date)"
    echo "Duration: ${DURATION} seconds"
    echo ""
    
    echo "=== CPU Softirq Summary ==="
    echo "Average softirq percentage across all CPUs:"
    grep "Average:" cpu_stats_timeseries.txt | head -1 || grep "all" cpu_snapshot.txt | tail -1
    echo ""
    
    echo "=== Highest Softirq CPUs ==="
    grep -E "^Average:" cpu_stats_timeseries.txt | sort -k7 -rn | head -5 2>/dev/null || \
        grep -E "^[0-9]" cpu_snapshot.txt | sort -k7 -rn | head -5
    echo ""
    
    echo "=== eBPF Metrics ==="
    grep -E "packets_total|loops_total|loops_per_packet" ebpf_metrics.txt 2>/dev/null || echo "Not available"
    echo ""
    
    echo "=== Verdict ==="
    AVG_SOFT=$(grep "Average:.*all" cpu_stats_timeseries.txt 2>/dev/null | awk '{print int($7)}' || echo "0")
    if [ "$AVG_SOFT" -ge 50 ]; then
        echo "✅ AF_PACKET ISSUE SUCCESSFULLY REPLICATED"
        echo "   Average softirq: ${AVG_SOFT}% (threshold: 50%)"
    elif [ "$AVG_SOFT" -ge 20 ]; then
        echo "⚠️  PARTIAL REPLICATION"
        echo "   Average softirq: ${AVG_SOFT}% (threshold: 50%)"
    else
        echo "❌ ISSUE NOT REPLICATED"
        echo "   Average softirq: ${AVG_SOFT}% (threshold: 50%)"
    fi
    echo ""
    echo "═══════════════════════════════════════════════════════════════════"
} | tee summary.txt

# Create archive
echo ""
echo "📦 Creating archive..."
cd ..
tar -czf "${OUTPUT_DIR}.tar.gz" "$OUTPUT_DIR"

echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║  ✅ Evidence Collection Complete                                 ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""
echo "Files saved to: $OUTPUT_DIR/"
echo "Archive: ${OUTPUT_DIR}.tar.gz"
echo ""
echo "Contents:"
ls -la "$OUTPUT_DIR/"
