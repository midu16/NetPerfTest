# eBPF Stress Test Program - Multi-stage Dockerfile
#
# Build: docker build -t ebpf-stress:latest .
# Run:   docker run --rm --privileged --network host ebpf-stress --hook xdp --interface eth0 --loops 10000

# =============================================================================
# Stage 1: Build eBPF programs
# =============================================================================
FROM fedora:latest AS bpf-builder

LABEL maintainer="Mihai IDU <midu@redhat.com>" \
      description="eBPF program builder stage"

# Install BPF toolchain
RUN dnf install -y \
    clang \
    llvm \
    libbpf-devel \
    kernel-headers \
    && dnf clean all

WORKDIR /build

# Copy BPF source
COPY bpf/ bpf/

# Build BPF programs
RUN mkdir -p bpf/obj && \
    clang -O2 -g -Wall -target bpf -c bpf/stress.c -o bpf/obj/stress.o && \
    clang -O2 -g -Wall -target bpf -c bpf/xdp.c -o bpf/obj/xdp.o 2>/dev/null || true && \
    clang -O2 -g -Wall -target bpf -c bpf/tc.c -o bpf/obj/tc.o 2>/dev/null || true && \
    clang -O2 -g -Wall -target bpf -c bpf/socket.c -o bpf/obj/socket.o 2>/dev/null || true

# =============================================================================
# Stage 2: Build Go binary
# =============================================================================
FROM golang:1.21 AS go-builder

LABEL maintainer="Mihai IDU <midu@redhat.com>" \
      description="Go program builder stage"

WORKDIR /build

# Copy go.mod and go.sum first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.version=2.0.0 -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o ebpf-stress \
    ./cmd/ebpf-stress

# =============================================================================
# Stage 3: Final runtime image
# =============================================================================
FROM fedora:latest

LABEL maintainer="Mihai IDU <midu@redhat.com>" \
      description="eBPF Stress Test - Configurable N loops per packet" \
      version="2.0.0"

# Install runtime dependencies
RUN dnf install -y \
    iproute \
    procps-ng \
    && dnf clean all

# Create app directory
WORKDIR /app

# Copy BPF programs from builder
COPY --from=bpf-builder /build/bpf/obj/ bpf/obj/

# Copy Go binary from builder
COPY --from=go-builder /build/ebpf-stress /app/ebpf-stress

# Make binary executable
RUN chmod +x /app/ebpf-stress

# Create non-root user (but program needs root for eBPF)
# This is for documentation; actual runtime requires --privileged
RUN useradd -r -u 1001 -g root ebpfuser

# Expose metrics port
EXPOSE 9091

# Default entrypoint
ENTRYPOINT ["/app/ebpf-stress"]

# Default arguments (can be overridden)
CMD ["--hook", "xdp", "--interface", "eth0", "--loops", "1000", "--verbose"]
