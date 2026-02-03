// SPDX-License-Identifier: GPL-2.0
/*
 * eBPF Packet Latency Measurement Program
 *
 * Purpose: Measure the time packets spend traversing the kernel network stack.
 * This helps identify "cost per packet" by measuring:
 * - Time from XDP (earliest hook) to TC egress (post-processing)
 * - Per-packet processing time in softIRQ context
 * - Queue delays and kernel stack overhead
 *
 * Architecture:
 * - XDP program records packet arrival timestamp with packet hash
 * - TC ingress/egress programs calculate time delta
 * - Statistics are exported via pinned maps
 *
 * This is independent of ebpf-stress and measures actual kernel overhead.
 *
 * Author: NetPerfTest Project
 * Reference: https://github.com/midu16/NetPerfTest
 */

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/ipv6.h>
#include <linux/in.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

/* Maximum entries for tracking in-flight packets */
#define MAX_TRACKED_PACKETS 65536

/* Histogram buckets for latency distribution (in microseconds) */
#define LATENCY_BUCKET_COUNT 16

/* TC action codes */
#ifndef TC_ACT_OK
#define TC_ACT_OK 0
#endif

/* ============================================================================
 * Data Structures
 * ============================================================================ */

/* Packet tracking entry - stores arrival timestamp */
struct packet_timestamp {
    __u64 timestamp_ns;
    __u32 ifindex;
    __u32 len;
};

/* Per-interface statistics */
struct interface_stats {
    __u64 packets_total;
    __u64 bytes_total;
    __u64 latency_ns_total;      /* Total latency in nanoseconds */
    __u64 latency_min_ns;        /* Minimum latency observed */
    __u64 latency_max_ns;        /* Maximum latency observed */
    __u64 xdp_packets;           /* Packets seen at XDP */
    __u64 tc_ingress_packets;    /* Packets seen at TC ingress */
    __u64 tc_egress_packets;     /* Packets seen at TC egress */
    __u64 softirq_time_ns;       /* Time spent in softIRQ context */
};

/* ============================================================================
 * BPF Maps - Pinned for ebpf-exporter access
 * ============================================================================ */

/* Map to track in-flight packets: hash -> timestamp */
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, MAX_TRACKED_PACKETS);
    __type(key, __u32);  /* Packet hash */
    __type(value, struct packet_timestamp);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} packet_timestamps SEC(".maps");

/* Per-interface statistics: ifindex -> stats */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_HASH);
    __uint(max_entries, 256);
    __type(key, __u32);  /* Interface index */
    __type(value, struct interface_stats);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} interface_latency_stats SEC(".maps");

/* Latency histogram buckets (microseconds): bucket_id -> count
 * Buckets: 0-1us, 1-2us, 2-4us, 4-8us, 8-16us, 16-32us, 32-64us, 
 *          64-128us, 128-256us, 256-512us, 512-1024us, 1-2ms, 
 *          2-4ms, 4-8ms, 8-16ms, 16ms+ */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, LATENCY_BUCKET_COUNT);
    __type(key, __u32);
    __type(value, __u64);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} latency_histogram SEC(".maps");

/* Global packet counters */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} global_packets SEC(".maps");

/* Global latency sum */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, __u64);
    __uint(pinning, LIBBPF_PIN_BY_NAME);
} global_latency_ns SEC(".maps");

/* ============================================================================
 * Helper Functions
 * ============================================================================ */

/*
 * Calculate a simple hash from packet headers for tracking
 * Uses: src IP, dst IP, src port, dst port, protocol
 */
static __always_inline __u32 calculate_packet_hash(void *data, void *data_end)
{
    struct ethhdr *eth = data;
    __u32 hash = 0;
    
    if ((void *)(eth + 1) > data_end)
        return 0;
    
    /* Include MAC addresses in hash */
    hash = eth->h_source[0] ^ eth->h_source[5];
    hash ^= eth->h_dest[0] ^ eth->h_dest[5];
    
    if (eth->h_proto == bpf_htons(ETH_P_IP)) {
        struct iphdr *ip = (void *)(eth + 1);
        if ((void *)(ip + 1) > data_end)
            return hash;
        
        hash ^= ip->saddr;
        hash ^= ip->daddr;
        hash ^= ip->protocol;
        hash ^= ip->id;  /* Include IP ID for better uniqueness */
        
        /* Include ports for TCP/UDP */
        if (ip->protocol == IPPROTO_TCP || ip->protocol == IPPROTO_UDP) {
            __u16 *ports = (void *)ip + (ip->ihl * 4);
            if ((void *)(ports + 2) <= data_end) {
                hash ^= ports[0];  /* src port */
                hash ^= ports[1];  /* dst port */
            }
        }
    } else if (eth->h_proto == bpf_htons(ETH_P_IPV6)) {
        struct ipv6hdr *ip6 = (void *)(eth + 1);
        if ((void *)(ip6 + 1) > data_end)
            return hash;
        
        hash ^= ip6->saddr.s6_addr32[0] ^ ip6->saddr.s6_addr32[3];
        hash ^= ip6->daddr.s6_addr32[0] ^ ip6->daddr.s6_addr32[3];
        hash ^= ip6->nexthdr;
    }
    
    return hash;
}

/*
 * Get histogram bucket for a latency value (in nanoseconds)
 */
static __always_inline __u32 get_latency_bucket(__u64 latency_ns)
{
    __u64 latency_us = latency_ns / 1000;  /* Convert to microseconds */
    
    if (latency_us < 1) return 0;
    if (latency_us < 2) return 1;
    if (latency_us < 4) return 2;
    if (latency_us < 8) return 3;
    if (latency_us < 16) return 4;
    if (latency_us < 32) return 5;
    if (latency_us < 64) return 6;
    if (latency_us < 128) return 7;
    if (latency_us < 256) return 8;
    if (latency_us < 512) return 9;
    if (latency_us < 1024) return 10;
    if (latency_us < 2048) return 11;
    if (latency_us < 4096) return 12;
    if (latency_us < 8192) return 13;
    if (latency_us < 16384) return 14;
    return 15;  /* 16ms+ */
}

/*
 * Update latency histogram
 */
static __always_inline void update_histogram(__u64 latency_ns)
{
    __u32 bucket = get_latency_bucket(latency_ns);
    __u64 *count = bpf_map_lookup_elem(&latency_histogram, &bucket);
    if (count)
        __sync_fetch_and_add(count, 1);
}

/*
 * Update interface statistics
 */
static __always_inline void update_interface_stats(__u32 ifindex, __u32 pkt_len, 
                                                    __u64 latency_ns, int hook_type)
{
    struct interface_stats *stats, new_stats = {};
    
    stats = bpf_map_lookup_elem(&interface_latency_stats, &ifindex);
    if (!stats) {
        /* Initialize new entry */
        new_stats.latency_min_ns = latency_ns > 0 ? latency_ns : ~0ULL;
        new_stats.latency_max_ns = latency_ns;
        bpf_map_update_elem(&interface_latency_stats, &ifindex, &new_stats, BPF_ANY);
        stats = bpf_map_lookup_elem(&interface_latency_stats, &ifindex);
        if (!stats)
            return;
    }
    
    __sync_fetch_and_add(&stats->packets_total, 1);
    __sync_fetch_and_add(&stats->bytes_total, pkt_len);
    
    if (latency_ns > 0) {
        __sync_fetch_and_add(&stats->latency_ns_total, latency_ns);
        
        /* Update min/max (not perfectly atomic but good enough for stats) */
        if (latency_ns < stats->latency_min_ns || stats->latency_min_ns == 0)
            stats->latency_min_ns = latency_ns;
        if (latency_ns > stats->latency_max_ns)
            stats->latency_max_ns = latency_ns;
    }
    
    /* Track per-hook counters */
    switch (hook_type) {
    case 0:  /* XDP */
        __sync_fetch_and_add(&stats->xdp_packets, 1);
        break;
    case 1:  /* TC ingress */
        __sync_fetch_and_add(&stats->tc_ingress_packets, 1);
        break;
    case 2:  /* TC egress */
        __sync_fetch_and_add(&stats->tc_egress_packets, 1);
        break;
    }
}

/*
 * Update global counters
 */
static __always_inline void update_global_stats(__u64 latency_ns)
{
    __u32 key = 0;
    __u64 *packets = bpf_map_lookup_elem(&global_packets, &key);
    if (packets)
        __sync_fetch_and_add(packets, 1);
    
    if (latency_ns > 0) {
        __u64 *latency = bpf_map_lookup_elem(&global_latency_ns, &key);
        if (latency)
            __sync_fetch_and_add(latency, latency_ns);
    }
}

/* ============================================================================
 * XDP Program - Record Packet Arrival Time
 * ============================================================================ */

SEC("xdp")
int xdp_latency_ingress(struct xdp_md *ctx)
{
    void *data = (void *)(long)ctx->data;
    void *data_end = (void *)(long)ctx->data_end;
    __u32 pkt_len = (__u32)(data_end - data);
    __u64 now = bpf_ktime_get_ns();
    
    /* Calculate packet hash for tracking */
    __u32 hash = calculate_packet_hash(data, data_end);
    if (hash == 0)
        goto out;
    
    /* Store arrival timestamp */
    struct packet_timestamp ts = {
        .timestamp_ns = now,
        .ifindex = ctx->ingress_ifindex,
        .len = pkt_len,
    };
    bpf_map_update_elem(&packet_timestamps, &hash, &ts, BPF_ANY);
    
    /* Update XDP statistics */
    update_interface_stats(ctx->ingress_ifindex, pkt_len, 0, 0);
    
out:
    return XDP_PASS;
}

/* ============================================================================
 * TC Ingress Program - Track packet at TC layer
 * ============================================================================ */

SEC("tc")
int tc_latency_ingress(struct __sk_buff *skb)
{
    void *data = (void *)(long)skb->data;
    void *data_end = (void *)(long)skb->data_end;
    __u64 now = bpf_ktime_get_ns();
    __u64 latency_ns = 0;
    
    /* Calculate packet hash */
    __u32 hash = calculate_packet_hash(data, data_end);
    if (hash == 0)
        goto out;
    
    /* Look up arrival timestamp from XDP */
    struct packet_timestamp *ts = bpf_map_lookup_elem(&packet_timestamps, &hash);
    if (ts && ts->timestamp_ns > 0) {
        latency_ns = now - ts->timestamp_ns;
        
        /* Update histogram */
        update_histogram(latency_ns);
        
        /* Update global stats */
        update_global_stats(latency_ns);
    }
    
    /* Update TC ingress statistics */
    update_interface_stats(skb->ifindex, skb->len, latency_ns, 1);
    
out:
    return TC_ACT_OK;
}

/* ============================================================================
 * TC Egress Program - Final measurement point
 * ============================================================================ */

SEC("tc")
int tc_latency_egress(struct __sk_buff *skb)
{
    void *data = (void *)(long)skb->data;
    void *data_end = (void *)(long)skb->data_end;
    __u64 now = bpf_ktime_get_ns();
    __u64 latency_ns = 0;
    
    /* Calculate packet hash */
    __u32 hash = calculate_packet_hash(data, data_end);
    if (hash == 0)
        goto out;
    
    /* Look up arrival timestamp from XDP */
    struct packet_timestamp *ts = bpf_map_lookup_elem(&packet_timestamps, &hash);
    if (ts && ts->timestamp_ns > 0) {
        latency_ns = now - ts->timestamp_ns;
        
        /* Update histogram for egress latency */
        update_histogram(latency_ns);
        
        /* Update global stats */
        update_global_stats(latency_ns);
        
        /* Clean up the timestamp entry */
        bpf_map_delete_elem(&packet_timestamps, &hash);
    }
    
    /* Update TC egress statistics */
    update_interface_stats(skb->ifindex, skb->len, latency_ns, 2);
    
out:
    return TC_ACT_OK;
}

/* ============================================================================
 * License Declaration
 * ============================================================================ */

char _license[] SEC("license") = "GPL";
__u32 _version SEC("version") = 1;
