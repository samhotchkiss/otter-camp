FROM golang:1.24-alpine AS builder
WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /app/out/www-server ./cmd/www

FROM alpine:3.19
WORKDIR /app

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/out/www-server /app/www-server
COPY --from=builder /app/www /app/www

ENV PORT=8080
EXPOSE 8080

CMD ["/app/www-server"]
