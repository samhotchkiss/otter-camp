# Dockerfile - Go Backend (Multi-stage build)
# Build: docker build -t otter-camp-api .
# Run: docker run -p 8080:8080 --env-file .env otter-camp-api

# =============================================================================
# Stage 1: Build
# =============================================================================
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Copy go mod files first (better caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o /app/server \
    ./cmd/server

# =============================================================================
# Stage 2: Runtime
# =============================================================================
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 otter && \
    adduser -u 1000 -G otter -s /bin/sh -D otter

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/server /app/server

# Copy migrations (needed for runtime migration)
COPY --from=builder /app/migrations /app/migrations

# Set ownership
RUN chown -R otter:otter /app

USER otter

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Run the server
CMD ["/app/server"]
