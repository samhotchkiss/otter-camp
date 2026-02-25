# Stage 1: Build
FROM golang:1.21-alpine AS builder

WORKDIR /build

# Build dependencies for module downloads and version stamping.
RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
    -o /build/bin/ottercamp ./cmd/ottercamp

# Stage 2: Runtime
FROM alpine:3.19 AS runtime

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1000 ottercamp && \
    adduser -D -u 1000 -G ottercamp ottercamp

WORKDIR /app

COPY --from=builder /build/bin/ottercamp /app/ottercamp

# Uncomment for a production image that embeds built web assets.
# COPY --from=builder /build/web/dist /app/web/dist

RUN chown -R ottercamp:ottercamp /app

USER ottercamp

EXPOSE 4110

ENTRYPOINT ["/app/ottercamp"]
CMD ["serve"]
