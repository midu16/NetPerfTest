# Makefile for eBPF Stress Test Program

# Variables
PROJECT_NAME := ebpf-stress
VERSION := 1.0.0
GO := go
CLANG := clang
LLC := llc
ARCH := $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
GOOS := linux
GOARCH := $(ARCH)

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
BPF_CFLAGS := -O2 -g -Wall -Werror
BPF_TARGET := bpf

.PHONY: all build clean test lint fmt vet run help install

all: clean fmt lint vet build

# Build everything
build: build-bpf build-go

# Build eBPF programs
build-bpf:
	@echo "Building eBPF programs..."
	@mkdir -p $(OBJ_DIR)
	$(CLANG) $(BPF_CFLAGS) -target $(BPF_TARGET) -c $(BPF_DIR)/xdp.c -o $(OBJ_DIR)/xdp.o
	$(CLANG) $(BPF_CFLAGS) -target $(BPF_TARGET) -c $(BPF_DIR)/tc.c -o $(OBJ_DIR)/tc.o
	$(CLANG) $(BPF_CFLAGS) -target $(BPF_TARGET) -c $(BPF_DIR)/socket.c -o $(OBJ_DIR)/socket.o
	@echo "✓ eBPF programs compiled"

# Build Go binary
build-go:
	@echo "Building Go binary..."
	@mkdir -p $(OUTPUT_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build $(GO_BUILD_FLAGS) -o $(OUTPUT_DIR)/$(PROJECT_NAME) ./$(CMD_DIR)
	@echo "✓ Go binary built: $(OUTPUT_DIR)/$(PROJECT_NAME)"

# Run the program (example)
run: build
	sudo $(OUTPUT_DIR)/$(PROJECT_NAME) --hook xdp --interface eth0 --loops 1000

# Install to system
install: build
	sudo cp $(OUTPUT_DIR)/$(PROJECT_NAME) /usr/local/bin/
	sudo setcap cap_bpf,cap_net_admin+ep /usr/local/bin/$(PROJECT_NAME)
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

# Generate eBPF bindings
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

# Help
help:
	@echo "Available targets:"
	@echo "  build          - Build eBPF programs and Go binary"
	@echo "  build-bpf      - Build only eBPF programs"
	@echo "  build-go       - Build only Go binary"
	@echo "  run            - Build and run example"
	@echo "  install        - Install to system"
	@echo "  test           - Run tests"
	@echo "  test-coverage  - Run tests with coverage"
	@echo "  lint           - Lint code"
	@echo "  fmt            - Format code"
	@echo "  vet            - Vet code"
	@echo "  clean          - Clean build artifacts"
	@echo "  generate       - Generate eBPF bindings"
	@echo "  docker-build   - Build Docker image"
	@echo "  docker-run     - Run in Docker"
	@echo "  help           - Show this help"
