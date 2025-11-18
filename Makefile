# CNF-20409 - Network Tools Container Build
# Builds Fedora-based OCI image with tcpdump and iperf3

# Image configuration
IMAGE_REGISTRY ?= quay.io
IMAGE_NAMESPACE ?= midu
IMAGE_NAME ?= network-tools
IMAGE_TAG ?= latest
IMAGE_FULL = $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$(IMAGE_NAME):$(IMAGE_TAG)

# Kubernetes configuration
KUBECONFIG ?= $(HOME)/.kube/config
NAMESPACE ?= network-tools

# Dockerfiles
DOCKERFILE = Dockerfile.network-tools
DOCKERFILE_PERF = Dockerfile.perf

# Perf image configuration
PERF_IMAGE_NAME ?= perf-tools
PERF_IMAGE_TAG ?= latest
PERF_IMAGE_FULL = $(IMAGE_REGISTRY)/$(IMAGE_NAMESPACE)/$(PERF_IMAGE_NAME):$(PERF_IMAGE_TAG)

# Kernel version configuration
TARGET_KERNEL ?= 5.14.0-586
NODE_NAME ?= hub-ctlplane-0.5g-deployment.lab

.PHONY: help
help:
	@printf "\n╔════════════════════════════════════════════════════════════════╗\n"
	@printf "║                                                                ║\n"
	@printf "║          CNF-20409 - Network Tools Container                  ║\n"
	@printf "║                                                                ║\n"
	@printf "╚════════════════════════════════════════════════════════════════╝\n\n"
	@printf "\033[1mBuild Targets:\033[0m\n"
	@printf "  \033[32mbuild\033[0m              Build network-tools container image\n"
	@printf "  \033[32mpush\033[0m               Push network-tools image to registry\n"
	@printf "  \033[32mbuild-push\033[0m         Build and push network-tools in one step\n"
	@printf "  \033[32mtest\033[0m               Test network-tools image locally\n"
	@printf "  \033[32mbuild-perf\033[0m         Build perf-tools container image\n"
	@printf "  \033[32mpush-perf\033[0m          Push perf-tools image to registry\n"
	@printf "  \033[32mbuild-push-perf\033[0m    Build and push perf-tools in one step\n\n"
	@printf "\033[1mDeployment Targets:\033[0m\n"
	@printf "  \033[32mcreate-namespace\033[0m   Create namespace\n"
	@printf "  \033[32mdeploy\033[0m             Deploy all 3 pods\n"
	@printf "  \033[32mstatus\033[0m             Show pod status\n"
	@printf "  \033[32mlogs-cat1\033[0m          Show cat-1 logs\n"
	@printf "  \033[32mlogs-cat2\033[0m          Show cat-2 logs\n"
	@printf "  \033[32mlogs-cat3\033[0m          Show cat-3 logs\n"
	@printf "  \033[32mexec-cat1\033[0m          Shell into cat-1\n"
	@printf "  \033[32mexec-cat2\033[0m          Shell into cat-2\n"
	@printf "  \033[32mexec-cat3\033[0m          Shell into cat-3\n"
	@printf "  \033[32mcleanup\033[0m            Delete all pods\n\n"
	@printf "\033[1mNetwork Testing Targets:\033[0m\n"
	@printf "  \033[32miperf-server\033[0m       Run iperf3 server in cat-3\n"
	@printf "  \033[32miperf-client\033[0m       Run iperf3 client from cat-1 to cat-3\n"
	@printf "  \033[32mtcpdump-loop\033[0m       Start 500 tcpdump processes in cat-2\n\n"
	@printf "\033[1mPerformance Analysis Targets:\033[0m\n"
	@printf "  \033[32mdeploy-perf\033[0m        Deploy perf-tools pod\n"
	@printf "  \033[32mperf-top\033[0m           Run perf top on specific CPUs\n"
	@printf "  \033[32mcleanup-perf\033[0m       Delete perf-tools pod\n\n"
	@printf "\033[1mSystem Check Targets:\033[0m\n"
	@printf "  \033[32mcheck-kernel\033[0m       Check node kernel version\n\n"
	@printf "\033[1mConfiguration:\033[0m\n"
	@printf "  IMAGE_REGISTRY=\033[33m$(IMAGE_REGISTRY)\033[0m\n"
	@printf "  IMAGE_NAMESPACE=\033[33m$(IMAGE_NAMESPACE)\033[0m\n"
	@printf "  IMAGE_NAME=\033[33m$(IMAGE_NAME)\033[0m\n"
	@printf "  IMAGE_TAG=\033[33m$(IMAGE_TAG)\033[0m\n"
	@printf "  IMAGE_FULL=\033[33m$(IMAGE_FULL)\033[0m\n"
	@printf "  NAMESPACE=\033[33m$(NAMESPACE)\033[0m\n"
	@printf "  KUBECONFIG=\033[33m$(KUBECONFIG)\033[0m\n\n"
	@printf "\033[1mExample Usage:\033[0m\n"
	@printf "  \033[36mmake build-push\033[0m\n"
	@printf "  \033[36mmake deploy KUBECONFIG=/path/to/kubeconfig\033[0m\n"
	@printf "  \033[36mmake status KUBECONFIG=/path/to/kubeconfig\033[0m\n"
	@printf "  \033[36mmake tcpdump-loop KUBECONFIG=/path/to/kubeconfig\033[0m\n"
	@printf "  \033[36mmake iperf-server KUBECONFIG=/path/to/kubeconfig\033[0m  (Terminal 1)\n"
	@printf "  \033[36mmake iperf-client KUBECONFIG=/path/to/kubeconfig\033[0m  (Terminal 2)\n"
	@printf "  \033[36mmake build-push-perf\033[0m\n"
	@printf "  \033[36mmake deploy-perf KUBECONFIG=/path/to/kubeconfig\033[0m\n"
	@printf "  \033[36mmake perf-top KUBECONFIG=/path/to/kubeconfig\033[0m\n\n"

.PHONY: check-kubeconfig
check-kubeconfig:
	@if [ -z "$(KUBECONFIG)" ]; then \
		printf "\033[31mError: KUBECONFIG not set. Use: make <target> KUBECONFIG=/path/to/kubeconfig\033[0m\n"; \
		exit 1; \
	fi
	@if [ ! -f "$(KUBECONFIG)" ]; then \
		printf "\033[31mError: KUBECONFIG file not found: $(KUBECONFIG)\033[0m\n"; \
		exit 1; \
	fi
	@export KUBECONFIG=$(KUBECONFIG) && \
	if ! oc cluster-info &> /dev/null; then \
		printf "\033[31mError: Cannot connect to cluster using KUBECONFIG=$(KUBECONFIG)\033[0m\n"; \
		exit 1; \
	fi
	@printf "\033[32m✓ Connected to cluster using KUBECONFIG=$(KUBECONFIG)\033[0m\n"

.PHONY: build
build:
	@printf "\033[1m🔨 Building container image...\033[0m\n"
	podman build -f $(DOCKERFILE) -t $(IMAGE_FULL) .
	@printf "\033[32m✓ Build complete: $(IMAGE_FULL)\033[0m\n"

.PHONY: push
push:
	@printf "\033[1m📤 Pushing image to registry...\033[0m\n"
	podman push $(IMAGE_FULL)
	@printf "\033[32m✓ Push complete: $(IMAGE_FULL)\033[0m\n"

.PHONY: build-push
build-push: build push
	@printf "\033[32m✓ Build and push complete!\033[0m\n"

.PHONY: test
test:
	@printf "\033[1m🧪 Testing image locally...\033[0m\n"
	podman run --rm $(IMAGE_FULL) tcpdump --version
	podman run --rm $(IMAGE_FULL) iperf3 --version
	@printf "\033[32m✓ Image test passed!\033[0m\n"

.PHONY: build-perf
build-perf:
	@printf "\033[1m🔨 Building perf-tools container image...\033[0m\n"
	podman build -f $(DOCKERFILE_PERF) -t $(PERF_IMAGE_FULL) .
	@printf "\033[32m✓ Build complete: $(PERF_IMAGE_FULL)\033[0m\n"

.PHONY: push-perf
push-perf:
	@printf "\033[1m📤 Pushing perf-tools image to registry...\033[0m\n"
	podman push $(PERF_IMAGE_FULL)
	@printf "\033[32m✓ Push complete: $(PERF_IMAGE_FULL)\033[0m\n"

.PHONY: build-push-perf
build-push-perf: build-perf push-perf
	@printf "\033[32m✓ Build and push complete for perf-tools!\033[0m\n"

.PHONY: create-namespace
create-namespace: check-kubeconfig
	@printf "\033[1m📦 Creating namespace...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	if oc get namespace $(NAMESPACE) &> /dev/null; then \
		printf "\033[33m⚠ Namespace $(NAMESPACE) already exists\033[0m\n"; \
	else \
		oc create namespace $(NAMESPACE) && \
		printf "\033[32m✓ Namespace $(NAMESPACE) created\033[0m\n"; \
	fi
	@printf "\033[1m🔐 Applying privileged SCC...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc adm policy add-scc-to-user privileged -z default -n $(NAMESPACE) || true
	@printf "\033[32m✓ SCC applied\033[0m\n"

.PHONY: generate-deployment
generate-deployment:
	@printf "\033[1m📝 Generating deployment YAML...\033[0m\n"
	@printf -- "---\n" > deployment.yaml
	@printf "apiVersion: v1\n" >> deployment.yaml
	@printf "kind: Pod\n" >> deployment.yaml
	@printf "metadata:\n" >> deployment.yaml
	@printf "  name: cat-1\n" >> deployment.yaml
	@printf "  namespace: %s\n" "$(NAMESPACE)" >> deployment.yaml
	@printf "  labels:\n" >> deployment.yaml
	@printf "    app: network-tools\n" >> deployment.yaml
	@printf "    instance: cat-1\n" >> deployment.yaml
	@printf "spec:\n" >> deployment.yaml
	@printf "  hostNetwork: false\n" >> deployment.yaml
	@printf "  hostPID: true\n" >> deployment.yaml
	@printf "  nodeSelector:\n" >> deployment.yaml
	@printf "    kubernetes.io/hostname: hub-ctlplane-0.5g-deployment.lab\n" >> deployment.yaml
	@printf "  containers:\n" >> deployment.yaml
	@printf "  - name: network-tools\n" >> deployment.yaml
	@printf "    image: %s\n" "$(IMAGE_FULL)" >> deployment.yaml
	@printf "    imagePullPolicy: Always\n" >> deployment.yaml
	@printf "    securityContext:\n" >> deployment.yaml
	@printf "      privileged: true\n" >> deployment.yaml
	@printf "      capabilities:\n" >> deployment.yaml
	@printf "        add:\n" >> deployment.yaml
	@printf "        - NET_ADMIN\n" >> deployment.yaml
	@printf "        - NET_RAW\n" >> deployment.yaml
	@printf "        - SYS_ADMIN\n" >> deployment.yaml
	@printf "    resources:\n" >> deployment.yaml
	@printf "      limits:\n" >> deployment.yaml
	@printf "        cpu: \"2\"\n" >> deployment.yaml
	@printf "        memory: 2Gi\n" >> deployment.yaml
	@printf "      requests:\n" >> deployment.yaml
	@printf "        cpu: \"500m\"\n" >> deployment.yaml
	@printf "        memory: 512Mi\n" >> deployment.yaml
	@printf "    volumeMounts:\n" >> deployment.yaml
	@printf "    - name: pcaps\n" >> deployment.yaml
	@printf "      mountPath: /data/pcaps\n" >> deployment.yaml
	@printf "  volumes:\n" >> deployment.yaml
	@printf "  - name: pcaps\n" >> deployment.yaml
	@printf "    emptyDir: {}\n" >> deployment.yaml
	@printf "  restartPolicy: Always\n" >> deployment.yaml
	@printf -- "---\n" >> deployment.yaml
	@printf "apiVersion: v1\n" >> deployment.yaml
	@printf "kind: Pod\n" >> deployment.yaml
	@printf "metadata:\n" >> deployment.yaml
	@printf "  name: cat-2\n" >> deployment.yaml
	@printf "  namespace: %s\n" "$(NAMESPACE)" >> deployment.yaml
	@printf "  labels:\n" >> deployment.yaml
	@printf "    app: network-tools\n" >> deployment.yaml
	@printf "    instance: cat-2\n" >> deployment.yaml
	@printf "spec:\n" >> deployment.yaml
	@printf "  hostNetwork: false\n" >> deployment.yaml
	@printf "  hostPID: true\n" >> deployment.yaml
	@printf "  nodeSelector:\n" >> deployment.yaml
	@printf "    kubernetes.io/hostname: hub-ctlplane-0.5g-deployment.lab\n" >> deployment.yaml
	@printf "  containers:\n" >> deployment.yaml
	@printf "  - name: network-tools\n" >> deployment.yaml
	@printf "    image: %s\n" "$(IMAGE_FULL)" >> deployment.yaml
	@printf "    imagePullPolicy: Always\n" >> deployment.yaml
	@printf "    securityContext:\n" >> deployment.yaml
	@printf "      privileged: true\n" >> deployment.yaml
	@printf "      capabilities:\n" >> deployment.yaml
	@printf "        add:\n" >> deployment.yaml
	@printf "        - NET_ADMIN\n" >> deployment.yaml
	@printf "        - NET_RAW\n" >> deployment.yaml
	@printf "        - SYS_ADMIN\n" >> deployment.yaml
	@printf "    resources:\n" >> deployment.yaml
	@printf "      limits:\n" >> deployment.yaml
	@printf "        cpu: \"2\"\n" >> deployment.yaml
	@printf "        memory: 2Gi\n" >> deployment.yaml
	@printf "      requests:\n" >> deployment.yaml
	@printf "        cpu: \"500m\"\n" >> deployment.yaml
	@printf "        memory: 512Mi\n" >> deployment.yaml
	@printf "    volumeMounts:\n" >> deployment.yaml
	@printf "    - name: pcaps\n" >> deployment.yaml
	@printf "      mountPath: /data/pcaps\n" >> deployment.yaml
	@printf "  volumes:\n" >> deployment.yaml
	@printf "  - name: pcaps\n" >> deployment.yaml
	@printf "    emptyDir: {}\n" >> deployment.yaml
	@printf "  restartPolicy: Always\n" >> deployment.yaml
	@printf -- "---\n" >> deployment.yaml
	@printf "apiVersion: v1\n" >> deployment.yaml
	@printf "kind: Pod\n" >> deployment.yaml
	@printf "metadata:\n" >> deployment.yaml
	@printf "  name: cat-3\n" >> deployment.yaml
	@printf "  namespace: %s\n" "$(NAMESPACE)" >> deployment.yaml
	@printf "  labels:\n" >> deployment.yaml
	@printf "    app: network-tools\n" >> deployment.yaml
	@printf "    instance: cat-3\n" >> deployment.yaml
	@printf "spec:\n" >> deployment.yaml
	@printf "  hostNetwork: false\n" >> deployment.yaml
	@printf "  hostPID: true\n" >> deployment.yaml
	@printf "  nodeSelector:\n" >> deployment.yaml
	@printf "    kubernetes.io/hostname: hub-ctlplane-2.5g-deployment.lab\n" >> deployment.yaml
	@printf "  containers:\n" >> deployment.yaml
	@printf "  - name: network-tools\n" >> deployment.yaml
	@printf "    image: %s\n" "$(IMAGE_FULL)" >> deployment.yaml
	@printf "    imagePullPolicy: Always\n" >> deployment.yaml
	@printf "    securityContext:\n" >> deployment.yaml
	@printf "      privileged: true\n" >> deployment.yaml
	@printf "      capabilities:\n" >> deployment.yaml
	@printf "        add:\n" >> deployment.yaml
	@printf "        - NET_ADMIN\n" >> deployment.yaml
	@printf "        - NET_RAW\n" >> deployment.yaml
	@printf "        - SYS_ADMIN\n" >> deployment.yaml
	@printf "    resources:\n" >> deployment.yaml
	@printf "      limits:\n" >> deployment.yaml
	@printf "        cpu: \"2\"\n" >> deployment.yaml
	@printf "        memory: 2Gi\n" >> deployment.yaml
	@printf "      requests:\n" >> deployment.yaml
	@printf "        cpu: \"500m\"\n" >> deployment.yaml
	@printf "        memory: 512Mi\n" >> deployment.yaml
	@printf "    volumeMounts:\n" >> deployment.yaml
	@printf "    - name: pcaps\n" >> deployment.yaml
	@printf "      mountPath: /data/pcaps\n" >> deployment.yaml
	@printf "  volumes:\n" >> deployment.yaml
	@printf "  - name: pcaps\n" >> deployment.yaml
	@printf "    emptyDir: {}\n" >> deployment.yaml
	@printf "  restartPolicy: Always\n" >> deployment.yaml
	@printf "\033[32m✓ Deployment YAML generated\033[0m\n"

.PHONY: deploy
deploy: check-kubeconfig create-namespace generate-deployment
	@printf "\033[1m🚀 Deploying pods...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc delete pod cat-1 cat-2 cat-3 -n $(NAMESPACE) --ignore-not-found=true && \
	sleep 2 && \
	oc apply -f deployment.yaml
	@printf "\033[32m✓ Pods deployed\033[0m\n"
	@printf "\033[33m⏳ Waiting for pods to be ready...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc wait --for=condition=Ready pod/cat-1 pod/cat-2 pod/cat-3 -n $(NAMESPACE) --timeout=60s || true
	@printf "\033[1m📊 Pod status:\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc get pods -n $(NAMESPACE) -o wide

.PHONY: status
status: check-kubeconfig
	@printf "\033[1m📊 Pod Status:\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc get pods -n $(NAMESPACE) -o wide

.PHONY: logs-cat1
logs-cat1: check-kubeconfig
	@printf "\033[1m📋 cat-1 Logs:\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc logs -f cat-1 -n $(NAMESPACE)

.PHONY: logs-cat2
logs-cat2: check-kubeconfig
	@printf "\033[1m📋 cat-2 Logs:\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc logs -f cat-2 -n $(NAMESPACE)

.PHONY: logs-cat3
logs-cat3: check-kubeconfig
	@printf "\033[1m📋 cat-3 Logs:\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc logs -f cat-3 -n $(NAMESPACE)

.PHONY: exec-cat1
exec-cat1: check-kubeconfig
	@printf "\033[1m🔧 Exec into cat-1:\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc exec -it cat-1 -n $(NAMESPACE) -- /bin/bash

.PHONY: exec-cat2
exec-cat2: check-kubeconfig
	@printf "\033[1m🔧 Exec into cat-2:\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc exec -it cat-2 -n $(NAMESPACE) -- /bin/bash

.PHONY: exec-cat3
exec-cat3: check-kubeconfig
	@printf "\033[1m🔧 Exec into cat-3:\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc exec -it cat-3 -n $(NAMESPACE) -- /bin/bash

.PHONY: iperf-server
iperf-server: check-kubeconfig
	@printf "\033[1m📡 Starting iperf3 server in cat-3...\033[0m\n"
	@printf "\033[33m⚠  Press Ctrl+C to stop the server\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc exec -it cat-3 -n $(NAMESPACE) -- iperf3 -s

.PHONY: iperf-client
iperf-client: check-kubeconfig
	@printf "\033[1m📊 Running iperf3 client test from cat-1 to cat-3...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	CAT3_IP=$$(oc get pod cat-3 -n $(NAMESPACE) -o jsonpath='{.status.podIP}') && \
	if [ -z "$$CAT3_IP" ]; then \
		printf "\033[31mError: Could not get cat-3 pod IP. Is the pod running?\033[0m\n"; \
		exit 1; \
	fi && \
	printf "\033[33m📍 cat-3 IP: $$CAT3_IP\033[0m\n" && \
	printf "\033[33m⏱️  Test duration: 600 seconds (10 minutes)\033[0m\n" && \
	printf "\033[33m📈 Reporting interval: 5 seconds\033[0m\n\n" && \
	oc exec -it cat-1 -n $(NAMESPACE) -- iperf3 -c $$CAT3_IP -i 5 -t 600

.PHONY: tcpdump-loop
tcpdump-loop: check-kubeconfig
	@printf "\033[1m📦 Starting 500 tcpdump processes in cat-2...\033[0m\n"
	@printf "\033[33m⚠  This will create 500 background tcpdump processes!\033[0m\n"
	@printf "\033[33m📂 Capture files will be named: toto1, toto2, ..., toto500\033[0m\n"
	@printf "\033[33m💾 Location: /data/pcaps/ in the pod\033[0m\n"
	@printf "\033[33m⏱️  Adding small delays to avoid resource exhaustion...\033[0m\n\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc exec cat-2 -n $(NAMESPACE) -- bash -c 'for index in $$(seq 1 500); do (tcpdump -qni any -w /data/pcaps/toto$$index 2>/dev/null &); [ $$((index % 50)) -eq 0 ] && sleep 1; done; echo "All processes spawned"'
	@printf "\n\033[32m✓ Started 500 tcpdump processes in cat-2\033[0m\n"
	@printf "\033[33m💡 Tip: Use 'make exec-cat2' to check processes with 'ps aux | grep tcpdump | wc -l'\033[0m\n"

.PHONY: check-kernel
check-kernel: check-kubeconfig
	@printf "\033[1m🔍 Checking kernel version on node: $(NODE_NAME)...\033[0m\n"
	@printf "\033[33m📊 Target kernel version: $(TARGET_KERNEL)\033[0m\n\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	FULL_KERNEL=$$(oc debug node/$(NODE_NAME) -- chroot /host uname -r 2>/dev/null | grep -v "Starting pod" | grep -v "To use host" | tail -1) && \
	CURRENT_KERNEL=$$(echo $$FULL_KERNEL | sed -E 's/^([0-9]+\.[0-9]+\.[0-9]+-[0-9]+).*/\1/') && \
	printf "\033[36m📋 Full kernel version: $$FULL_KERNEL\033[0m\n" && \
	printf "\033[36m🔢 Extracted version: $$CURRENT_KERNEL\033[0m\n" && \
	printf "\033[36m🎯 Target version:    $(TARGET_KERNEL)\033[0m\n\n" && \
	if [ "$$CURRENT_KERNEL" = "$(TARGET_KERNEL)" ]; then \
		printf "\033[32m✓ Kernel versions MATCH!\033[0m\n"; \
	else \
		CURRENT_BUILD=$$(echo $$CURRENT_KERNEL | cut -d'-' -f2) && \
		TARGET_BUILD=$$(echo $(TARGET_KERNEL) | cut -d'-' -f2) && \
		if [ $$CURRENT_BUILD -lt $$TARGET_BUILD ]; then \
			printf "\033[31m✗ Current kernel ($$CURRENT_KERNEL) is OLDER than target ($(TARGET_KERNEL))\033[0m\n"; \
			printf "\033[33m⚠  Kernel fix within RHEL-88921 its not available\033[0m\n"; \
		else \
			printf "\033[33m⚠ Current kernel ($$CURRENT_KERNEL) is NEWER than target ($(TARGET_KERNEL))\033[0m\n"; \
		fi; \
	fi

.PHONY: generate-perf-deployment
generate-perf-deployment:
	@printf "\033[1m📝 Generating perf pod deployment YAML...\033[0m\n"
	@printf -- "---\n" > perf-deployment.yaml
	@printf "apiVersion: v1\n" >> perf-deployment.yaml
	@printf "kind: Pod\n" >> perf-deployment.yaml
	@printf "metadata:\n" >> perf-deployment.yaml
	@printf "  name: perf-pod\n" >> perf-deployment.yaml
	@printf "  namespace: %s\n" "$(NAMESPACE)" >> perf-deployment.yaml
	@printf "  labels:\n" >> perf-deployment.yaml
	@printf "    app: perf-tools\n" >> perf-deployment.yaml
	@printf "spec:\n" >> perf-deployment.yaml
	@printf "  hostNetwork: true\n" >> perf-deployment.yaml
	@printf "  hostPID: true\n" >> perf-deployment.yaml
	@printf "  nodeSelector:\n" >> perf-deployment.yaml
	@printf "    kubernetes.io/hostname: hub-ctlplane-0.5g-deployment.lab\n" >> perf-deployment.yaml
	@printf "  containers:\n" >> perf-deployment.yaml
	@printf "  - name: perf-tools\n" >> perf-deployment.yaml
	@printf "    image: %s\n" "$(PERF_IMAGE_FULL)" >> perf-deployment.yaml
	@printf "    imagePullPolicy: Always\n" >> perf-deployment.yaml
	@printf "    securityContext:\n" >> perf-deployment.yaml
	@printf "      privileged: true\n" >> perf-deployment.yaml
	@printf "      capabilities:\n" >> perf-deployment.yaml
	@printf "        add:\n" >> perf-deployment.yaml
	@printf "        - SYS_ADMIN\n" >> perf-deployment.yaml
	@printf "        - SYS_PTRACE\n" >> perf-deployment.yaml
	@printf "        - PERFMON\n" >> perf-deployment.yaml
	@printf "    resources:\n" >> perf-deployment.yaml
	@printf "      limits:\n" >> perf-deployment.yaml
	@printf "        cpu: \"2\"\n" >> perf-deployment.yaml
	@printf "        memory: 2Gi\n" >> perf-deployment.yaml
	@printf "      requests:\n" >> perf-deployment.yaml
	@printf "        cpu: \"500m\"\n" >> perf-deployment.yaml
	@printf "        memory: 512Mi\n" >> perf-deployment.yaml
	@printf "  restartPolicy: Always\n" >> perf-deployment.yaml
	@printf "\033[32m✓ Perf deployment YAML generated\033[0m\n"

.PHONY: deploy-perf
deploy-perf: check-kubeconfig create-namespace generate-perf-deployment
	@printf "\033[1m🚀 Deploying perf-tools pod...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc delete pod perf-pod -n $(NAMESPACE) --ignore-not-found=true && \
	sleep 2 && \
	oc apply -f perf-deployment.yaml
	@printf "\033[32m✓ Perf pod deployed\033[0m\n"
	@printf "\033[33m⏳ Waiting for perf pod to be ready...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc wait --for=condition=Ready pod/perf-pod -n $(NAMESPACE) --timeout=60s || true
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc get pod perf-pod -n $(NAMESPACE) -o wide

.PHONY: perf-top
perf-top: check-kubeconfig
	@printf "\033[1m📊 Running perf top on CPUs 0,1,20,21...\033[0m\n"
	@printf "\033[33m⚠  Press Ctrl+C to stop perf top\033[0m\n"
	@printf "\033[33m📍 Command: perf top -C 0,1,20,21 -z\033[0m\n\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc exec -it perf-pod -n $(NAMESPACE) -- perf top -C 0,1,20,21 -z

.PHONY: cleanup-perf
cleanup-perf: check-kubeconfig
	@printf "\033[1m🧹 Cleaning up perf-tools pod...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc delete pod perf-pod -n $(NAMESPACE) --ignore-not-found=true
	@printf "\033[32m✓ Perf pod cleanup complete\033[0m\n"

.PHONY: cleanup
cleanup: check-kubeconfig
	@printf "\033[1m🧹 Cleaning up resources...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc delete pod cat-1 cat-2 cat-3 -n $(NAMESPACE) --ignore-not-found=true
	@printf "\033[32m✓ Cleanup complete\033[0m\n"

.PHONY: cleanup-namespace
cleanup-namespace: cleanup check-kubeconfig
	@printf "\033[1m🧹 Deleting namespace...\033[0m\n"
	@export KUBECONFIG=$(KUBECONFIG) && \
	oc delete namespace $(NAMESPACE) --ignore-not-found=true
	@printf "\033[32m✓ Namespace deleted\033[0m\n"

.PHONY: clean
clean:
	@printf "\033[1m🧹 Cleaning generated files...\033[0m\n"
	rm -f deployment.yaml perf-deployment.yaml
	@printf "\033[32m✓ Clean complete\033[0m\n"

