# CNF-20409 - Network & Performance Tools Suite 

**Purpose**: Comprehensive containerized tools suite for network testing, performance analysis, and system diagnostics in OpenShift/Kubernetes environments.

---

## Overview

This project provides two specialized container images and an automated deployment system:

1. **Network Tools Container** (`network-tools`) - tcpdump, iperf3, and network utilities
2. **Performance Tools Container** (`perf-tools`) - perf for CPU performance analysis

Both containers can be deployed with full system privileges for deep diagnostics on OpenShift nodes.

---

## 🎯 Key Features

### Network Tools
- **Packet Capture**: tcpdump with full capabilities
- **Bandwidth Testing**: iperf3 server/client automation
- **Multi-Process Testing**: Launch 500 concurrent tcpdump processes
- **Network Utilities**: iproute, iputils, bind-utils, net-tools
- **Automated Testing**: Built-in iperf client/server orchestration

### Performance Tools
- **CPU Profiling**: perf top for real-time CPU analysis
- **Host Access**: Full host PID and network namespace access
- **Multi-Core Analysis**: Target specific CPU cores (e.g., 0,1,20,21)

### System Checks
- **Kernel Version**: Compare running kernel against target version
- **RHEL Fix Detection**: Identify if RHEL-88921 fix is available

### Deployment Automation
- **KUBECONFIG Support**: No need for `oc login`, use kubeconfig directly
- **Multi-Node Deployment**: Deploy on specific nodes with node selectors
- **Privilege Management**: Automatic SCC application
- **Resource Management**: Configurable CPU/memory limits

---

## 📦 Container Images

### 1. Network Tools (`network-tools`)

**Base**: Fedora 40

**Included Tools**:
- `tcpdump` - Packet capture and analysis
- `iperf3` - Network bandwidth testing
- `iproute` - Advanced network configuration (ip, ss, tc)
- `iputils` - ping, traceroute, arping
- `bind-utils` - DNS tools (dig, nslookup, host)
- `net-tools` - Classic tools (netstat, ifconfig, route)
- `procps-ng` - Process monitoring (ps, top, watch)
- `vim-minimal` - Text editor
- `bash-completion` - Shell completion

**Dockerfile**: `Dockerfile.network-tools`

### 2. Performance Tools (`perf-tools`)

**Base**: Fedora 40

**Included Tools**:
- `perf` - Linux performance analysis tool
- `kernel-tools` - Kernel debugging utilities
- `procps-ng` - Process utilities
- `util-linux` - System utilities

**Dockerfile**: `Dockerfile.perf`

---

## 🚀 Quick Start

### Prerequisites

```bash
# Set your kubeconfig
export KUBECONFIG=/path/to/kubeconfig

# Or pass it with each command
make <target> KUBECONFIG=/path/to/kubeconfig
```

### 1. Build and Push Images

```bash
cd /home/midu/telco-core/CNF-20409

# Build network-tools image
make build-push

# Build perf-tools image
make build-push-perf

# Or build both
make build-push && make build-push-perf
```

### 2. Deploy Network Tools Pods

```bash
# Deploy 3 network-tools pods
make deploy KUBECONFIG=/path/to/kubeconfig

# Check status
make status KUBECONFIG=/path/to/kubeconfig
```

### 3. Deploy Performance Tools Pod

```bash
# Deploy perf-tools pod
make deploy-perf KUBECONFIG=/path/to/kubeconfig
```

### 4. Run Tests

```bash
# Terminal 1: Start iperf3 server
make iperf-server KUBECONFIG=/path/to/kubeconfig

# Terminal 2: Run iperf3 client (auto-detects server IP)
make iperf-client KUBECONFIG=/path/to/kubeconfig

# Run perf top on specific CPUs
make perf-top KUBECONFIG=/path/to/kubeconfig
```

---

## 📊 Pod Deployments

### Network Tools Pods

| Pod Name | Node | IP Mode | Purpose |
|----------|------|---------|---------|
| `cat-1` | hub-ctlplane-0.5g-deployment.lab | hostNetwork | Network tools pod 1 |
| `cat-2` | hub-ctlplane-0.5g-deployment.lab | hostNetwork | Network tools pod 2 |
| `cat-3` | hub-ctlplane-2.5g-deployment.lab | hostNetwork | Network tools pod 3 |

**Pod Configuration**:
- `hostNetwork: true` - Uses node's network namespace
- `hostPID: true` - Can see host processes
- `privileged: true` - Full system access
- Capabilities: `NET_ADMIN`, `NET_RAW`, `SYS_ADMIN`
- Volume: `/data/pcaps` (emptyDir for packet captures)
- Resources: 500m-2 CPU, 512Mi-2Gi memory

### Performance Tools Pod

| Pod Name | Node | Purpose |
|----------|------|---------|
| `perf-pod` | hub-ctlplane-0.5g-deployment.lab | CPU performance analysis |

**Pod Configuration**:
- `hostNetwork: true` - Access to host network
- `hostPID: true` - Access to all host processes
- `privileged: true` - Full system access
- Capabilities: `SYS_ADMIN`, `SYS_PTRACE`, `PERFMON`
- Resources: 500m-2 CPU, 512Mi-2Gi memory

---

## 🎮 Makefile Targets

### Build Targets

| Target | Description |
|--------|-------------|
| `make build` | Build network-tools container image |
| `make push` | Push network-tools image to registry |
| `make build-push` | Build and push network-tools |
| `make test` | Test network-tools image locally |
| `make build-perf` | Build perf-tools container image |
| `make push-perf` | Push perf-tools image to registry |
| `make build-push-perf` | Build and push perf-tools |

### Deployment Targets

| Target | Description |
|--------|-------------|
| `make create-namespace` | Create namespace with privileged SCC |
| `make deploy` | Deploy all 3 network-tools pods |
| `make status` | Show pod status |
| `make cleanup` | Delete network-tools pods |
| `make cleanup-namespace` | Delete namespace |

### Pod Access Targets

| Target | Description |
|--------|-------------|
| `make exec-cat1` | Shell into cat-1 |
| `make exec-cat2` | Shell into cat-2 |
| `make exec-cat3` | Shell into cat-3 |
| `make logs-cat1` | Show cat-1 logs |
| `make logs-cat2` | Show cat-2 logs |
| `make logs-cat3` | Show cat-3 logs |

### Network Testing Targets

| Target | Description |
|--------|-------------|
| `make iperf-server` | Run iperf3 server in cat-3 |
| `make iperf-client` | Run iperf3 client from cat-1 to cat-3 (auto-detects IP) |
| `make tcpdump-loop` | Start 500 tcpdump processes in cat-2 |

### Performance Analysis Targets

| Target | Description |
|--------|-------------|
| `make deploy-perf` | Deploy perf-tools pod |
| `make perf-top` | Run `perf top -C 0,1,20,21 -z` |
| `make cleanup-perf` | Delete perf-tools pod |

### System Check Targets

| Target | Description |
|--------|-------------|
| `make check-kernel` | Check node kernel version vs target |

---

## 💡 Common Use Cases

### Use Case 1: Network Bandwidth Testing (Automated)

**Most Common**: Use the automated targets

```bash
# Terminal 1: Start iperf3 server in cat-3
make iperf-server KUBECONFIG=/path/to/kubeconfig

# Terminal 2: Run iperf3 client from cat-1 to cat-3
# (automatically detects cat-3 IP and runs 10-minute test with 5s intervals)
make iperf-client KUBECONFIG=/path/to/kubeconfig
```

**Output**:
- Test duration: 600 seconds (10 minutes)
- Reporting interval: 5 seconds
- Automatic IP detection for cat-3

### Use Case 2: Packet Capture - Single Instance

```bash
# Shell into any pod
make exec-cat1 KUBECONFIG=/path/to/kubeconfig

# Inside pod - capture on all interfaces
tcpdump -qni any -w /data/pcaps/capture.pcap

# Capture specific traffic
tcpdump -qni any 'tcp port 80' -w /data/pcaps/http.pcap

# Copy file out
oc cp network-tools/cat-1:/data/pcaps/capture.pcap ./capture.pcap
```

### Use Case 3: Packet Capture - Mass Testing (500 Processes)

**Purpose**: Test system behavior under high process load

```bash
# Start 500 tcpdump processes in cat-2
make tcpdump-loop KUBECONFIG=/path/to/kubeconfig

# Verify processes are running
make exec-cat2 KUBECONFIG=/path/to/kubeconfig
# Inside pod:
ps aux | grep tcpdump | wc -l  # Should show ~500

# Check capture files
ls -lh /data/pcaps/  # Shows toto1, toto2, ..., toto500
```

**Features**:
- Spawns 500 background tcpdump processes
- Small delays between spawns to prevent OOM
- Silent mode (stderr suppressed)
- Files: `/data/pcaps/toto1` through `/data/pcaps/toto500`

### Use Case 4: CPU Performance Analysis

```bash
# Deploy perf pod
make deploy-perf KUBECONFIG=/path/to/kubeconfig

# Run perf top on specific CPUs (0, 1, 20, 21)
make perf-top KUBECONFIG=/path/to/kubeconfig

# Cleanup when done
make cleanup-perf KUBECONFIG=/path/to/kubeconfig
```

**perf top flags**:
- `-C 0,1,20,21` - Monitor specific CPU cores
- `-z` - Show symbol names (demangle C++ symbols)

### Use Case 5: Kernel Version Check

```bash
# Check kernel version on default node
make check-kernel KUBECONFIG=/path/to/kubeconfig

# Check different node
make check-kernel NODE_NAME=hub-ctlplane-2.5g-deployment.lab KUBECONFIG=/path/to/kubeconfig

# Check against different target version
make check-kernel TARGET_KERNEL=5.14.0-600 KUBECONFIG=/path/to/kubeconfig
```

**Output**:
- Extracts kernel version (e.g., `5.14.0-570.62.1.el9_6.x86_64` → `5.14.0-570`)
- Compares against target (default: `5.14.0-586`)
- Shows if RHEL-88921 fix is available

**Example Output**:
```
📋 Full kernel version: 5.14.0-570.62.1.el9_6.x86_64
🔢 Extracted version: 5.14.0-570
🎯 Target version:    5.14.0-586

✗ Current kernel (5.14.0-570) is OLDER than target (5.14.0-586)
⚠  Kernel fix within RHEL-88921 its not available
```

### Use Case 6: Multi-Node Simultaneous Capture

```bash
# Terminal 1 - cat-1 (node 0)
make exec-cat1 KUBECONFIG=/path/to/kubeconfig
tcpdump -qni any -w /data/pcaps/node0.pcap

# Terminal 2 - cat-3 (node 2)
make exec-cat3 KUBECONFIG=/path/to/kubeconfig
tcpdump -qni any -w /data/pcaps/node2.pcap

# Terminal 3 - Generate traffic
make iperf-client KUBECONFIG=/path/to/kubeconfig
```

---

## ⚙️ Configuration Variables

All targets support these variables:

### Image Configuration

```bash
IMAGE_REGISTRY=quay.io          # Container registry
IMAGE_NAMESPACE=midu            # Registry namespace/user
IMAGE_NAME=network-tools        # Network tools image name
IMAGE_TAG=latest                # Image tag
PERF_IMAGE_NAME=perf-tools      # Perf tools image name
PERF_IMAGE_TAG=latest           # Perf image tag
```

### Kubernetes Configuration

```bash
KUBECONFIG=$HOME/.kube/config   # Path to kubeconfig file
NAMESPACE=network-tools         # Namespace for deployments
```

### System Check Configuration

```bash
TARGET_KERNEL=5.14.0-586                          # Target kernel version
NODE_NAME=hub-ctlplane-0.5g-deployment.lab       # Node to check
```

### Example Usage

```bash
# Custom registry
make build-push IMAGE_REGISTRY=docker.io IMAGE_NAMESPACE=myteam

# Custom namespace
make deploy NAMESPACE=my-tools KUBECONFIG=/path/to/kubeconfig

# Different target kernel
make check-kernel TARGET_KERNEL=5.14.0-600 KUBECONFIG=/path/to/kubeconfig
```

---

## 📝 Complete Workflow Examples

### Example 1: End-to-End Network Testing

```bash
# 1. Build and deploy
make build-push
make deploy KUBECONFIG=/path/to/kubeconfig

# 2. Check deployment
make status KUBECONFIG=/path/to/kubeconfig

# 3. Run automated iperf test
# Terminal 1
make iperf-server KUBECONFIG=/path/to/kubeconfig

# Terminal 2
make iperf-client KUBECONFIG=/path/to/kubeconfig

# 4. Cleanup
make cleanup KUBECONFIG=/path/to/kubeconfig
```

### Example 2: System Performance Analysis

```bash
# 1. Check kernel version
make check-kernel KUBECONFIG=/path/to/kubeconfig

# 2. Deploy perf tools
make build-push-perf
make deploy-perf KUBECONFIG=/path/to/kubeconfig

# 3. Run performance analysis
make perf-top KUBECONFIG=/path/to/kubeconfig

# 4. Cleanup
make cleanup-perf KUBECONFIG=/path/to/kubeconfig
```

### Example 3: Stress Testing with tcpdump

```bash
# 1. Deploy network tools
make deploy KUBECONFIG=/path/to/kubeconfig

# 2. Start 500 tcpdump processes
make tcpdump-loop KUBECONFIG=/path/to/kubeconfig

# 3. Verify and monitor
make exec-cat2 KUBECONFIG=/path/to/kubeconfig
# Inside pod:
ps aux | grep tcpdump | wc -l
top -bn1 | head -20

# 4. Run network test while capturing
# (Open another terminal)
make iperf-client KUBECONFIG=/path/to/kubeconfig

# 5. Cleanup
make cleanup KUBECONFIG=/path/to/kubeconfig
```

---

## 🗂️ Directory Structure

```
CNF-20409/
├── Dockerfile.network-tools    # Network tools container definition
├── Dockerfile.perf             # Performance tools container definition
├── Makefile                    # Automation and orchestration (475 lines)
├── deployment.yaml             # Generated network-tools pod manifests
├── perf-deployment.yaml        # Generated perf-tools pod manifest
├── pao.yaml                    # Performance Addon Operator config
├── QUICKSTART.md               # Quick reference guide
└── README.md                   # This file
```

---

## 🔒 Security Considerations

### Privileged Access

**Why Needed**:
- tcpdump requires `NET_RAW` capability for packet capture
- perf requires `SYS_ADMIN`, `SYS_PTRACE`, `PERFMON` for system profiling
- Host network/PID access for comprehensive diagnostics

**Risks**:
- Full access to host network interfaces
- Can capture all network traffic on the node
- Can see and profile all host processes
- Privileged containers can escape to host

### Best Practices

1. **Limit Deployment**:
   - Only deploy in test/dev/troubleshooting scenarios
   - Remove pods after testing
   - Use `make cleanup` and `make cleanup-perf`

2. **Access Control**:
   - Restrict who can deploy privileged pods (RBAC)
   - Monitor pod creation events
   - Audit pod exec sessions

3. **Data Security**:
   - Packet captures may contain sensitive data
   - Use `oc cp` to retrieve files, then delete from pod
   - Store captures securely
   - Consider encryption for captured data

4. **Namespace Isolation**:
   - Use dedicated namespace (`network-tools`)
   - Apply network policies if needed
   - Monitor resource usage

---

## 🐛 Troubleshooting

### Issue: KUBECONFIG not found

**Error**: `Error: KUBECONFIG file not found`

**Solution**:
```bash
# Export kubeconfig
export KUBECONFIG=/path/to/kubeconfig

# Or pass with each command
make deploy KUBECONFIG=/path/to/kubeconfig
```

### Issue: Cannot connect to cluster

**Error**: `Cannot connect to cluster using KUBECONFIG`

**Solution**:
```bash
# Test connection
oc whoami --kubeconfig=/path/to/kubeconfig

# Verify cluster access
oc get nodes --kubeconfig=/path/to/kubeconfig
```

### Issue: Pod fails with SCC error

**Error**: `unable to validate against any security context constraint`

**Solution**:
```bash
# Manually apply SCC
oc adm policy add-scc-to-user privileged -z default -n network-tools

# Or let make do it
make create-namespace KUBECONFIG=/path/to/kubeconfig
```

### Issue: Node not found

**Error**: Pod stuck in `Pending`, events show node selector not matching

**Solution**:
```bash
# List available nodes
oc get nodes -o wide

# Update Makefile or deployment.yaml with correct node names
# Edit lines with nodeSelector: kubernetes.io/hostname
```

### Issue: tcpdump-loop terminated with exit code 137

**Error**: Process killed (OOM)

**Solution**:
- This is expected with 500 processes consuming too much memory
- The Makefile now includes delays to prevent this
- If still occurring, reduce pod count or increase memory limits

### Issue: perf commands fail

**Error**: `perf: Operation not permitted`

**Solution**:
```bash
# Verify pod is privileged
oc get pod perf-pod -n network-tools -o yaml | grep privileged

# Check capabilities
oc get pod perf-pod -n network-tools -o yaml | grep -A 5 capabilities

# Redeploy if needed
make cleanup-perf KUBECONFIG=/path/to/kubeconfig
make deploy-perf KUBECONFIG=/path/to/kubeconfig
```

---

## 📚 Advanced Topics

### Custom Node Selection

Edit generated `deployment.yaml` or `perf-deployment.yaml`:

```yaml
nodeSelector:
  kubernetes.io/hostname: my-custom-node.example.com
```

Or modify Makefile variables before generating.

### Custom Resource Limits

In `deployment.yaml` or `perf-deployment.yaml`:

```yaml
resources:
  limits:
    cpu: "4"
    memory: 4Gi
  requests:
    cpu: "1"
    memory: 1Gi
```

### Persistent Storage for Captures

Replace `emptyDir` with PVC in deployment:

```yaml
volumes:
- name: pcaps
  persistentVolumeClaim:
    claimName: pcaps-pvc
```

---

## 📖 References

- **tcpdump**: https://www.tcpdump.org/manpages/tcpdump.1.html
- **iperf3**: https://iperf.fr/iperf-doc.php
- **perf**: https://perf.wiki.kernel.org/
- **OpenShift SCC**: https://docs.openshift.com/container-platform/latest/authentication/managing-security-context-constraints.html
- **RHEL-88921**: Red Hat kernel bugfix for packet drop issues

---

## 📊 Version History

| Version | Date | Changes |
|---------|------|---------|
| 2.0 | 2025-11-11 | Added perf-tools, KUBECONFIG support, automated testing, kernel checks |
| 1.0 | 2025-11-10 | Initial release with network-tools |

---

## ✅ Status

**Current Status**: ✅ **Production Ready**

**Features**:
- ✅ Network tools container (tcpdump, iperf3)
- ✅ Performance tools container (perf)
- ✅ KUBECONFIG-based deployment (no oc login required)
- ✅ Automated iperf testing with IP detection
- ✅ Mass tcpdump testing (500 processes)
- ✅ CPU performance profiling
- ✅ Kernel version checking
- ✅ Multi-node deployment
- ✅ Privileged pod management

**Tested On**:
- OpenShift 4.x
- RHEL 9.x nodes
- Fedora 40 base images