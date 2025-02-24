FROM golang:1.24-bookworm AS builder

WORKDIR /opt

# Mount go.mod/sum for dependency caching
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Build with source code
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -o headd ./cmd/headd

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    tzdata \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt

COPY server.crt server.key ./

# Copy the binary from builder
COPY --from=builder /opt/headd .

# Run the binary
CMD ["./headd", "server"]
