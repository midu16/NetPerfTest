# Makefile for Traffic Generator
#
# This component generates high PPS traffic for stress testing.
#
# Usage:
#   make build         - Build all traffic generators
#   make run           - Run traffic-gen
#   make run-afpacket  - Run afpacket-stress
#   make clean         - Clean build artifacts

PROJECT_NAME := traffic-generator
VERSION := 2.0.0
GO := go

# Directories
OUTPUT_DIR := bin

# Build flags
LDFLAGS := -X main.version=$(VERSION) -X main.buildTime=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_BUILD_FLAGS := -ldflags "$(LDFLAGS)"

.PHONY: all build clean test run run-afpacket help

all: build

# Build all binaries
build:
	@echo "Building Traffic Generator tools..."
	@mkdir -p $(OUTPUT_DIR)
	GOCACHE=/tmp/go-cache $(GO) mod tidy
	@echo "  Building traffic-gen..."
	GOCACHE=/tmp/go-cache $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/traffic-gen ./cmd/traffic-gen
	@echo "  Building afpacket-stress..."
	GOCACHE=/tmp/go-cache $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/afpacket-stress ./cmd/afpacket-stress
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════╗"
	@echo "║  Traffic Generator Build Complete!                       ║"
	@echo "╚══════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "Binaries:"
	@echo "  $(OUTPUT_DIR)/traffic-gen       - High PPS UDP traffic generator"
	@echo "  $(OUTPUT_DIR)/afpacket-stress   - AF_PACKET socket stress tool"
	@echo ""

# Run traffic generator
run: build
	@echo ""
	@echo "Running traffic generator..."
	./$(OUTPUT_DIR)/traffic-gen --pps 100000 --duration 30s

# Run with maximum rate
run-max: build
	./$(OUTPUT_DIR)/traffic-gen --max --duration 60s

# Run AF_PACKET stress
run-afpacket: build
	@echo ""
	@echo "Running AF_PACKET stress test (requires root)..."
	sudo ./$(OUTPUT_DIR)/afpacket-stress --sockets 200 --pps 500000 --duration 120s

# Run AF_PACKET stress with maximum settings
run-afpacket-max: build
	@echo ""
	@echo "Running AF_PACKET stress test with maximum settings..."
	sudo ./$(OUTPUT_DIR)/afpacket-stress --sockets 500 --max --duration 180s --progressive

# Test
test:
	$(GO) test -v ./...

# Install
install: build
	@echo "Installing..."
	sudo cp $(OUTPUT_DIR)/traffic-gen /usr/local/bin/
	sudo cp $(OUTPUT_DIR)/afpacket-stress /usr/local/bin/
	@echo "✓ Installed to /usr/local/bin/"

# Clean
clean:
	@echo "Cleaning..."
	rm -rf $(OUTPUT_DIR)
	$(GO) clean ./...
	@echo "✓ Cleaned"

# Help
help:
	@echo "Traffic Generator Tools"
	@echo ""
	@echo "Targets:"
	@echo "  build           - Build all binaries"
	@echo "  run             - Run traffic-gen (100k PPS)"
	@echo "  run-max         - Run traffic-gen (max rate)"
	@echo "  run-afpacket    - Run afpacket-stress (200 sockets)"
	@echo "  run-afpacket-max - Run afpacket-stress (500 sockets, max rate)"
	@echo "  test            - Run tests"
	@echo "  install         - Install to /usr/local/bin"
	@echo "  clean           - Clean build artifacts"
