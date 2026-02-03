# Makefile for eBPF Metrics Exporter
#
# This component reads pinned eBPF maps and exports Prometheus metrics.
# It can also load its own eBPF programs to measure kernel stack latency.
#
# Usage:
#   make build      - Build Go binary only (reads existing maps)
#   make build-all  - Build eBPF program and Go binary
#   make run        - Build and run
#   make clean      - Clean build artifacts

PROJECT_NAME := ebpf-exporter
VERSION := 2.1.0
GO := go
CLANG := clang

# Directories
OUTPUT_DIR := bin
BPF_DIR := bpf
BPF_OBJ_DIR := $(BPF_DIR)/obj

# Build flags
LDFLAGS := -X main.version=$(VERSION) -X main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_BUILD_FLAGS := -ldflags "$(LDFLAGS)"
BPF_CFLAGS := -O2 -g -Wall -Werror -target bpf

.PHONY: all build build-all build-bpf clean test run help

all: build

# Build eBPF program for latency measurement
build-bpf:
	@echo "Building eBPF latency measurement program..."
	@mkdir -p $(BPF_OBJ_DIR)
	$(CLANG) $(BPF_CFLAGS) -c $(BPF_DIR)/latency.c -o $(BPF_OBJ_DIR)/latency.o
	@echo "✓ eBPF program compiled: $(BPF_OBJ_DIR)/latency.o"

# Build Go binary only (for reading existing maps)
build:
	@echo "Building eBPF Metrics Exporter..."
	@mkdir -p $(OUTPUT_DIR)
	GOCACHE=/tmp/go-cache $(GO) mod tidy
	GOCACHE=/tmp/go-cache $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/$(PROJECT_NAME) ./cmd/ebpf-exporter
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════╗"
	@echo "║  ebpf-exporter Build Complete!                           ║"
	@echo "╚══════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "Usage:"
	@echo "  ./$(OUTPUT_DIR)/$(PROJECT_NAME) --config ebpf-exporter.yaml"
	@echo ""

# Build everything (eBPF + Go)
build-all: build-bpf build
	@echo "✓ Full build complete (eBPF + Go)"

# Run
run: build
	@echo ""
	@echo "Starting eBPF Metrics Exporter..."
	./$(OUTPUT_DIR)/$(PROJECT_NAME) --verbose

# Run with custom port
run-port: build
	./$(OUTPUT_DIR)/$(PROJECT_NAME) --port 9092 --verbose

# Test
test:
	$(GO) test -v ./...

# Install
install: build
	@echo "Installing..."
	sudo cp $(OUTPUT_DIR)/$(PROJECT_NAME) /usr/local/bin/
	@echo "✓ Installed to /usr/local/bin/$(PROJECT_NAME)"

# Clean
clean:
	@echo "Cleaning..."
	rm -rf $(OUTPUT_DIR)
	$(GO) clean ./...
	@echo "✓ Cleaned"

# Help
help:
	@echo "eBPF Metrics Exporter"
	@echo ""
	@echo "Targets:"
	@echo "  build    - Build the exporter"
	@echo "  run      - Build and run with default settings"
	@echo "  run-port - Build and run on port 9092"
	@echo "  test     - Run tests"
	@echo "  install  - Install to /usr/local/bin"
	@echo "  clean    - Clean build artifacts"
