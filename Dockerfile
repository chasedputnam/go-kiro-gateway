# Go Kiro Gateway - Docker Image
#
# Multi-stage build using repo root as context (context: .).
# The gateway source lives under gateway/ relative to the repo root.
#
# Build:
#   docker build -t go-kiro-gateway .
#   docker build --build-arg VERSION=1.0.0 -t go-kiro-gateway .
#
# Run:
#   docker run -p 8000:8000 --env-file .env go-kiro-gateway

# ---------------------------------------------------------------------------
# Build stage
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

# Cache module downloads (paths relative to repo root context).
COPY gateway/go.mod gateway/go.sum ./
RUN go mod download

# Copy gateway source and build a fully static binary.
COPY gateway/ .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
    -ldflags "-X main.version=${VERSION} -s -w" \
    -o /go-kiro-gateway ./cmd/gateway

# ---------------------------------------------------------------------------
# Runtime stage
# ---------------------------------------------------------------------------
FROM alpine:3.21

# Install CA certificates and curl for health checks.
RUN apk add --no-cache ca-certificates curl

# Create non-root user.
RUN addgroup -S kiro && adduser -S -G kiro kiro

# Copy the compiled binary.
COPY --from=builder /go-kiro-gateway /usr/local/bin/go-kiro-gateway

# Create debug logs directory with proper permissions.
RUN mkdir -p /app/debug_logs && chown -R kiro:kiro /app/debug_logs

WORKDIR /app

# Switch to non-root user.
USER kiro

# Expose the default gateway port.
EXPOSE 8000

# Health check querying the /health endpoint.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["curl", "-sf", "http://localhost:8000/health"]

# Entry point.
ENTRYPOINT ["go-kiro-gateway"]
