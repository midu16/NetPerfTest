# Makefile for eBPF Stress Test Program
#
# This component creates eBPF hooks on network interfaces and
# pins maps for the ebpf-exporter to read.
#
# Usage:
#   make build       - Build everything
#   make build-bpf   - Build only eBPF programs
#   make build-go    - Build only Go binary
#   make run         - Build and run
#   make clean       - Clean build artifacts

PROJECT_NAME := ebpf-stress
VERSION := 2.0.0
GO := go
CLANG := clang

# Directories
BPF_DIR := bpf
OUTPUT_DIR := bin
OBJ_DIR := bpf/obj

# Build flags
LDFLAGS := -X main.version=$(VERSION) -X main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_BUILD_FLAGS := -ldflags "$(LDFLAGS)"

# eBPF compilation flags
BPF_CFLAGS := -O2 -g -Wall -Werror
BPF_TARGET := bpf

.PHONY: all build clean test run help

all: build

# Build everything
build: build-bpf build-go
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════╗"
	@echo "║  ebpf-stress Build Complete!                             ║"
	@echo "╚══════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "Usage:"
	@echo "  sudo ./$(OUTPUT_DIR)/$(PROJECT_NAME) --hook xdp --interface lo --loops 1000"
	@echo ""

# Build eBPF programs
build-bpf:
	@echo "Building eBPF programs..."
	@mkdir -p $(OBJ_DIR)
	$(CLANG) $(BPF_CFLAGS) -target $(BPF_TARGET) -c $(BPF_DIR)/stress.c -o $(OBJ_DIR)/stress.o
	@echo "✓ eBPF program compiled"

# Build Go binary
build-go:
	@echo "Building Go binary..."
	@mkdir -p $(OUTPUT_DIR)
	GOCACHE=/tmp/go-cache $(GO) mod tidy
	GOCACHE=/tmp/go-cache $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/$(PROJECT_NAME) ./cmd/ebpf-stress
	@echo "✓ Go binary built: $(OUTPUT_DIR)/$(PROJECT_NAME)"

# Run the program
run: build
	@echo ""
	@echo "Running eBPF stress test (requires root)..."
	sudo $(OUTPUT_DIR)/$(PROJECT_NAME) --hook xdp --interface lo --loops 1000 --verbose

# Run with TC hook
run-tc: build
	sudo $(OUTPUT_DIR)/$(PROJECT_NAME) --hook tc-ingress --interface lo --loops 1000 --verbose

# Install to system
install: build
	@echo "Installing..."
	sudo cp $(OUTPUT_DIR)/$(PROJECT_NAME) /usr/local/bin/
	sudo mkdir -p /usr/share/ebpf-stress
	sudo cp $(OBJ_DIR)/stress.o /usr/share/ebpf-stress/
	@echo "✓ Installed to /usr/local/bin/$(PROJECT_NAME)"

# Test
test:
	$(GO) test -v ./...

# Clean
clean:
	@echo "Cleaning..."
	rm -rf $(OUTPUT_DIR)
	rm -rf $(OBJ_DIR)
	sudo rm -rf /sys/fs/bpf/ebpf-stress 2>/dev/null || true
	$(GO) clean ./...
	@echo "✓ Cleaned"

# Help
help:
	@echo "eBPF Stress Test Program"
	@echo ""
	@echo "Targets:"
	@echo "  build      - Build eBPF programs and Go binary"
	@echo "  build-bpf  - Build only eBPF programs"
	@echo "  build-go   - Build only Go binary"
	@echo "  run        - Build and run with XDP hook"
	@echo "  run-tc     - Build and run with TC hook"
	@echo "  install    - Install to system"
	@echo "  test       - Run tests"
	@echo "  clean      - Clean build artifacts"
