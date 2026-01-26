# Makefile for eBPF Stress Test Program
#
# This Makefile builds:
# 1. eBPF programs (C -> BPF bytecode)
# 2. Go userspace binary with CLI
#
# Usage:
#   make build       - Build everything
#   make build-bpf   - Build only eBPF programs
#   make build-go    - Build only Go binary
#   make run         - Build and run example
#   make clean       - Clean build artifacts

# Variables
PROJECT_NAME := ebpf-stress
VERSION := 2.0.0
GO := go
CLANG := clang
LLC := llc
ARCH := $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
GOOS := linux
GOARCH := $(ARCH)
KERNEL_VERSION := $(shell uname -r | cut -d. -f1-2)

# Directories
CMD_DIR := cmd/ebpf-stress
PKG_DIR := pkg
BPF_DIR := bpf
OUTPUT_DIR := bin
OBJ_DIR := bpf/obj

# Build flags
LDFLAGS := -X main.version=$(VERSION) -X main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_BUILD_FLAGS := -ldflags "$(LDFLAGS)" -tags netgo

# eBPF compilation flags
# -O2: Optimization for smaller, faster BPF code
# -g: Debug info for better error messages
# -target bpf: Compile for BPF virtual machine
BPF_CFLAGS := -O2 -g -Wall -Werror
BPF_TARGET := bpf

# Note: Using bounded loops for maximum kernel compatibility
# bpf_loop() is available on kernel 5.17+ but has verifier issues on some systems
# Bounded loops work reliably on kernel 5.4+

.PHONY: all build clean test lint fmt vet run help install check-deps

all: check-deps clean fmt lint vet build

# Check dependencies
check-deps:
	@echo "Checking dependencies..."
	@which clang > /dev/null || (echo "Error: clang not found. Install with: dnf install clang" && exit 1)
	@which go > /dev/null || (echo "Error: go not found. Install Go 1.21+" && exit 1)
	@echo "✓ Dependencies OK"
	@echo "  Kernel version: $(KERNEL_VERSION)"
	@echo "  Loop method: bounded loops (verifier-friendly, max 10,000 iterations)"

# Build everything
build: build-bpf build-go
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════╗"
	@echo "║  Build Complete!                                        ║"
	@echo "╚══════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "Binary: $(OUTPUT_DIR)/$(PROJECT_NAME)"
	@echo ""
	@echo "Usage examples:"
	@echo "  sudo ./$(OUTPUT_DIR)/$(PROJECT_NAME) --hook xdp --interface eth0 --loops 10000"
	@echo "  sudo ./$(OUTPUT_DIR)/$(PROJECT_NAME) --hook tc-ingress --interface eth0 --loops 5000 --metrics"
	@echo ""

# Build eBPF programs
build-bpf:
	@echo "Building eBPF programs..."
	@mkdir -p $(OBJ_DIR)
	@echo "  Compiling stress.c (unified program)..."
	$(CLANG) $(BPF_CFLAGS) -target $(BPF_TARGET) -c $(BPF_DIR)/stress.c -o $(OBJ_DIR)/stress.o
	@echo "  Compiling xdp.c (legacy)..."
	$(CLANG) $(BPF_CFLAGS) -target $(BPF_TARGET) -c $(BPF_DIR)/xdp.c -o $(OBJ_DIR)/xdp.o 2>/dev/null || true
	@echo "  Compiling tc.c (legacy)..."
	$(CLANG) $(BPF_CFLAGS) -target $(BPF_TARGET) -c $(BPF_DIR)/tc.c -o $(OBJ_DIR)/tc.o 2>/dev/null || true
	@echo "  Compiling socket.c (legacy)..."
	$(CLANG) $(BPF_CFLAGS) -target $(BPF_TARGET) -c $(BPF_DIR)/socket.c -o $(OBJ_DIR)/socket.o 2>/dev/null || true
	@echo "✓ eBPF programs compiled"

# Build Go binaries
build-go:
	@echo "Building Go binaries..."
	@mkdir -p $(OUTPUT_DIR)
	$(GO) mod tidy
	@echo "  Building ebpf-stress..."
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/$(PROJECT_NAME) ./$(CMD_DIR)
	@echo "  Building traffic-gen..."
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/traffic-gen ./cmd/traffic-gen 2>/dev/null || echo "    (traffic-gen skipped)"
	@echo "  Building afpacket-stress..."
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/afpacket-stress ./cmd/afpacket-stress 2>/dev/null || echo "    (afpacket-stress skipped)"
	@chmod +x test-local.sh 2>/dev/null || true
	@echo "✓ Go binaries built: $(OUTPUT_DIR)/"

# Run the program (example with XDP hook)
run: build
	@echo ""
	@echo "Running eBPF stress test..."
	@echo "NOTE: Requires root privileges"
	@echo ""
	sudo $(OUTPUT_DIR)/$(PROJECT_NAME) --hook xdp --interface eth0 --loops 1000 --verbose

# Run with TC hook
run-tc: build
	sudo $(OUTPUT_DIR)/$(PROJECT_NAME) --hook tc-ingress --interface eth0 --loops 1000 --verbose

# Run with metrics
run-metrics: build
	sudo $(OUTPUT_DIR)/$(PROJECT_NAME) --hook xdp --interface eth0 --loops 1000 --metrics --metrics-port 9091

# Run AF_PACKET stress test (combined high PPS + 200 sockets)
run-afpacket: build
	@echo ""
	@echo "Running AF_PACKET socket stress test..."
	@echo "This combines HIGH PPS traffic with 200+ AF_PACKET sockets"
	@echo ""
	sudo $(OUTPUT_DIR)/afpacket-stress --sockets 200 --pps 500000 --duration 120s --progressive

# Run AF_PACKET stress test with maximum settings
run-afpacket-max: build
	@echo ""
	@echo "Running AF_PACKET socket stress test (MAXIMUM MODE)..."
	@echo "This uses maximum PPS + 500 AF_PACKET sockets"
	@echo ""
	sudo $(OUTPUT_DIR)/afpacket-stress --sockets 500 --max --duration 180s --progressive --socket-batch 100

# Install to system
install: build
	@echo "Installing to system..."
	sudo cp $(OUTPUT_DIR)/$(PROJECT_NAME) /usr/local/bin/
	sudo setcap cap_bpf,cap_net_admin+ep /usr/local/bin/$(PROJECT_NAME) 2>/dev/null || \
		echo "Warning: setcap failed, program will need root privileges"
	@echo "✓ Installed to /usr/local/bin/$(PROJECT_NAME)"

# Run tests
test:
	@echo "Running tests..."
	$(GO) test -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report: coverage.html"

# Run integration tests (requires root)
test-integration:
	@echo "Running integration tests (requires root)..."
	sudo $(GO) test -tags=integration -v ./...

# Lint code
lint:
	@echo "Linting..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, skipping..."; \
	fi

# Format code
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...
	@echo "✓ Code formatted"

# Vet code
vet:
	@echo "Vetting code..."
	$(GO) vet ./...
	@echo "✓ Code vetted"

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf $(OUTPUT_DIR)
	rm -rf $(OBJ_DIR)
	rm -f coverage.out coverage.html
	$(GO) clean ./...
	@echo "✓ Cleaned"

# Generate eBPF bindings (if using bpf2go)
generate:
	@echo "Generating eBPF bindings..."
	$(GO) generate ./...
	@echo "✓ Bindings generated"

# Docker build
docker-build:
	@echo "Building Docker image..."
	docker build -t $(PROJECT_NAME):$(VERSION) .
	docker tag $(PROJECT_NAME):$(VERSION) $(PROJECT_NAME):latest
	@echo "✓ Docker image built"

# Docker run
docker-run: docker-build
	docker run --rm --privileged --network host \
		-v /sys/fs/bpf:/sys/fs/bpf \
		$(PROJECT_NAME):latest \
		--hook xdp --interface eth0 --loops 1000

# Push to registry
docker-push:
	@echo "Pushing Docker image..."
	docker push $(PROJECT_NAME):$(VERSION)
	docker push $(PROJECT_NAME):latest

# Show BPF program info
bpf-info:
	@echo "eBPF Program Information:"
	@echo ""
	@for obj in $(OBJ_DIR)/*.o; do \
		echo "=== $$obj ==="; \
		llvm-objdump -S $$obj 2>/dev/null | head -50 || echo "(llvm-objdump not available)"; \
		echo ""; \
	done

# Verify BPF programs can be loaded
bpf-verify:
	@echo "Verifying eBPF programs..."
	@for obj in $(OBJ_DIR)/*.o; do \
		echo "Checking $$obj..."; \
		sudo bpftool prog load $$obj /sys/fs/bpf/test_prog 2>&1 || echo "  (verification may require kernel support)"; \
		sudo rm -f /sys/fs/bpf/test_prog 2>/dev/null; \
	done

# Help
help:
	@echo "╔══════════════════════════════════════════════════════════════════╗"
	@echo "║  eBPF Stress Test Program - Makefile                            ║"
	@echo "╚══════════════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "Build targets:"
	@echo "  build          - Build eBPF programs and Go binaries"
	@echo "  build-bpf      - Build only eBPF programs"
	@echo "  build-go       - Build only Go binaries"
	@echo ""
	@echo "Run targets:"
	@echo "  run            - Build and run example (XDP hook)"
	@echo "  run-tc         - Build and run with TC hook"
	@echo "  run-metrics    - Build and run with Prometheus metrics"
	@echo "  run-afpacket   - Run AF_PACKET stress (200 sockets + 500K PPS)"
	@echo "  run-afpacket-max - Run AF_PACKET stress (500 sockets + MAX PPS)"
	@echo ""
	@echo "Install targets:"
	@echo "  install        - Install to /usr/local/bin"
	@echo ""
	@echo "Test targets:"
	@echo "  test           - Run tests"
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  test-integration - Run integration tests (requires root)"
	@echo ""
	@echo "Development targets:"
	@echo "  lint           - Lint code"
	@echo "  fmt            - Format code"
	@echo "  vet            - Vet code"
	@echo "  clean          - Clean build artifacts"
	@echo "  generate       - Generate eBPF bindings"
	@echo ""
	@echo "Docker targets:"
	@echo "  docker-build   - Build Docker image"
	@echo "  docker-run     - Run in Docker"
	@echo "  docker-push    - Push to registry"
	@echo ""
	@echo "Debug targets:"
	@echo "  bpf-info       - Show BPF program info"
	@echo "  bpf-verify     - Verify BPF programs can be loaded"
	@echo "  check-deps     - Check build dependencies"
	@echo ""
	@echo "Environment:"
	@echo "  Kernel version:    $(KERNEL_VERSION)"
	@echo "  Max loops:         10,000 (bounded loops for verifier compatibility)"
	@echo ""
