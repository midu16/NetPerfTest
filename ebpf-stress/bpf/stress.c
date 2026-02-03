// SPDX-License-Identifier: GPL-2.0
/*
 * eBPF Stress Test Program - Configurable N Loops Per Packet
 *
 * Purpose: Increase kernel processing cost per packet by looping N times
 *          where N is a configurable parameter passed from userspace.
 *
 * This version uses bounded loops that are verifier-friendly and work
 * across all kernel versions (5.4+).
 *
 * Maximum iterations: ~2,500 (50 outer × 50 inner loops)
 * For higher iterations, use the kernel module in kernel-module/
 *
 * Hook Points:
 * - XDP: Earliest point in network stack
 * - TC:  Traffic Control (ingress/egress)
 * - Socket: Per-socket filtering
 *
 * Maps are pinned to /sys/fs/bpf/ebpf-stress/ for external access
 * by the ebpf-exporter component.
 *
 * Author: Based on AF_PACKET socket issue analysis
 * Reference: https://github.com/midu16/NetPerfTest
 */

#include <linux/bpf.h>
#include <linux/in.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

/* Maximum iterations with bounded loops (verifier-friendly) */
#define MAX_OUTER_LOOPS 50
#define MAX_INNER_LOOPS 50
#define MAX_TOTAL_LOOPS (MAX_OUTER_LOOPS * MAX_INNER_LOOPS)  /* 2,500 */

/* Default loop count if not configured */
#define DEFAULT_LOOP_COUNT 1000

/* TC action codes */
#ifndef TC_ACT_OK
#define TC_ACT_OK 0
#endif

/* ============================================================================
 * BPF Maps - Pinned to /sys/fs/bpf/ebpf-stress/ for ebpf-exporter access
 * ============================================================================ */

/* Configuration map: stores loop count parameter from userspace */
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u32);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} config_loop_count SEC(".maps");

/* Configuration map: stores whether program is enabled */
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u32);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} config_enabled SEC(".maps");

/* Statistics: packets processed (per-CPU for lock-free access) */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} stats_packets SEC(".maps");

/* Statistics: total loop iterations performed */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} stats_loops SEC(".maps");

/* Statistics: total bytes processed */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} stats_bytes SEC(".maps");

/* Statistics: processing time (in nanoseconds) */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} stats_time_ns SEC(".maps");

/* ============================================================================
 * Helper Functions
 * ============================================================================ */

/*
 * Get configured loop count from map
 */
static __always_inline __u32 get_loop_count(void)
{
    __u32 key = 0;
    __u32 *count = bpf_map_lookup_elem(&config_loop_count, &key);
    
    if (!count) {
        return DEFAULT_LOOP_COUNT;
    }
    
    /* Cap at maximum to prevent verifier issues */
    return (*count > MAX_TOTAL_LOOPS) ? MAX_TOTAL_LOOPS : *count;
}

/*
 * Check if program is enabled
 */
static __always_inline int is_enabled(void)
{
    __u32 key = 0;
    __u32 *enabled = bpf_map_lookup_elem(&config_enabled, &key);
    
    /* Default to enabled if not configured */
    return !enabled || *enabled;
}

/*
 * Perform stress loop with bounded iterations (verifier-friendly)
 * Uses nested loops: outer × inner = up to MAX_TOTAL_LOOPS iterations
 */
static __always_inline __u64 perform_stress_loop(__u32 target_loops)
{
    __u64 overhead = 0;
    __u64 work = 0;
    __u32 outer_limit, inner_limit;
    __u32 outer, inner;
    
    /* Calculate loop limits */
    if (target_loops >= MAX_TOTAL_LOOPS) {
        outer_limit = MAX_OUTER_LOOPS;
        inner_limit = MAX_INNER_LOOPS;
    } else if (target_loops >= MAX_INNER_LOOPS) {
        outer_limit = target_loops / MAX_INNER_LOOPS;
        if (outer_limit > MAX_OUTER_LOOPS) outer_limit = MAX_OUTER_LOOPS;
        inner_limit = MAX_INNER_LOOPS;
    } else {
        outer_limit = 1;
        inner_limit = target_loops;
        if (inner_limit > MAX_INNER_LOOPS) inner_limit = MAX_INNER_LOOPS;
    }
    
    /* Bounded nested loops - verifier can prove termination */
    #pragma unroll 1
    for (outer = 0; outer < MAX_OUTER_LOOPS; outer++) {
        if (outer >= outer_limit) break;
        
        #pragma unroll 1
        for (inner = 0; inner < MAX_INNER_LOOPS; inner++) {
            if (inner >= inner_limit) break;
            
            /* CPU-intensive operations (~20 ops per iteration) */
            __u64 idx = outer * MAX_INNER_LOOPS + inner;
            
            /* Arithmetic chain */
            work = idx * 7 + 13;
            work = work * 11 + 17;
            work = work * 13 + 19;
            work = work * 17 + 23;
            
            /* Bitwise operations */
            work ^= (work << 7);
            work ^= (work >> 11);
            work ^= (work << 13);
            work ^= (work >> 17);
            
            /* More arithmetic */
            overhead += work;
            overhead ^= (overhead << 3);
            overhead = overhead * 3 + 29;
            overhead ^= (overhead >> 5);
            overhead = overhead * 7 + 31;
            overhead ^= (overhead << 11);
            overhead += idx * 37;
        }
    }
    
    /* Prevent compiler optimization */
    asm volatile("" : "+r"(overhead), "+r"(work));
    
    return overhead + work;
}

/*
 * Update statistics atomically
 */
static __always_inline void update_stats(__u32 loops, __u32 bytes, __u64 time_ns)
{
    __u32 key = 0;
    __u64 *packets_ptr, *loops_ptr, *bytes_ptr, *time_ptr;
    
    packets_ptr = bpf_map_lookup_elem(&stats_packets, &key);
    if (packets_ptr)
        __sync_fetch_and_add(packets_ptr, 1);
    
    loops_ptr = bpf_map_lookup_elem(&stats_loops, &key);
    if (loops_ptr)
        __sync_fetch_and_add(loops_ptr, loops);
    
    bytes_ptr = bpf_map_lookup_elem(&stats_bytes, &key);
    if (bytes_ptr)
        __sync_fetch_and_add(bytes_ptr, bytes);
    
    time_ptr = bpf_map_lookup_elem(&stats_time_ns, &key);
    if (time_ptr)
        __sync_fetch_and_add(time_ptr, time_ns);
}

/* ============================================================================
 * XDP Program - eXpress Data Path (Earliest Hook Point)
 * ============================================================================ */

SEC("xdp")
int xdp_stress_prog(struct xdp_md *ctx)
{
    void *data = (void *)(long)ctx->data;
    void *data_end = (void *)(long)ctx->data_end;
    __u32 pkt_len;
    __u32 loop_count;
    __u64 start_time, end_time;
    
    /* Check if enabled */
    if (!is_enabled())
        return XDP_PASS;
    
    /* Calculate packet length */
    pkt_len = (__u32)(data_end - data);
    
    /* Get configured loop count */
    loop_count = get_loop_count();
    
    /* Record start time */
    start_time = bpf_ktime_get_ns();
    
    /* Perform stress loop */
    perform_stress_loop(loop_count);
    
    /* Record end time */
    end_time = bpf_ktime_get_ns();
    
    /* Update statistics */
    update_stats(loop_count, pkt_len, end_time - start_time);
    
    /* Continue normal packet processing */
    return XDP_PASS;
}

/* ============================================================================
 * TC Program - Traffic Control (Ingress/Egress)
 * ============================================================================ */

SEC("tc")
int tc_stress_prog(struct __sk_buff *skb)
{
    __u32 pkt_len = skb->len;
    __u32 loop_count;
    __u64 start_time, end_time;
    
    /* Check if enabled */
    if (!is_enabled())
        return TC_ACT_OK;
    
    /* Get configured loop count */
    loop_count = get_loop_count();
    
    /* Record start time */
    start_time = bpf_ktime_get_ns();
    
    /* Perform stress loop */
    perform_stress_loop(loop_count);
    
    /* Record end time */
    end_time = bpf_ktime_get_ns();
    
    /* Update statistics */
    update_stats(loop_count, pkt_len, end_time - start_time);
    
    /* Continue normal packet processing */
    return TC_ACT_OK;
}

/* ============================================================================
 * Socket Filter Program
 * ============================================================================ */

SEC("socket")
int socket_stress_prog(struct __sk_buff *skb)
{
    __u32 pkt_len = skb->len;
    __u32 loop_count;
    __u64 start_time, end_time;
    
    /* Check if enabled */
    if (!is_enabled())
        return 0; /* Accept packet */
    
    /* Get configured loop count */
    loop_count = get_loop_count();
    
    /* Record start time */
    start_time = bpf_ktime_get_ns();
    
    /* Perform stress loop */
    perform_stress_loop(loop_count);
    
    /* Record end time */
    end_time = bpf_ktime_get_ns();
    
    /* Update statistics */
    update_stats(loop_count, pkt_len, end_time - start_time);
    
    /* Accept packet (continue processing) */
    return 0;
}

/* ============================================================================
 * License Declaration
 * ============================================================================ */

char _license[] SEC("license") = "GPL";
__u32 _version SEC("version") = 1;
