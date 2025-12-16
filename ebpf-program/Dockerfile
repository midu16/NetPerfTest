# Multi-stage build for eBPF Stress Test Program

# Stage 1: Build eBPF programs
FROM clang:16 AS bpf-builder

WORKDIR /build

# Copy eBPF source files
COPY bpf/ ./bpf/

# Build eBPF programs
RUN mkdir -p bpf/obj && \
    clang -O2 -g -Wall -Werror -target bpf -c bpf/xdp.c -o bpf/obj/xdp.o && \
    clang -O2 -g -Wall -Werror -target bpf -c bpf/tc.c -o bpf/obj/tc.o && \
    clang -O2 -g -Wall -Werror -target bpf -c bpf/socket.c -o bpf/obj/socket.o

# Stage 2: Build Go application
FROM golang:1.21-alpine AS go-builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git make clang llvm

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/ ./cmd/
COPY pkg/ ./pkg/
COPY Makefile ./

# Copy eBPF objects from previous stage
COPY --from=bpf-builder /build/bpf/obj/ ./bpf/obj/

# Build Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-w -s" -o bin/ebpf-stress ./cmd/ebpf-stress

# Stage 3: Runtime image
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=go-builder /build/bin/ebpf-stress .

# Create non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

USER appuser

ENTRYPOINT ["/app/ebpf-stress"]
