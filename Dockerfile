# Dockerfile - Combined Frontend + Backend Build
# Build: docker build -t otter-camp .
# Run: docker run -p 4200:4200 --env-file .env otter-camp

# =============================================================================
# Stage 1: Build Frontend
# =============================================================================
FROM node:20-alpine AS frontend-builder

WORKDIR /app

# Copy package files first (better caching)
COPY web/package.json web/package-lock.json ./

# Install dependencies
RUN npm ci

# Copy source code
COPY web/ ./

# Build for production
ARG VITE_API_URL=/api
ENV VITE_API_URL=$VITE_API_URL

RUN npm run build

# =============================================================================
# Stage 2: Build Backend
# =============================================================================
FROM golang:1.24-alpine AS backend-builder

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
# Stage 3: Runtime
# =============================================================================
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata git

# Create non-root user
RUN addgroup -g 1000 otter && \
    adduser -u 1000 -G otter -s /bin/sh -D otter

WORKDIR /app

# Copy binary from builder
COPY --from=backend-builder /app/server /app/server

# Copy migrations (needed for runtime migration)
COPY --from=backend-builder /app/migrations /app/migrations

# Copy frontend build
COPY --from=frontend-builder /app/dist /app/static

# Set environment variable for static directory
ENV STATIC_DIR=/app/static

# Set ownership
RUN chown -R otter:otter /app

USER otter

# Expose port
EXPOSE 4200

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:4200/health || exit 1

# Run the server
CMD ["/app/server"]
