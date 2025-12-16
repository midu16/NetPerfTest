/*
 * Kernel Module for Network Stack Stress Testing
 *
 * This module hooks into the network stack and adds configurable CPU overhead
 * per packet. Unlike eBPF, there are NO verifier limits - you can use any
 * loop count or complexity.
 *
 * WARNING: This is kernel code. Bugs can crash the system!
 * Only use in test environments.
 */

#include <linux/module.h>
#include <linux/kernel.h>
#include <linux/netfilter.h>
#include <linux/netfilter_ipv4.h>
#include <linux/skbuff.h>
#include <linux/ip.h>
#include <linux/version.h>
#include <linux/moduleparam.h>

MODULE_LICENSE("GPL");
MODULE_AUTHOR("Telco Core");
MODULE_DESCRIPTION("Kernel Stack Stress Test Module");
MODULE_VERSION("1.0");

// Module parameter: loop count per packet
static unsigned int loop_count = 1000;
module_param(loop_count, uint, 0644);
MODULE_PARM_DESC(loop_count, "Number of loops per packet (default: 1000)");

// Statistics
static atomic_t packets_processed = ATOMIC_INIT(0);
static atomic_t total_loops = ATOMIC_INIT(0);

// Hook function - called for each packet
static unsigned int stress_hook(void *priv, struct sk_buff *skb,
                                const struct nf_hook_state *state)
{
    unsigned int loops = loop_count;
    volatile unsigned long dummy = 0;
    unsigned int i;
    
    // NO VERIFIER LIMITS - can use any loop count!
    // This is full kernel C code, not eBPF
    for (i = 0; i < loops; i++) {
        dummy += i * 7 + 13;
        dummy ^= (dummy << 1);
        dummy = dummy * 3 + 17;
    }
    
    // Use dummy to prevent optimization
    (void)dummy;
    
    // Update statistics
    atomic_inc(&packets_processed);
    atomic_add(loops, &total_loops);
    
    // Accept packet (continue normal processing)
    return NF_ACCEPT;
}

// Netfilter hook structure
static struct nf_hook_ops nfho = {
    .hook = stress_hook,
    .pf = NFPROTO_IPV4,
    .hooknum = NF_INET_PRE_ROUTING,  // Hook early in packet processing
    .priority = NF_IP_PRI_FIRST,      // Highest priority
};

// Module initialization
static int __init stress_init(void)
{
    int ret;
    
    printk(KERN_INFO "stress_module: Loading with loop_count=%u\n", loop_count);
    
    ret = nf_register_net_hook(&init_net, &nfho);
    if (ret < 0) {
        printk(KERN_ERR "stress_module: Failed to register netfilter hook\n");
        return ret;
    }
    
    printk(KERN_INFO "stress_module: Successfully loaded\n");
    printk(KERN_INFO "stress_module: Hooked into NF_INET_PRE_ROUTING\n");
    printk(KERN_INFO "stress_module: Adding %u loops per packet\n", loop_count);
    
    return 0;
}

// Module cleanup
static void __exit stress_exit(void)
{
    nf_unregister_net_hook(&init_net, &nfho);
    
    printk(KERN_INFO "stress_module: Unloaded\n");
    printk(KERN_INFO "stress_module: Processed %d packets\n", 
           atomic_read(&packets_processed));
    printk(KERN_INFO "stress_module: Total loops: %d\n", 
           atomic_read(&total_loops));
}

module_init(stress_init);
module_exit(stress_exit);
