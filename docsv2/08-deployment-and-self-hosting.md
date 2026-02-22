# 08. Deployment and Self-Hosting

## Supported Modes

- Local single-node (developer laptop)
- VPS single-tenant (self-host)
- Managed multi-tenant (hosted by us)

## Core Services

- API service
- Worker service
- Postgres
- Object storage (S3-compatible)
- Optional queue backend (Redis/Postgres queue)

## Packaging

- Docker Compose for local and simple VPS installs.
- Helm/Kubernetes profile for managed deployments.
- Versioned migration bundle for database upgrades.

## Configuration

- Environment-based runtime config.
- Secret injection via env or secret manager.
- Per-instance feature flags.

## Backup and Restore

- DB logical backup schedule.
- Object storage snapshot/versioning.
- One-command restore playbook.

## Upgrade Strategy

- Backward-compatible schema migrations first.
- Blue/green or rolling update for managed mode.
- Explicit upgrade guide for self-host users.

## Open Questions

- Is Kubernetes required for managed launch, or can we start with simpler orchestration?
- What is the minimum supported self-host footprint?
- How do we version and support custom deployment overrides?

