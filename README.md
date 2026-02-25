# OtterCamp

Self-hosted AI team coordination platform.

## Quick Start (Self-Hosted)

### Prerequisites

- Docker and Docker Compose v2
- Linux or macOS host with at least 2 GB RAM
- Optional: model provider API credentials

### 1. Clone and configure

```bash
git clone https://github.com/samhotchkiss/otter-camp.git
cd otter-camp
cp .env.example .env
```

Set at minimum in `.env`:
- `POSTGRES_PASSWORD`
- `OTTERCAMP_MASTER_KEY` (placeholder in `.env.example`; generate a real value before production)

### 2. Start services

```bash
docker compose up -d
```

### 3. Bootstrap

```bash
docker compose exec ottercamp ./ottercamp bootstrap
```

### 4. Verify

```bash
curl http://localhost:4110/health/live
```

Expected response: `{"data":{"status":"healthy"...}}` (or `ok` depending on build).

## Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | Yes | — | Legacy alias; use `OTTERCAMP_DATABASE_URL` for runtime config. |
| `OTTERCAMP_DATABASE_URL` | Yes | — | PostgreSQL connection string used by API and worker. |
| `OTTERCAMP_MASTER_KEY` | Yes | — | Master encryption key for secrets at rest. |
| `OTTERCAMP_MODE` | No | `production` | Runtime mode (`production`, `development`, `test`). |
| `OTTERCAMP_BIND` | No | `0.0.0.0:4110` | Legacy alias; prefer `OTTERCAMP_ADDR`. |
| `OTTERCAMP_ADDR` | No | `:4110` | HTTP bind address used by `ottercamp serve`. |
| `OTTERCAMP_WEB_DIR` | No | `/app/web/dist` | Path to built SPA assets for static serving. |
| `OTTERCAMP_STORAGE_ADAPTER` | No | `filesystem` | Legacy alias; use `OTTERCAMP_STORAGE_BACKEND` (`fs`/`s3`). |
| `OTTERCAMP_STORAGE_BACKEND` | No | `fs` | Object storage backend (`fs` or `s3`). |
| `OTTERCAMP_STORAGE_PATH` | No | `/app/data/objects` | Legacy alias; use `OTTERCAMP_STORAGE_ROOT`. |
| `OTTERCAMP_STORAGE_ROOT` | No | `/app/data/objects` | Filesystem object storage root path. |
| `OTTERCAMP_S3_BUCKET` | No | — | S3 bucket name for object storage. |
| `OTTERCAMP_S3_ENDPOINT` | No | — | S3-compatible endpoint URL. |
| `OTTERCAMP_LOG_LEVEL` | No | `info` | Logging level (`debug`, `info`, `warn`, `error`). |
| `OTTERCAMP_LOG_FORMAT` | No | `json` | Logging output format (compose keeps this for compatibility). |
| `OTTERCAMP_WORKER_CONCURRENCY` | No | `4` | Worker concurrency hint for deployment config. |
| `OTTERCAMP_AUTH_MODE` | No | `standard` | Auth mode (`standard` or `local`). |
| `OTTERCAMP_TLS_MODE` | No | `none` | TLS mode (`none`, `acme`, `manual`). |
| `OTTERCAMP_TLS_DOMAIN` | No | — | Legacy alias for ACME domain; prefer `OTTERCAMP_TLS_ACME_DOMAIN`. |
| `OTTERCAMP_TLS_ACME_DOMAIN` | No | — | Domain used for ACME certificate provisioning. |
| `OTTERCAMP_TLS_CERT_FILE` | No | — | Legacy alias for TLS cert path; prefer `OTTERCAMP_TLS_CERT`. |
| `OTTERCAMP_TLS_CERT` | No | — | TLS certificate path for manual TLS mode. |
| `OTTERCAMP_TLS_KEY_FILE` | No | — | Legacy alias for TLS key path; prefer `OTTERCAMP_TLS_KEY`. |
| `OTTERCAMP_TLS_KEY` | No | — | TLS private key path for manual TLS mode. |
