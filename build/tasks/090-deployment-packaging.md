# 090: Deployment Packaging and Docker Compose

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 08 §SelfHosted, doc 08 §ManagedDeployment, doc 08 §BackupRestore, doc 08 §EnvironmentVariables, doc 08 §TLSConfiguration, doc 21 §DeploymentPackaging |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

Deployment packaging for OtterCamp: multi-stage Dockerfile, Docker Compose configuration
with all required services, environment variable documentation, a seed data script for
development, and a README stub with bootstrap instructions. All files are for self-hosted
single-tenant deployment (the primary Sprint 1 deployment target).

### Must build

**File:** `Dockerfile`

**File:** `docker-compose.yml`

**File:** `docker-compose.dev.yml` (development overrides)

**File:** `scripts/seed-dev.sh` (development seed data)

**File:** `README.md` (bootstrap instructions stub; minimal, not a full documentation file)

---

#### `Dockerfile`

Multi-stage build: Go build stage + minimal runtime stage.

```dockerfile
# Stage 1: Build
FROM golang:1.21-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy dependency files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build the binary (CGO disabled for scratch compatibility)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
    -o /build/bin/ottercamp ./cmd/ottercamp

# Stage 2: Runtime
FROM alpine:3.19 AS runtime

# Add CA certificates and timezone data for HTTPS and time zone support
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -g 1000 ottercamp && \
    adduser -D -u 1000 -G ottercamp ottercamp

WORKDIR /app

# Copy binary
COPY --from=builder /build/bin/ottercamp /app/ottercamp

# Copy web UI static assets if they exist (built separately)
# COPY --from=builder /build/web/dist /app/web/dist

# Set ownership
RUN chown -R ottercamp:ottercamp /app

USER ottercamp

EXPOSE 4110

# Default: run the API server
ENTRYPOINT ["/app/ottercamp"]
CMD ["serve"]
```

Note on scratch vs alpine: alpine is used instead of scratch to support DNS resolution,
TLS certificates, and shell access for debugging. Switch to scratch in a hardened
production build if desired.

---

#### `docker-compose.yml`

Production-style single-host deployment with three services.

```yaml
version: "3.9"

services:
  postgres:
    image: pgvector/pgvector:pg16
    container_name: ottercamp-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: ${POSTGRES_USER:-ottercamp}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}
      POSTGRES_DB: ${POSTGRES_DB:-ottercamp}
    volumes:
      - postgres-data:/var/lib/postgresql/data
      - ./scripts/init-pgvector.sql:/docker-entrypoint-initdb.d/01-pgvector.sql:ro
    ports:
      - "127.0.0.1:5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER:-ottercamp}"]
      interval: 5s
      timeout: 5s
      retries: 10
    networks:
      - ottercamp-net

  ottercamp:
    image: ${OTTERCAMP_IMAGE:-ottercamp:latest}
    build:
      context: .
      dockerfile: Dockerfile
    container_name: ottercamp-api
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      OTTERCAMP_MODE: ${OTTERCAMP_MODE:-production}
      DATABASE_URL: postgres://${POSTGRES_USER:-ottercamp}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB:-ottercamp}?sslmode=disable
      OTTERCAMP_MASTER_KEY: ${OTTERCAMP_MASTER_KEY:?OTTERCAMP_MASTER_KEY is required}
      OTTERCAMP_BIND: "0.0.0.0:4110"
      OTTERCAMP_WEB_DIR: ${OTTERCAMP_WEB_DIR:-/app/web/dist}
      OTTERCAMP_STORAGE_ADAPTER: ${OTTERCAMP_STORAGE_ADAPTER:-filesystem}
      OTTERCAMP_STORAGE_PATH: ${OTTERCAMP_STORAGE_PATH:-/app/data/objects}
      OTTERCAMP_LOG_LEVEL: ${OTTERCAMP_LOG_LEVEL:-info}
      OTTERCAMP_LOG_FORMAT: ${OTTERCAMP_LOG_FORMAT:-json}
    volumes:
      - object-storage:/app/data/objects
    ports:
      - "${OTTERCAMP_PORT:-4110}:4110"
    command: ["serve"]
    networks:
      - ottercamp-net

  ottercamp-worker:
    image: ${OTTERCAMP_IMAGE:-ottercamp:latest}
    build:
      context: .
      dockerfile: Dockerfile
    container_name: ottercamp-worker
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
      ottercamp:
        condition: service_started
    environment:
      OTTERCAMP_MODE: ${OTTERCAMP_MODE:-production}
      DATABASE_URL: postgres://${POSTGRES_USER:-ottercamp}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB:-ottercamp}?sslmode=disable
      OTTERCAMP_MASTER_KEY: ${OTTERCAMP_MASTER_KEY:?OTTERCAMP_MASTER_KEY is required}
      OTTERCAMP_STORAGE_ADAPTER: ${OTTERCAMP_STORAGE_ADAPTER:-filesystem}
      OTTERCAMP_STORAGE_PATH: ${OTTERCAMP_STORAGE_PATH:-/app/data/objects}
      OTTERCAMP_LOG_LEVEL: ${OTTERCAMP_LOG_LEVEL:-info}
      OTTERCAMP_LOG_FORMAT: ${OTTERCAMP_LOG_FORMAT:-json}
      OTTERCAMP_WORKER_CONCURRENCY: ${OTTERCAMP_WORKER_CONCURRENCY:-4}
    volumes:
      - object-storage:/app/data/objects
    command: ["worker"]
    networks:
      - ottercamp-net

volumes:
  postgres-data:
    driver: local
  object-storage:
    driver: local

networks:
  ottercamp-net:
    driver: bridge
```

---

#### `docker-compose.dev.yml`

Development overrides: exposes postgres port, uses `OTTERCAMP_MODE=development`, enables
verbose logging, and mounts source for hot reload (if supported).

```yaml
version: "3.9"

services:
  postgres:
    ports:
      - "5432:5432"  # expose to host for psql/pgAdmin access

  ottercamp:
    environment:
      OTTERCAMP_MODE: development
      OTTERCAMP_LOG_LEVEL: debug
      OTTERCAMP_LOG_FORMAT: text
    ports:
      - "4110:4110"

  ottercamp-worker:
    environment:
      OTTERCAMP_MODE: development
      OTTERCAMP_LOG_LEVEL: debug
      OTTERCAMP_LOG_FORMAT: text
```

Usage: `docker-compose -f docker-compose.yml -f docker-compose.dev.yml up`

---

#### `scripts/init-pgvector.sql`

SQL run at first Postgres startup to ensure the pgvector extension is enabled:

```sql
-- Enable pgvector extension in the default database
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
```

---

#### `scripts/seed-dev.sh`

Development seed data script. Creates a model provider connection with a placeholder
API key so the development instance can make model calls (or use test-mode mocks).

```bash
#!/usr/bin/env bash
set -euo pipefail

OTTERCAMP_URL="${OTTERCAMP_URL:-http://localhost:4110}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@localhost}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-changeme}"

echo "Seeding development data for ${OTTERCAMP_URL}..."

# Run bootstrap first
./bin/ottercamp bootstrap \
  --server "${OTTERCAMP_URL}" \
  --admin-email "${ADMIN_EMAIL}" \
  --admin-password "${ADMIN_PASSWORD}"

echo "Bootstrap complete."

# Log in and get token
TOKEN=$(curl -sf -X POST "${OTTERCAMP_URL}/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${ADMIN_EMAIL}\",\"password\":\"${ADMIN_PASSWORD}\"}" \
  | jq -r '.data.session_token')

if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
  echo "ERROR: failed to obtain session token"
  exit 1
fi

echo "Logged in as ${ADMIN_EMAIL}."

# Register a model provider connection (Anthropic as default dev provider)
# Replace ANTHROPIC_API_KEY in your environment before running
if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
  curl -sf -X POST "${OTTERCAMP_URL}/v1/model/providers/anthropic/connections" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Content-Type: application/json" \
    -d "{
      \"name\": \"Anthropic Dev\",
      \"api_key\": \"${ANTHROPIC_API_KEY}\",
      \"is_default\": true
    }" > /dev/null
  echo "Anthropic provider connection registered."
else
  echo "ANTHROPIC_API_KEY not set — skipping provider connection seed."
  echo "Model calls will use test-mode stubs."
fi

echo "Development seed complete."
```

---

#### `README.md`

```markdown
# OtterCamp

Self-hosted AI team coordination platform.

## Quick Start (Self-Hosted)

### Prerequisites

- Docker and Docker Compose v2
- A Linux or macOS host with at least 2 GB RAM
- (Optional) An Anthropic or OpenAI API key for real model calls

### 1. Clone and configure

```bash
git clone https://github.com/YOUR_ORG/otter-camp.git
cd otter-camp
cp .env.example .env
```

Edit `.env` and set at minimum:
- `POSTGRES_PASSWORD` — a strong password for the database
- `OTTERCAMP_MASTER_KEY` — a 32-byte hex key for secret encryption
  (generate with: `openssl rand -hex 32`)

### 2. Start services

```bash
docker-compose up -d
```

### 3. Bootstrap

```bash
docker-compose exec ottercamp ./ottercamp bootstrap
```

### 4. Verify

```bash
curl http://localhost:4110/v1/health
# {"status":"healthy"}
```

Open `http://localhost:4110` in your browser.

## Environment Variables

See [docs/environment-variables.md](docs/environment-variables.md) for the full
reference. Key variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `OTTERCAMP_MASTER_KEY` | Yes | — | 32-byte hex key for AES-256-GCM secret encryption |
| `OTTERCAMP_MODE` | No | `production` | `production`, `development`, or `test` |
| `OTTERCAMP_BIND` | No | `0.0.0.0:4110` | HTTP bind address |
| `OTTERCAMP_WEB_DIR` | No | `/app/web/dist` | Path to built web UI assets |
| `OTTERCAMP_STORAGE_ADAPTER` | No | `filesystem` | `filesystem` or `s3` |
| `OTTERCAMP_STORAGE_PATH` | No | `/app/data/objects` | Local path for filesystem adapter |
| `OTTERCAMP_S3_BUCKET` | No | — | S3 bucket name (when `STORAGE_ADAPTER=s3`) |
| `OTTERCAMP_S3_ENDPOINT` | No | — | S3-compatible endpoint URL |
| `OTTERCAMP_LOG_LEVEL` | No | `info` | `debug`, `info`, `warn`, `error` |
| `OTTERCAMP_LOG_FORMAT` | No | `json` | `json` or `text` |
| `OTTERCAMP_WORKER_CONCURRENCY` | No | `4` | Worker goroutine concurrency |
| `OTTERCAMP_AUTH_MODE` | No | `standard` | `standard` or `local` (localhost auto-login) |
| `OTTERCAMP_TLS_MODE` | No | `none` | `none`, `acme`, or `manual` |
| `OTTERCAMP_TLS_DOMAIN` | No | — | Domain for ACME certificate provisioning |
| `OTTERCAMP_TLS_CERT_FILE` | No | — | Path to TLS certificate (manual TLS mode) |
| `OTTERCAMP_TLS_KEY_FILE` | No | — | Path to TLS private key (manual TLS mode) |
```

---

#### `.env.example`

```bash
# Database
POSTGRES_USER=ottercamp
POSTGRES_PASSWORD=changeme_in_production
POSTGRES_DB=ottercamp

# OtterCamp core
OTTERCAMP_MODE=production
OTTERCAMP_MASTER_KEY=generate_with_openssl_rand_hex_32
OTTERCAMP_PORT=4110
OTTERCAMP_LOG_LEVEL=info
OTTERCAMP_LOG_FORMAT=json
OTTERCAMP_WORKER_CONCURRENCY=4

# Storage (filesystem default; switch to s3 for production)
OTTERCAMP_STORAGE_ADAPTER=filesystem
OTTERCAMP_STORAGE_PATH=/app/data/objects
# OTTERCAMP_S3_BUCKET=my-ottercamp-bucket
# OTTERCAMP_S3_ENDPOINT=https://s3.amazonaws.com

# TLS (optional; use a reverse proxy like Nginx/Caddy instead for most deployments)
OTTERCAMP_TLS_MODE=none
# OTTERCAMP_TLS_MODE=acme
# OTTERCAMP_TLS_DOMAIN=ottercamp.example.com
```

### Must NOT build

- Full deployment runbook documentation (deferred to Sprint 2 docs)
- Managed multi-tenant deployment configuration (catalog DB routing — Sprint 2)
- Kubernetes/Helm charts (Sprint 2)
- CI/CD deployment automation (Sprint 2)
- Worker concurrency tuning guide (Sprint 2)

## Acceptance Criteria

- [ ] `Dockerfile` builds successfully with `docker build -t ottercamp:test .`
- [ ] Multi-stage build: builder stage uses `golang:1.21-alpine`; runtime stage uses `alpine:3.19`
- [ ] `docker build` produces a working binary: `docker run --rm ottercamp:test version` exits 0
- [ ] `docker-compose.yml` is valid; `docker-compose config` parses without errors
- [ ] Three services defined: `postgres` (pgvector/pgvector:pg16), `ottercamp` (API server), `ottercamp-worker` (same image, `worker` command)
- [ ] `docker-compose up` starts all three services; postgres healthcheck passes before ottercamp starts
- [ ] `POSTGRES_PASSWORD` and `OTTERCAMP_MASTER_KEY` are required (`:?` syntax — compose fails with clear error if unset)
- [ ] `docker-compose.dev.yml` overrides `OTTERCAMP_MODE=development` and exposes postgres port 5432 to host
- [ ] `scripts/init-pgvector.sql` creates `vector` and `uuid-ossp` extensions
- [ ] `scripts/seed-dev.sh` runs `ottercamp bootstrap` and optionally registers a model provider
- [ ] `README.md` contains Quick Start section with 4 steps (clone, configure, start, verify)
- [ ] `README.md` environment variables table includes all 16 listed variables with Required/Default/Description
- [ ] `.env.example` is present and contains all required and optional variables with placeholder values
- [ ] `OTTERCAMP_MASTER_KEY` is never committed with a real value (`.env.example` uses placeholder)

## Tests Required

**Unit tests:** None — this task IS deployment infrastructure.

**Integration tests:** None — deployment files are validated by `docker-compose config`
and a `docker build` smoke test.

**E2E tests:** None — the Docker Compose setup is validated by the bootstrap E2E test
(task 081) when running against a docker-compose-based deployment.

## Implementer Notes

**pgvector/pgvector:pg16 image:**
This image includes PostgreSQL 16 with the pgvector extension pre-compiled. The
`scripts/init-pgvector.sql` script runs `CREATE EXTENSION IF NOT EXISTS vector` as a
belt-and-suspenders measure in case the extension was not auto-enabled by the image.

**Dockerfile CGO:**
`CGO_ENABLED=0` is required for the binary to run in the `alpine:3.19` runtime stage.
All Go dependencies must be pure Go (no CGO). If a dependency requires CGO, the runtime
stage must be changed from `alpine:3.19` to an image with libc (e.g., `debian:bookworm-slim`).

**OTTERCAMP_MASTER_KEY:**
The master key is a hex-encoded 32-byte value (64 hex characters). It is used for
AES-256-GCM encryption of `secret` table rows (task 009). The Docker Compose setup
requires it at startup via the `:?` required variable syntax. The `.env.example` file
contains a placeholder reminder to generate it with `openssl rand -hex 32`.

**Worker vs API service:**
Both `ottercamp` (API) and `ottercamp-worker` (background worker) use the same Docker
image built from the same `Dockerfile`. The difference is the container command:
- API service: `command: ["serve"]` (starts HTTP server + embedded worker for test/dev mode)
- Worker service: `command: ["worker"]` (starts background job worker only, no HTTP listener)

**ISSUE #22 (health endpoint path):**
The `README.md` Quick Start uses `GET /v1/health` to verify the deployment. The Docker
Compose healthcheck for the `ottercamp` service (not shown above but can be added) should
use the same path. If both `/health` and `/v1/health` are exposed, document both.

**Object storage shared volume:**
Both `ottercamp` and `ottercamp-worker` mount the same `object-storage` volume at
`/app/data/objects`. This ensures workers can access artifacts created by the API server
and vice versa. In a multi-host deployment, replace with S3-compatible object storage.

**Web UI assets:**
The `OTTERCAMP_WEB_DIR` environment variable points to the built React SPA assets (task 070).
In the Docker image, these are not included in the `Dockerfile` by default (the
`COPY --from=builder /build/web/dist` line is commented out). For a production build
with UI included, uncomment that line and ensure the web UI is built before `docker build`.
Sprint 1 focuses on the API; the UI build integration is a Sprint 2 task.
