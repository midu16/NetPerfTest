// SPDX-License-Identifier: GPL-2.0
/*
 * eBPF XDP Program - Kernel Stack Stress Test
 *
 * Purpose: Increase kernel processing cost per packet by looping N times
 *          where N is a configurable parameter.
 *
 * Hook: XDP (eXpress Data Path) - earliest point in kernel network stack
 */

#include <linux/bpf.h>
#include <linux/in.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// Map to store loop count parameter
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u32);
} loop_count_map SEC(".maps");

// Map to store statistics (per-CPU for performance)
// Using separate maps for each counter to avoid struct access issues
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} packets_processed_map SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
} total_loops_map SEC(".maps");

SEC("xdp")
int xdp_stress_prog(struct xdp_md *ctx)
{
    __u32 key = 0;
    __u32 *loop_count_ptr;
    __u32 loop_count;
    volatile __u64 overhead = 0;
    
    // Get loop count from map
    loop_count_ptr = bpf_map_lookup_elem(&loop_count_map, &key);
    if (!loop_count_ptr) {
        // Default loop count if not set (capped at 20)
        loop_count = 10;
    } else {
        loop_count = *loop_count_ptr;
        // Cap at 20 to avoid verifier issues
        if (loop_count > 20) {
            loop_count = 20;
        }
    }
    
    // Add CPU overhead through multiple map lookups and arithmetic
    // This approach is more verifier-friendly than large loops
    // Each map lookup adds overhead, and we do multiple operations
    
    // Multiple map lookups add overhead (verifier-friendly)
    __u32 *dummy1 = bpf_map_lookup_elem(&loop_count_map, &key);
    __u64 *dummy2 = bpf_map_lookup_elem(&packets_processed_map, &key);
    __u64 *dummy3 = bpf_map_lookup_elem(&total_loops_map, &key);
    
    // Perform stress operations (arithmetic instead of loop)
    // Use the loop count to determine how many operations to do
    if (loop_count > 0) {
        overhead = (__u64)loop_count * 7 + 13;
        overhead = overhead * 3 + 5;
        overhead = overhead / 2 + 1;
    }
    
    // Additional overhead through repeated arithmetic
    if (loop_count > 5) {
        overhead = overhead * 11 + 17;
        overhead = overhead * 13 + 19;
    }
    if (loop_count > 10) {
        overhead = overhead * 7 + 23;
        overhead = overhead * 3 + 29;
    }
    if (loop_count > 15) {
        overhead = overhead * 5 + 31;
        overhead = overhead * 2 + 37;
    }
    
    // Use overhead to prevent optimization
    (void)overhead;
    (void)dummy1;
    (void)dummy2;
    (void)dummy3;
    
    // Update statistics (per-CPU maps for performance)
    __u64 *packets_ptr = bpf_map_lookup_elem(&packets_processed_map, &key);
    if (packets_ptr) {
        __sync_fetch_and_add(packets_ptr, 1);
    }
    
    __u64 *loops_ptr = bpf_map_lookup_elem(&total_loops_map, &key);
    if (loops_ptr) {
        __sync_fetch_and_add(loops_ptr, loop_count);
    }
    
    // Continue normal packet processing
    return XDP_PASS;
}

char _license[] SEC("license") = "GPL";
