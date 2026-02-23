---
## Summary

This spec defines how OtterCamp is packaged, deployed, configured, and operated across three deployment modes: Local Single-Node (developer laptop), VPS Single-Tenant (self-host), and Managed Multi-Tenant (hosted). The same Go binary is used in all modes -- the deployment mode is determined entirely by configuration, not by building different artifacts. The single most important architectural constraint is that PostgreSQL is the only required external dependency for self-hosting; everything else (object storage, job queue, event bus) has a built-in fallback that runs on PostgreSQL or the local filesystem.

In single-node mode, one process runs both the API server (HTTP/WebSocket) and the background Worker (agent turns, memory pipeline, scheduled tasks) using goroutines, sharing a single database connection pool. For VPS deployments, a `--mode` flag allows splitting API and Worker into separate processes for resource isolation, coordinating through the PostgreSQL-backed job queue. The managed multi-tenant mode uses **database-per-org isolation** (consistent with 04-auth-tenancy-and-identity.md) — every organization gets its own PostgreSQL database, with a catalog database mapping org slugs to database connections. No shared databases, no RLS. Managed mode adds per-org resource limits, S3 with path-prefixed object isolation, per-org migration orchestration, connection routing, and horizontal scaling of stateless API/Worker processes behind a load balancer. Docker Compose is the primary self-host distribution (as few as two containers: OtterCamp + PostgreSQL with pgvector), while a standalone binary with zero runtime dependencies is also available via Homebrew, apt, or direct download.

Configuration is entirely through environment variables with sensible defaults -- the only truly required user input is a model provider API key (e.g., `ANTHROPIC_API_KEY`). An optional YAML config file supports complex structures like MCP server definitions. Schema migrations are embedded in the binary and run automatically on startup (forward-only, transactional, backward-compatible for minor versions). Secrets (provider API keys, SSH keys, MCP credentials) are stored AES-256-GCM encrypted in a `secret` table, with a master key sourced from an environment variable, key file, or cloud KMS. The spec also covers backup/restore (single-command `ottercamp backup`/`restore` packaging both database and object storage), health check endpoints (`/health` for liveness, `/ready` for readiness), TLS options (reverse proxy, built-in ACME, or manual certs), the upgrade path (semantic versioning with automatic migration for minor releases), and the CLI command surface for server management.

---

# 08. Deployment and Self-Hosting

## Goals

- Make OtterCamp trivially easy to run on a developer's laptop.
- Make OtterCamp deployable on any VPS with a single command.
- Support a managed multi-tenant hosting model without requiring a fundamentally different architecture.
- PostgreSQL is the only required external dependency for self-host. Everything else has a fallback.

## Deployment Modes

OtterCamp runs in three modes. The application binary is the same in all three — the mode is determined by configuration, not by building a different artifact.

### Local Single-Node (Developer Laptop)

The simplest possible deployment. One process, one database, local filesystem for object storage.

- **Target audience**: developers, personal use, evaluation, local development.
- **Process model**: single Go binary runs both the API service and the Worker service in one process.
- **Database**: PostgreSQL with pgvector extension — local (via Docker, Homebrew, system package) or remote. pgvector is required for the memory system's 1536-dimension embeddings (see 06-memory.md).
- **Object storage**: local filesystem. A configured directory stores all artifacts, uploads, and binary content. No S3 setup required.
- **Job queue**: PostgreSQL-backed (LISTEN/NOTIFY + polling). No Redis or external queue.
- **Event bus**: PostgreSQL-backed (same mechanism as job queue).
- **Network**: localhost only by default. No TLS required for local access.
- **Browser service**: optional. If configured, connects to a locally running headless Chrome. If not configured, browser-based agent tools are unavailable (not an error — the feature is simply absent).
- **Resource requirements**: 2 GB RAM, 2 CPU cores, 10 GB disk.

This mode exists so that a developer can run `ottercamp serve` and be productive in under a minute.

### VPS Single-Tenant (Self-Host)

The same binary, configured for a remote server with internet access. One operator, one instance.

- **Target audience**: self-hosters who want their own persistent OtterCamp instance.
- **Process model**: single Go binary in combined mode, or optionally separate API and Worker processes for better resource isolation (same binary, different `--mode` flag).
- **Database**: PostgreSQL with pgvector — local on the VPS or a managed database (RDS, Supabase, Neon, etc.). Most managed PostgreSQL providers support pgvector natively.
- **Object storage**: local filesystem (default) or S3-compatible (MinIO on the VPS, or a cloud provider like AWS S3, Cloudflare R2, Backblaze B2).
- **Job queue**: PostgreSQL-backed. Same as local mode.
- **Event bus**: PostgreSQL-backed. Same as local mode.
- **Network**: exposed to the internet (or a private network). TLS required — via reverse proxy (Caddy, Nginx) or the built-in ACME integration.
- **Browser service**: optional. Docker Compose includes a headless Chrome container.
- **Resource requirements**: 4 GB RAM, 2 CPU cores, 50 GB disk.

### Managed Multi-Tenant (Hosted)

Operated by us. Multiple organizations on shared application infrastructure with full database-level isolation (see 04-auth-tenancy-and-identity.md).

- **Target audience**: users who do not want to manage infrastructure.
- **Process model**: separate API and Worker processes. Workers can scale horizontally.
- **Database**: managed PostgreSQL with **database-per-org isolation**. Every organization gets its own PostgreSQL database. A catalog database maps org slugs to database connections. No shared data between tenants, no RLS needed — isolation is architectural (see Managed Mode Specifics).
- **Object storage**: S3 (or compatible). Per-org path prefixing for isolation.
- **Job queue**: PostgreSQL-backed, per-org database. Same implementation as self-host.
- **Event bus**: PostgreSQL-backed, per-org database. Same implementation.
- **Network**: load balancer (ALB or equivalent) in front of API processes. TLS terminated at the load balancer.
- **Browser service**: pooled headless Chrome instances, allocated per-org.
- **Orchestration**: Docker Compose + process manager (systemd, Supervisor) for initial launch. Kubernetes is NOT required. K8s support is added when operational complexity justifies it, not before.
- **Resource requirements**: varies by tenant count. Provisioned per operational metrics.

## Single-Node Architecture

In single-node mode, one process runs everything. This is the default and the most common deployment.

```
┌─────────────────────────────────────────────────────┐
│                  ottercamp serve                     │
│                                                     │
│  ┌──────────────┐  ┌──────────────┐                │
│  │  API Server   │  │   Worker     │                │
│  │  (HTTP + WS)  │  │  (Jobs)      │                │
│  └──────┬───────┘  └──────┬───────┘                │
│         │                  │                         │
│  ┌──────┴──────────────────┴───────┐                │
│  │        Shared Application Core   │                │
│  │   (Domain modules, Model GW,    │                │
│  │    Memory pipeline, Connectors)  │                │
│  └──────────────┬──────────────────┘                │
│                 │                                    │
│  ┌──────────────┴──────────────────┐                │
│  │       Storage Layer              │                │
│  │  PostgreSQL    Local Filesystem  │                │
│  └──────────────────────────────────┘                │
└─────────────────────────────────────────────────────┘
```

### How It Works

- The `ottercamp serve` command starts both the HTTP/WebSocket server and the background worker in the same process, using goroutines. No IPC, no inter-process communication overhead.
- The API server handles HTTP requests and WebSocket/SSE connections. It serves the web UI static assets, the REST API, and the realtime transport.
- The Worker processes background jobs: agent turns (model calls + tool loops), memory pipeline extraction, scheduled tasks, cleanup jobs. It pulls jobs from the PostgreSQL-backed queue.
- Both API and Worker share the same database connection pool. Connection pool size is configured once and shared.
- The model gateway (see doc 07) is a module within the process, not a separate service. It manages provider connections, routing, and concurrency limits.
- The memory pipeline (see doc 06) runs as Worker jobs — extraction, scoring, dedup, entity synthesis are all background jobs.
- MCP connections (see doc 09) are managed by the connector runtime within the process. Each connection is a long-lived subprocess or network connection.

### Split Mode

For VPS deployments where the operator wants resource isolation between serving HTTP traffic and running agent work:

```
ottercamp serve --mode api     # runs only the API server
ottercamp serve --mode worker  # runs only the Worker
ottercamp serve                # runs both (default)
```

Both processes connect to the same PostgreSQL database and the same object storage. The job queue in PostgreSQL coordinates work between them. No additional infrastructure is needed for the split.

## Docker Compose Packaging

Docker Compose is the primary self-host distribution. One file, one command, everything runs.

### docker-compose.yml

```yaml
services:
  ottercamp:
    image: ghcr.io/ottercamp/ottercamp:latest
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://ottercamp:ottercamp@postgres:5432/ottercamp?sslmode=disable
      - OBJECT_STORAGE_TYPE=s3
      - OBJECT_STORAGE_ENDPOINT=http://minio:9000
      - OBJECT_STORAGE_BUCKET=ottercamp
      - OBJECT_STORAGE_ACCESS_KEY=ottercamp
      - OBJECT_STORAGE_SECRET_KEY=ottercamp
      # Model provider keys — the only required user configuration
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
      - OPENAI_API_KEY=${OPENAI_API_KEY:-}
      - GOOGLE_AI_API_KEY=${GOOGLE_AI_API_KEY:-}
    volumes:
      - ottercamp-data:/data
    depends_on:
      postgres:
        condition: service_healthy
      minio:
        condition: service_started
    restart: unless-stopped

  postgres:
    image: pgvector/pgvector:pg16
    environment:
      - POSTGRES_USER=ottercamp
      - POSTGRES_PASSWORD=ottercamp
      - POSTGRES_DB=ottercamp
    volumes:
      - postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ottercamp"]
      interval: 5s
      timeout: 5s
      retries: 5
    restart: unless-stopped

  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    environment:
      - MINIO_ROOT_USER=ottercamp
      - MINIO_ROOT_PASSWORD=ottercamp
    volumes:
      - minio-data:/data
    restart: unless-stopped

  # Optional: headless Chrome for browser-based agent tools
  # Uncomment to enable browser capabilities
  # browser:
  #   image: ghcr.io/ottercamp/browser:latest
  #   restart: unless-stopped
  #   environment:
  #     - CHROME_FLAGS=--no-sandbox --disable-gpu

volumes:
  ottercamp-data:
  postgres-data:
  minio-data:
```

### Startup Sequence

1. PostgreSQL starts and becomes healthy.
2. MinIO starts (object storage).
3. OtterCamp starts, connects to PostgreSQL, runs schema migrations automatically.
4. If this is the first start (empty database), bootstrap runs: creates the org, prompts for or reads admin credentials from environment variables, seeds the starter trio (Frank, Lori, Ellie), creates the General session. See doc 04 for full bootstrap flow.
5. API server begins accepting connections on port 8080.
6. Worker begins processing background jobs.
7. The operator opens `http://localhost:8080` and logs in.

### Minimal Docker Compose (No MinIO)

For users who want the absolute smallest footprint, a minimal compose file uses local filesystem for object storage instead of MinIO:

```yaml
services:
  ottercamp:
    image: ghcr.io/ottercamp/ottercamp:latest
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://ottercamp:ottercamp@postgres:5432/ottercamp?sslmode=disable
      - OBJECT_STORAGE_TYPE=filesystem
      - OBJECT_STORAGE_PATH=/data/objects
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
    volumes:
      - ottercamp-data:/data
    depends_on:
      postgres:
        condition: service_healthy
    restart: unless-stopped

  postgres:
    image: pgvector/pgvector:pg16
    environment:
      - POSTGRES_USER=ottercamp
      - POSTGRES_PASSWORD=ottercamp
      - POSTGRES_DB=ottercamp
    volumes:
      - postgres-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ottercamp"]
      interval: 5s
      timeout: 5s
      retries: 5
    restart: unless-stopped

volumes:
  ottercamp-data:
  postgres-data:
```

Two containers. That is the minimum Docker deployment.

## Binary Distribution

For users who do not want Docker. A single Go binary with zero runtime dependencies (beyond PostgreSQL).

### Installation

```bash
# macOS (Homebrew)
brew install ottercamp/tap/ottercamp

# Linux (package managers)
# Debian/Ubuntu
curl -fsSL https://get.ottercamp.dev/deb | sudo bash
apt install ottercamp

# Arch
yay -S ottercamp

# Direct download (any platform)
curl -fsSL https://get.ottercamp.dev | sh

# From source
go install github.com/ottercamp/ottercamp@latest
```

### Quick Start

```bash
# Start with a local or remote PostgreSQL
ottercamp serve --database-url postgres://user:pass@localhost:5432/ottercamp

# Or set it via environment variable
export DATABASE_URL=postgres://user:pass@localhost:5432/ottercamp
export ANTHROPIC_API_KEY=sk-ant-...
ottercamp serve
```

On first run, the binary:

1. Connects to PostgreSQL.
2. Runs schema migrations.
3. Runs the bootstrap flow (creates org, prompts for admin credentials if not set via env vars).
4. Starts serving on `http://localhost:8080`.

Object storage defaults to local filesystem (`~/.ottercamp/objects/` or configurable via `OBJECT_STORAGE_PATH`). No S3 setup required.

### CLI Commands

```bash
ottercamp serve                 # Start the server (API + Worker)
ottercamp serve --mode api      # Start only the API server
ottercamp serve --mode worker   # Start only the Worker
ottercamp serve --port 9090     # Custom port

ottercamp migrate               # Run pending database migrations
ottercamp migrate --status      # Show migration status

ottercamp bootstrap             # Run bootstrap manually (create org, admin user, starter trio)
ottercamp bootstrap --email operator@example.com --password <password>

ottercamp reset-password --email operator@example.com

ottercamp backup --output backup.tar.gz    # Full backup (database + objects)
ottercamp restore --input backup.tar.gz    # Full restore

ottercamp version               # Show version and build info
ottercamp health                # Check service health (database, object storage, providers)
```

## Configuration

### Environment Variables

All configuration is via environment variables. No configuration file is required. Sensible defaults mean most variables can be omitted.

#### Required

| Variable | Description | Example |
|----------|-------------|---------|
| `ANTHROPIC_API_KEY` | Anthropic API key. At least one model provider key is required. | `sk-ant-...` |

That is the only required configuration when using the binary with a local PostgreSQL. If PostgreSQL is not on `localhost:5432`, `DATABASE_URL` is also needed.

#### Database

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | `postgres://localhost:5432/ottercamp?sslmode=disable` | PostgreSQL connection string. |
| `DATABASE_MAX_CONNECTIONS` | `25` | Maximum connection pool size. |
| `DATABASE_MIN_CONNECTIONS` | `5` | Minimum idle connections in pool. |

#### Object Storage

| Variable | Default | Description |
|----------|---------|-------------|
| `OBJECT_STORAGE_TYPE` | `filesystem` | Storage backend: `filesystem` or `s3`. |
| `OBJECT_STORAGE_PATH` | `~/.ottercamp/objects` | Local path for filesystem storage. Ignored when type is `s3`. |
| `OBJECT_STORAGE_ENDPOINT` | — | S3-compatible endpoint URL. Required when type is `s3`. |
| `OBJECT_STORAGE_BUCKET` | `ottercamp` | S3 bucket name. |
| `OBJECT_STORAGE_REGION` | `us-east-1` | S3 region. |
| `OBJECT_STORAGE_ACCESS_KEY` | — | S3 access key. |
| `OBJECT_STORAGE_SECRET_KEY` | — | S3 secret key. |

#### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listen port. |
| `HOST` | `0.0.0.0` | HTTP listen address. |
| `TLS_CERT_FILE` | — | Path to TLS certificate. If set, serves HTTPS. |
| `TLS_KEY_FILE` | — | Path to TLS private key. |
| `TLS_ACME_DOMAIN` | — | Domain for automatic ACME (Let's Encrypt) certificate. Requires port 443. |
| `BASE_URL` | `http://localhost:8080` | Public-facing URL. Used for links in notifications, API responses, and OAuth callbacks. |

#### Model Providers

| Variable | Default | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | — | Anthropic API key. |
| `OPENAI_API_KEY` | — | OpenAI API key. |
| `GOOGLE_AI_API_KEY` | — | Google AI API key. |
| `MODEL_CONCURRENCY_GLOBAL` | `10` | Maximum concurrent LLM sessions across all providers. |
| `MODEL_CONCURRENCY_PER_PROVIDER` | `5` | Maximum concurrent sessions per provider. |

#### Worker

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_CONCURRENCY` | `5` | Maximum concurrent background jobs. |
| `WORKER_POLL_INTERVAL` | `1s` | Job queue polling interval (also uses LISTEN/NOTIFY for immediate wakeup). |

#### Bootstrap

| Variable | Default | Description |
|----------|---------|-------------|
| `OTTERCAMP_ADMIN_EMAIL` | — | Admin email for automated bootstrap. If unset, prompts interactively. |
| `OTTERCAMP_ADMIN_PASSWORD` | — | Admin password for automated bootstrap. If unset, prompts interactively. |
| `OTTERCAMP_ORG_NAME` | `My Organization` | Organization name for bootstrap. |

#### Browser Service

| Variable | Default | Description |
|----------|---------|-------------|
| `BROWSER_ENDPOINT` | — | WebSocket URL for headless Chrome (CDP endpoint). If unset, browser tools are disabled. |
| `BROWSER_POOL_SIZE` | `2` | Number of browser instances to maintain. |

#### Observability

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error`. |
| `LOG_FORMAT` | `json` | Log format: `json` or `text`. `text` is human-readable for local dev. |
| `METRICS_ENABLED` | `false` | Enable Prometheus metrics endpoint at `/metrics`. |

#### Git

| Variable | Default | Description |
|----------|---------|-------------|
| `GIT_REPOS_PATH` | `~/.ottercamp/repos` | Local path for project git repositories. |

### Configuration File (Optional)

For complex structures that do not map well to flat environment variables (such as per-provider model routing overrides or complex MCP server configurations), an optional YAML configuration file is supported:

```yaml
# ~/.ottercamp/config.yaml (or OTTERCAMP_CONFIG_FILE env var)

# Model routing overrides (per-provider settings beyond API key)
models:
  anthropic:
    default_model: claude-sonnet-4-20250514
    max_tokens: 8192
  openai:
    default_model: gpt-4o
    max_tokens: 4096

# MCP server connections (complex, multi-field config)
mcp_servers:
  - name: github
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "${GITHUB_TOKEN}"
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/projects"]
```

Environment variables always override the config file. The config file is optional — everything can be done with env vars alone, though some configurations (like MCP servers with multiple fields) are more readable in YAML.

## Database Management

### Schema Migrations

Migrations are embedded in the Go binary. They are forward-only SQL scripts, numbered sequentially.

```
migrations/
  001_initial_schema.sql
  002_add_chat_tables.sql
  003_add_project_tables.sql
  004_add_memory_tables.sql
  ...
```

### Automatic Migration on Startup

When `ottercamp serve` starts, it checks the current schema version against the migrations embedded in the binary. If there are pending migrations, they run automatically before the server begins accepting requests.

Migration behavior:

- **Startup check**: compare database schema version (stored in a `schema_migrations` table) against the latest migration in the binary.
- **Pending migrations**: run in sequence, within a transaction per migration. If any migration fails, the transaction is rolled back and the process exits with an error.
- **Already current**: no-op. Startup continues immediately.
- **Downgrade detection**: if the database schema version is newer than the binary, the process refuses to start with a clear error message. This prevents running an older binary against a newer database.

### Manual Migration Command

For operators who want to run migrations separately from startup (e.g., in a CI/CD pipeline or before deploying a new version):

```bash
ottercamp migrate                # Run all pending migrations
ottercamp migrate --status       # Show current version and pending migrations
ottercamp migrate --dry-run      # Show what would run without executing
```

### Migration Guarantees

- **Forward-only**: there are no rollback migrations. If a migration causes problems, the fix is a new forward migration. This eliminates the class of bugs where a rollback migration is out of sync with the forward migration.
- **Backward-compatible for minor versions**: minor version upgrades (e.g., 1.2 to 1.3) only add columns, tables, and indexes. They do not remove or rename columns that the previous version depends on. This means the old binary can continue running against the new schema during a rolling update.
- **Transactional**: each migration runs in its own transaction. A partial migration is not possible.
- **Idempotent detection**: the `schema_migrations` table tracks which migrations have been applied. Running migrations multiple times is safe — already-applied migrations are skipped.

### Large Table Migrations

For migrations that modify large tables (adding an index on a table with millions of rows), the migration uses PostgreSQL's `CREATE INDEX CONCURRENTLY` or equivalent non-blocking DDL. The migration runner detects these and executes them outside a transaction (as PostgreSQL requires for concurrent index creation).

### Managed Mode: Per-Org Migration Orchestration

In managed hosting, every org has its own database. Migrations must run against every org database, not just one. The migration orchestrator handles this:

1. **On deployment**: the deployment pipeline runs `ottercamp migrate --catalog` which iterates all active tenants in the catalog database and runs pending migrations against each org database. Migrations are parallelized up to a configurable concurrency limit (default: 10 databases at a time).
2. **Progress tracking**: the catalog database tracks each org's current schema version. If the orchestrator is interrupted, it resumes from where it left off — only un-migrated databases are processed.
3. **Failure handling**: if a migration fails for one org, the orchestrator logs the error and continues with other orgs. The failed org's database is not left in a broken state (transactions roll back) but is flagged for manual investigation.
4. **New org provisioning**: when a new org is created, the provisioner creates a fresh database, runs all migrations from scratch, and runs the bootstrap flow. The new database starts at the current schema version.

The catalog database itself has its own migration chain, separate from org database migrations. Catalog migrations are simple (the catalog schema is minimal) and run before org migrations.

## Backup and Restore

### What Needs to Be Backed Up

OtterCamp has two stateful components:

1. **PostgreSQL database**: all transactional data — users, agents, chats, projects, tasks, memory, audit events.
2. **Object storage**: artifacts, file uploads, screenshots, exported sessions, git repo archives.

### One-Command Backup

```bash
ottercamp backup --output backup-2026-02-22.tar.gz
```

This command:

1. Takes a `pg_dump` of the entire database (custom format, compressed).
2. Creates a tarball of all object storage content (filesystem mode) or uses the S3 API to list and download all objects (S3 mode).
3. Packages both into a single compressed archive with a manifest file that records:
   - OtterCamp version
   - Schema version
   - Timestamp
   - Object count and total size

### One-Command Restore

```bash
ottercamp restore --input backup-2026-02-22.tar.gz
```

This command:

1. Reads the manifest from the archive.
2. Checks compatibility: the current binary's schema version must be >= the backup's schema version.
3. Drops and recreates the database (with confirmation prompt, or `--force` flag).
4. Runs `pg_restore` from the dump.
5. Restores object storage content to the configured storage backend.
6. Runs any pending migrations (if the binary is newer than the backup).

### Managed Mode Backups

In managed mode, database-per-org isolation makes per-tenant backup natural:

- **Per-org database backup**: each org's database is an independent PostgreSQL database. Automated daily snapshots and point-in-time recovery are configured per database via the managed PostgreSQL provider.
- **Object storage**: S3 with versioning enabled. Per-org path prefixing (`s3://bucket/org-{org_id}/`) means each org's objects are independently restorable.
- **Tenant-level export**: an API endpoint allows an org owner to export their complete data (database dump + objects) as a portable archive. This is the same format as `ottercamp backup` produces. Since each org has its own database, the export is a straightforward `pg_dump` of the org's database plus its S3 prefix — no cross-tenant filtering needed.
- **Catalog database**: backed up separately. The catalog is small (one row per org) and recoverable from the set of provisioned databases if needed.

### Backup Schedule for Self-Host

The binary does not include a built-in backup scheduler. Self-hosters use cron, systemd timers, or their preferred scheduling tool:

```bash
# Daily backup via cron
0 2 * * * /usr/local/bin/ottercamp backup --output /backups/ottercamp-$(date +\%Y\%m\%d).tar.gz
```

A future enhancement may add a built-in backup scheduler, but cron is universal and well-understood.

## Upgrade Path

### Versioning

OtterCamp follows semantic versioning: `MAJOR.MINOR.PATCH`.

- **PATCH** (e.g., 1.2.3 to 1.2.4): bug fixes only. No schema changes. Drop-in replacement.
- **MINOR** (e.g., 1.2 to 1.3): new features, additive schema migrations. Backward-compatible — the previous version's binary can run against the new schema.
- **MAJOR** (e.g., 1.x to 2.x): breaking changes. May require manual migration steps. Upgrade guide published with every major release.

### Self-Host Upgrade Process

For minor version upgrades, the process is:

```bash
# Docker Compose
docker compose pull
docker compose up -d
# The new container starts, runs migrations, and serves the new version.

# Binary
# Download or install the new version
brew upgrade ottercamp
# Restart the service
systemctl restart ottercamp
# The new binary runs migrations on startup.
```

No manual migration steps. No downtime beyond the restart (which is typically under 10 seconds for migration + startup).

### Pre-Upgrade Validation

The `ottercamp migrate --dry-run` command shows what migrations would run without executing them. Self-hosters can run this against a copy of their database to validate before upgrading.

### Major Version Upgrades

Major version upgrades may include:

- Schema changes that are not backward-compatible (column renames, table restructuring).
- Changes to the configuration surface (deprecated env vars removed).
- Changes to the backup format.

Each major version ships with an upgrade guide that includes:

1. Prerequisites (minimum version to upgrade from).
2. Backup instructions.
3. Step-by-step upgrade procedure.
4. Post-upgrade verification steps.
5. Rollback procedure (restore from backup).

### Managed Mode Upgrades

In managed mode, upgrades are zero-downtime:

1. **Catalog migration**: the catalog database is migrated first (if needed). This is fast — the catalog schema is minimal.
2. **Per-org migration orchestration**: the deployment pipeline runs `ottercamp migrate --catalog` to iterate all org databases and run pending migrations. Parallelized up to the configured concurrency limit. Since migrations are backward-compatible for minor versions, the old binary can continue serving traffic against the new schema during this phase.
3. **Process rollout**: new API and Worker processes are deployed behind the load balancer. Health checks verify the new processes can connect to org databases and serve requests.
4. **Traffic shift**: traffic shifts to the new version after health checks pass.
5. **Drain**: old processes drain and terminate.

For orgs with large databases, the migration orchestrator can be configured to prioritize smaller databases first, ensuring the majority of tenants are migrated quickly while a few large tenants complete in the background.

## Secrets Management

### Categories of Secrets

OtterCamp manages several categories of secrets:

1. **Model provider API keys**: Anthropic, OpenAI, Google AI keys used for model inference.
2. **SSH keys**: for cloning and pushing to git remotes (see doc 03).
3. **MCP server credentials**: API tokens, OAuth credentials, and other authentication material for MCP connections (see doc 09).
4. **External service credentials**: any credentials agents need for tool execution (GitHub tokens, Slack tokens, email service credentials).

### Storage

Secrets are stored encrypted in PostgreSQL, in a dedicated `secret` table.

```sql
create table secret (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  name            text not null,               -- human-readable label ("Anthropic API Key", "GitHub SSH Key")
  slug            text not null,               -- machine-readable identifier ("anthropic_api_key")
  category        text not null check (category in ('model_provider', 'ssh_key', 'mcp_credential', 'external_service')),
  encrypted_value bytea not null,              -- AES-256-GCM encrypted
  created_by_type text not null check (created_by_type in ('human', 'system')),
  created_by_id   uuid,
  last_rotated_at timestamptz,
  expires_at      timestamptz,                 -- optional expiry
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),

  unique (organization_id, slug)
);

create index on secret (organization_id, category);
```

### Encryption

- **Encryption algorithm**: AES-256-GCM. Each secret is encrypted with a unique nonce.
- **Encryption key**: derived from a master key. The master key is the one secret that is NOT stored in PostgreSQL.
- **Master key sources** (in order of preference):
  1. `OTTERCAMP_MASTER_KEY` environment variable — a 32-byte hex-encoded key.
  2. A key file at a configurable path (`OTTERCAMP_MASTER_KEY_FILE`).
  3. In managed mode: a cloud KMS (AWS KMS, GCP KMS). The master key is a KMS key reference, and encryption/decryption calls go through the KMS API.
- **Key rotation**: when the master key is rotated, a background job re-encrypts all secrets with the new key. The old key is retained (in a key history) until re-encryption is complete.

### Runtime Decryption

Secrets are decrypted at runtime when needed:

- Model provider keys are decrypted when the model gateway makes an API call. The decrypted key is held in memory for the duration of the request, never written to disk or logs.
- SSH keys are decrypted when cloning or pushing a git repository. The key is written to a temporary file with restrictive permissions (`0600`), used for the git operation, and deleted immediately after.
- MCP credentials are decrypted when establishing or authenticating an MCP connection.
- Secrets are NEVER included in log output. The logging layer redacts any string that matches a known secret pattern.

### Self-Host vs Managed

In **self-host mode**, model provider API keys can be configured via environment variables (`ANTHROPIC_API_KEY`, etc.) for convenience. These are read at startup and stored in the `secret` table (encrypted). After bootstrap, the env vars are no longer needed — the encrypted database copy is the source of truth. The operator can remove the env vars from their compose file after first run if they prefer.

In **managed mode**, secrets are NEVER in environment variables. All secrets are managed through the application UI or API, stored encrypted in PostgreSQL, and decrypted at runtime via KMS. Environment variables are used only for infrastructure configuration (database URL, S3 endpoint), never for tenant-scoped secrets.

### Secret Lifecycle

- **Creation**: via the settings UI, CLI (`ottercamp secret set --name "Anthropic Key" --category model_provider`), or API.
- **Update**: the old value is overwritten (encrypted). The audit trail records that the secret was updated, but never logs the old or new value.
- **Deletion**: the row is hard-deleted. Audit event records the deletion.
- **Rotation**: update with a new value. An optional `last_rotated_at` timestamp tracks when the secret was last changed.
- **Expiry**: optional. If set, a daily job flags expired secrets and notifies the operator (or creates an inbox item).

## Health Checks

### Endpoints

Two health check endpoints, following Kubernetes conventions (but useful for any orchestrator or monitoring system):

#### GET /health (Liveness)

Returns 200 if the process is running and responsive. Does NOT check downstream dependencies. Used by process managers and load balancers to detect a hung or crashed process.

```json
{
  "status": "ok",
  "version": "1.3.2",
  "uptime_seconds": 86400
}
```

Returns 503 if the process is shutting down or in a fatal error state.

#### GET /ready (Readiness)

Returns 200 if the process is ready to serve traffic. Checks all critical dependencies:

```json
{
  "status": "ready",
  "checks": {
    "database": {"status": "ok", "latency_ms": 2},
    "object_storage": {"status": "ok", "latency_ms": 15},
    "migrations": {"status": "current", "version": 47}
  }
}
```

Returns 503 if any critical dependency is unavailable:

```json
{
  "status": "not_ready",
  "checks": {
    "database": {"status": "ok", "latency_ms": 2},
    "object_storage": {"status": "error", "message": "connection refused"},
    "migrations": {"status": "current", "version": 47}
  }
}
```

Critical dependencies checked by `/ready`:

- **Database**: can execute a simple query (`SELECT 1`).
- **Object storage**: can list/head the bucket (or check filesystem path exists and is writable).
- **Migrations**: schema version matches the binary's expected version.

Non-critical dependencies (model providers, browser service, MCP connections) are NOT checked by `/ready`. Their unavailability degrades functionality but does not make the instance unready to serve requests.

### Usage

- **Docker Compose**: `healthcheck` directive in the compose file uses `/health`.
- **Kubernetes** (when supported): liveness probe uses `/health`, readiness probe uses `/ready`.
- **Process managers** (systemd, Supervisor): check `/health` for restart decisions.
- **Load balancers**: use `/ready` to determine whether to route traffic to an instance.
- **Monitoring**: scrape both endpoints for alerting. Alert on `/ready` returning 503.

## TLS and Network Security

### Self-Host TLS Options

OtterCamp supports three approaches to TLS for self-hosted deployments:

1. **Reverse proxy (recommended)**: run Caddy, Nginx, or Traefik in front of OtterCamp. The proxy handles TLS termination, certificate renewal, and HTTPS redirects. OtterCamp listens on HTTP internally. This is the simplest and most flexible approach.

2. **Built-in ACME**: set `TLS_ACME_DOMAIN=ottercamp.example.com` and OtterCamp handles Let's Encrypt certificate issuance and renewal automatically. Requires port 443 to be accessible from the internet. Good for single-VPS deployments without a reverse proxy.

3. **Manual certificates**: set `TLS_CERT_FILE` and `TLS_KEY_FILE` to provide your own certificates. OtterCamp serves HTTPS directly. Certificate renewal is the operator's responsibility.

### Local Mode

In local mode (`localhost` access), TLS is not required. The server listens on HTTP. This is intentional — requiring TLS for local development adds friction with no security benefit.

### Managed Mode

TLS is terminated at the load balancer. Internal traffic between the load balancer and OtterCamp processes is over a private network. OtterCamp processes listen on HTTP internally.

## Managed Mode Specifics

### Database-per-Org Isolation

Every organization gets its own PostgreSQL database — even in managed hosting. There is no shared database between tenants (see 04-auth-tenancy-and-identity.md Database-Level Isolation). This is the strongest possible isolation guarantee: cross-tenant data leaks are architecturally impossible because there is no other tenant's data in the database to leak. No RLS is needed.

### Catalog Database

Managed mode introduces one additional database: the **catalog**. This is a small, shared database that maps tenants to their org databases. It is not part of the per-org application — it is infrastructure.

The catalog contains:

- **Tenant registry**: org slug, database connection string (host, port, database name), org status (active, suspended, deleted), creation timestamp, current schema version.
- **Slug uniqueness**: org slugs must be unique across all tenants. The catalog enforces this with a unique index — the per-org databases cannot enforce cross-tenant uniqueness.
- **Provisioning state**: tracks in-progress database provisioning to prevent races and enable retry on failure.

The catalog is the routing layer referenced in doc 04. It is simple, small (one row per org), and changes infrequently. It is not a performance bottleneck — routing lookups are cached in application memory with a short TTL.

### Tenant Resolution and Connection Routing

Every request in managed mode must resolve to an org database before any application logic runs:

1. **Extract org identifier**: from subdomain (`acme.ottercamp.dev`) or URL path (`/org/acme/...`).
2. **Look up in catalog**: map the org slug to a database connection. This lookup is cached in-process with a short TTL (e.g., 30 seconds). Cache misses hit the catalog database.
3. **Connect to org database**: the request's database connection is routed to the correct org database. All subsequent queries for this request use this connection.
4. **Connection pooling**: each API/Worker process maintains a connection pool per active org database. Idle pools are closed after a configurable timeout. A connection pooler (PgBouncer or built-in Go pool) manages connection limits across all org databases to prevent exhausting PostgreSQL connection slots.

Self-hosted mode skips all of this — there is one hardcoded database connection. The routing layer is a managed-mode-only component.

### Org Provisioning

When a new org is created in managed mode:

1. A new PostgreSQL database is created on the managed database cluster.
2. The pgvector extension is enabled in the new database.
3. All schema migrations run against the new database (bringing it to the current version).
4. The bootstrap flow runs (creates org, admin user, starter trio, General session).
5. The catalog is updated with the new org's database connection and schema version.
6. The org is ready to serve requests.

Provisioning is idempotent — if interrupted, it can be retried safely. The catalog tracks provisioning state to enable this.

### Per-Org Object Storage Isolation

Object storage uses per-org path prefixing: `s3://bucket/org-{org_id}/...`. Each org's objects are in a separate prefix. For additional isolation, per-org IAM policies can restrict access to only the org's prefix.

### Horizontal Scaling

The managed architecture supports horizontal scaling:

- **API processes**: stateless. Add more behind the load balancer. Session state is in per-org PostgreSQL databases, not in-process. The connection routing layer handles mapping requests to the correct org database.
- **Worker processes**: stateless. Add more to increase job throughput. Each org's PostgreSQL-backed job queue is independent — workers poll the catalog for active orgs and claim jobs from each org's database via `SELECT ... FOR UPDATE SKIP LOCKED`.
- **PostgreSQL**: each org database scales independently. Small orgs share a database cluster; large orgs can be moved to dedicated instances. The catalog tracks which cluster hosts each org. Read replicas per cluster for read-heavy queries.
- **Object storage**: S3 scales transparently.
- **Connection management**: the total number of org databases grows with the tenant count. Connection pooling (per-org pools with configurable max connections, idle timeout, and total connection ceiling) prevents exhausting database server resources.

### Tenant Resource Limits

In managed mode, per-org resource limits are enforced:

- **Model concurrency**: maximum concurrent LLM sessions per org (prevents one org from starving others).
- **Worker job concurrency**: maximum concurrent background jobs per org.
- **Storage quota**: maximum object storage usage per org.
- **API rate limits**: per-org rate limiting on API endpoints.

These limits are stored in `organization.settings` (jsonb) within each org's database and enforced by the application layer. The catalog may also carry limit overrides for operational control (e.g., temporarily throttling a noisy tenant without connecting to its database).

## Resolved Decisions

- **Kubernetes is NOT required for managed launch.** Start with Docker Compose + a process manager (systemd, Supervisor). K8s support is added when operational complexity (horizontal scaling, auto-healing, multi-region) justifies the infrastructure overhead. Premature K8s adoption adds complexity without proportional value at launch scale.
- **Minimum self-host footprint: single process + PostgreSQL.** Object storage falls back to local filesystem. Job queue and event bus use PostgreSQL. No Redis, no S3, no message broker required. PostgreSQL is the only external dependency.
- **No custom deployment overrides in V2.** Configuration via environment variables covers all cases. No Helm values files, no Terraform modules, no custom deployment scripts. If something can not be configured via env vars, it goes in the optional YAML config file.
- **Single Go binary is the distribution unit.** Docker wraps the binary for convenience. No interpreted language runtime needed. No JVM, no Node.js, no Python. The binary includes embedded migrations, static assets for the web UI, and all application code.
- **Automatic schema migration on startup.** No manual migration steps for minor version upgrades. The binary knows what schema version it needs and brings the database up to date automatically. Operators who want manual control can use `ottercamp migrate` separately.
- **PostgreSQL is the ONLY required external dependency for self-host.** Everything else has a fallback: object storage falls back to local filesystem, job queue uses PostgreSQL LISTEN/NOTIFY + polling, event bus uses PostgreSQL. This is a deliberate constraint — every additional required service increases the barrier to self-hosting.
- **Environment variables for all configuration.** No mandatory config files. The YAML config file is optional, for complex structures only. Env vars always override the config file.
- **Secrets stored encrypted in PostgreSQL, not in environment variables (managed mode).** Self-host can bootstrap from env vars for convenience, but the encrypted database copy becomes the source of truth. Managed mode never uses env vars for tenant secrets.
- **Forward-only migrations.** No rollback migrations. Fixes go forward. This eliminates the class of bugs where rollback scripts are untested or out of sync.
- **Health checks follow the liveness/readiness pattern.** `/health` for process liveness (no dependency checks), `/ready` for traffic readiness (checks database, storage, migrations). Non-critical dependencies do not affect readiness.
- **Model provider API keys are the only truly required user configuration.** Everything else has defaults. A developer can run OtterCamp with just an Anthropic key and a local PostgreSQL.
- **Split mode is optional, not required.** Single process runs everything by default. Split API and Worker processes are available for VPS operators who want resource isolation but are not required.
- **Docker Compose is the primary self-host packaging.** It is not the only option — the binary runs independently — but Docker Compose is what we optimize for, document first, and test most thoroughly.
- **Master key for secrets encryption is the one external secret.** It is provided via environment variable or key file, never stored in the database. In managed mode, it is a KMS key reference.
- **TLS is the operator's responsibility in self-host.** We provide three options (reverse proxy, built-in ACME, manual certs) but do not mandate a specific approach. Local mode does not require TLS.
- **Managed mode uses database-per-org isolation, not RLS.** Every org gets its own PostgreSQL database, consistent with doc 04's tenancy model. No shared databases, no row-level security. Cross-tenant data leaks are architecturally impossible. A catalog database maps org slugs to database connections.
- **Catalog database is infrastructure, not application data.** The catalog is a small shared database that tracks tenants and their database connections. It is not part of the per-org application. One row per org, cached aggressively, changes infrequently.
- **Per-org migration orchestration for managed mode.** Migrations must run against every org database. The orchestrator parallelizes this (configurable concurrency), tracks progress in the catalog, and handles failures per-org without blocking others.
- **Connection pooling per-org with a global ceiling.** Each API/Worker process maintains connection pools to active org databases. Idle pools are closed after a timeout. A global connection ceiling prevents exhausting PostgreSQL connection slots across all tenants.
- **pgvector is a required PostgreSQL extension.** The memory system (doc 06) uses `vector(1536)` columns for embeddings. Docker images use `pgvector/pgvector:pg16` instead of vanilla PostgreSQL. Managed database providers must have pgvector enabled.

## Open Questions

_None currently outstanding._

## Resolved Open Questions

These were previously open and have been resolved:

- **Automatic backup scheduling**: cron is sufficient. The binary does not include a built-in backup scheduler. Cron is universal, well-understood, and avoids adding maintenance surface. Documented in the Backup section with a cron example.
- **Telemetry and update checks**: deferred to post-GA. Both are polarizing. The binary does not phone home. A `ottercamp version --check` command can check for updates on demand, but automatic checks are opt-in only.
- **Embedded PostgreSQL**: no. The binary size increase (~100MB) and maintenance burden of bundling PostgreSQL outweigh the convenience. PostgreSQL is available via Docker, Homebrew, and system packages on all platforms. The Docker Compose two-container setup is the zero-configuration path.
- **Multi-architecture Docker images**: `linux/amd64` and `linux/arm64` for Docker. Binary distribution covers `darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`. These four cover the vast majority of deployment targets.
