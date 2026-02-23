# 020: MCP Connection Schema and Service

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | M (1–2 days) |
| Spec refs | doc 09 §MCPConnection, doc 09 §MCPToolCatalog, doc 09 §MCPSecretBinding, doc 09 §ConnectionLifecycle |
| Spec status | finished |
| Depends on | 003, 009, 016, 013 |
| Blocks | 021, 022, 049, 055 |

## Scope

Build the schema, repository, and service layers for the MCP domain: `mcp_connection`,
`mcp_tool_catalog`, and `mcp_secret_binding` tables, plus the connection lifecycle service
and catalog refresh logic. Circuit breaker and health check scheduler live in task 021.

### Must build

**Migrations:**
- `0022_mcp_connection.sql`
- `0023_mcp_tool_catalog.sql`
- `0024_mcp_secret_binding.sql`

**`mcp_connection` table** (doc 09):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `project_id uuid references project(id) on delete cascade` — null = org-scoped (available to all agents in org)
- `display_name text not null`
- `slug text not null` — URL-safe identifier; unique within org
- `transport text not null check (transport in ('stdio','http','sse'))` — connection transport type
- `transport_config jsonb not null default '{}'` — transport-specific config; may contain `ref:<slug>` inline secret references
- `status text not null default 'configuring' check (status in ('configuring','active','degraded','failed'))` — connection lifecycle
- `is_enabled boolean not null default true`
- `last_healthy_at timestamptz` — updated on successful health check
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(organization_id, slug)`
- Index: `(organization_id, status)`
- Index: `(project_id) WHERE project_id IS NOT NULL`

**`mcp_tool_catalog` table** (doc 09):
- `id uuid primary key default gen_random_uuid()`
- `connection_id uuid not null references mcp_connection(id) on delete cascade`
- `tool_name text not null` — name as reported by the MCP server
- `description text not null default ''` — from MCP server's tool manifest
- `input_schema jsonb not null default '{}'` — JSON Schema for tool input
- `is_enabled boolean not null default false` — **default-deny**: tools are disabled until explicitly enabled
- `metadata jsonb not null default '{}'`
- `discovered_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(connection_id, tool_name)`
- Index: `(connection_id, is_enabled)`

**`mcp_secret_binding` table** (doc 09):
- `id uuid primary key default gen_random_uuid()`
- `connection_id uuid not null references mcp_connection(id) on delete cascade`
- `secret_ref text not null` — `ref:<secret-slug>` format; resolved at call time via `SecretService.ResolveRef`
- `env_var_name text not null` — environment variable name injected into the MCP transport process
- `created_at timestamptz not null default now()`
- Unique constraint: `(connection_id, env_var_name)`

**Repository layer:**
- `MCPConnectionRepo`: `Create`, `GetByID`, `GetBySlug`, `List`, `SetStatus`, `Enable`, `Disable`, `UpdateLastHealthy`
- `MCPToolCatalogRepo`: `BulkUpsert`, `GetByID`, `ListByConnection`, `Enable`, `Disable`, `GetEnabled`
- `MCPSecretBindingRepo`: `Create`, `GetByConnection`, `Delete`

**`internal/mcp/service.go` — `MCPService` interface and implementation:**

```go
type MCPService interface {
    CreateConnection(ctx context.Context, req CreateConnectionRequest) (*MCPConnection, error)
    GetConnection(ctx context.Context, orgID, connID uuid.UUID) (*MCPConnection, error)
    ListConnections(ctx context.Context, orgID uuid.UUID, filter MCPConnectionFilter) ([]*MCPConnection, error)
    UpdateConnection(ctx context.Context, orgID, connID uuid.UUID, req UpdateConnectionRequest) (*MCPConnection, error)
    DeleteConnection(ctx context.Context, orgID, connID uuid.UUID) error

    // Catalog management
    RefreshCatalog(ctx context.Context, connID uuid.UUID) error
    EnableTool(ctx context.Context, connID, entryID uuid.UUID) error
    DisableTool(ctx context.Context, connID, entryID uuid.UUID) error

    // Credential resolution
    ResolveSecretBindings(ctx context.Context, connID uuid.UUID) (map[string]string, error)  // env_var_name → decrypted_value
}
```

**Connection status lifecycle:**
- `configuring → active`: triggered by first successful `RefreshCatalog`
- `active → degraded`: set by circuit breaker (task 021) when call failures exceed threshold
- `degraded → active`: set by circuit breaker on recovery
- `degraded → failed`: set by circuit breaker after max failures
- `failed → configuring`: manual re-enable via `PATCH /v1/mcp/connections/:id` with `{"is_enabled": true}` (resets status to configuring; next health check advances to active)

**`RefreshCatalog` implementation:**
- Connect to MCP server via configured transport (stub transport implementation — real transport in task 021)
- Call MCP `tools/list` method (or equivalent per transport)
- `MCPToolCatalogRepo.BulkUpsert` with returned tool manifests
- New tools discovered: inserted with `is_enabled=false` (default-deny)
- Existing tools: update `description` and `input_schema`; preserve `is_enabled` value
- Removed tools (in catalog but not in new manifest): set `is_enabled=false` (do not delete)
- Emit `mcp.catalog.changed` domain event with `{connection_id, added_count, updated_count, removed_count}`
- Set `status='active'` if was `configuring`

**`ResolveSecretBindings`:**
- Query `mcp_secret_binding` rows for the connection
- For each binding, call `SecretService.ResolveRef(ref)` to decrypt the value
- Return map of `env_var_name → plaintext_value`
- Errors from `ResolveRef` (missing secret, decryption failure) bubble up; caller handles

**`transport_config` inline secret resolution:**
- `transport_config` may contain `ref:<slug>` values (e.g. `{"api_key": "ref:my-mcp-api-key"}`)
- At connection time, call `SecretService.ResolveRef` for each `ref:` value found in the config
- This resolution is in-memory only; never write the resolved value back to the DB

### Must NOT build
- MCP circuit breaker and health check scheduler (task 021)
- MCP API endpoints (task 022)
- MCP execution log (task 055)
- Tool resolution pipeline integration (task 049)

## Acceptance Criteria

- [ ] Migrations `0022`–`0024` apply cleanly
- [ ] `mcp_tool_catalog.is_enabled` defaults to `false` (default-deny); newly discovered tools are not enabled
- [ ] `mcp_connection` unique constraint on `(organization_id, slug)` enforced
- [ ] `RefreshCatalog` on connection with no existing catalog: inserts all discovered tools as `is_enabled=false`
- [ ] `RefreshCatalog` on connection with existing catalog: preserves `is_enabled` for existing tools; adds new tools as disabled; marks removed tools as disabled
- [ ] `RefreshCatalog` emits `mcp.catalog.changed` domain event
- [ ] `ResolveSecretBindings` calls `SecretService.ResolveRef` for each binding; returns env var map
- [ ] `DeleteConnection` cascades to `mcp_tool_catalog` and `mcp_secret_binding` rows

## Tests Required

**Unit tests:**
- `RefreshCatalog` diff logic: given existing catalog `[A, B, C]` and new manifest `[B, C, D]` → A is disabled, D is added disabled, B/C unchanged
- `ResolveSecretBindings`: mock `SecretService.ResolveRef`; verify map constructed correctly; one binding fails → error propagated
- `transport_config` `ref:` resolution: config `{"key": "ref:my-secret"}` → resolved to plaintext; non-ref values unchanged

**Integration tests:**
- `MCPConnectionRepo` CRUD + status transitions
- `MCPToolCatalogRepo.BulkUpsert`: insert 5 tools; call again with 4 (1 removed, 1 new) → verify counts
- `RefreshCatalog` (with stub transport returning a fixed tool list): verify catalog rows in DB
- `MCPSecretBindingRepo`: create binding → `ResolveSecretBindings` returns correct map
- Cascade delete: delete connection → verify tool catalog and secret binding rows deleted

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

- ISSUE #15 (AMBIGUOUS): MCP resource reads are tier 1 and governed by "a basic scope check — does the agent have access to this connection's project?" The exact check is unspecified. Implement as: if `connection.project_id IS NULL` (org-scoped), all org agents have access; if `connection.project_id IS NOT NULL`, check `agent_project_assignment` (task 025) for the agent + project. Document this decision in a code comment referencing ISSUE #15.
- The transport stub in this task should implement the `MCPTransport` interface with a `MockTransport` that returns a hardcoded tool list. The real transport implementations (stdio, http, sse) are wired in task 021.
- `mcp_connection.transport_config` is a jsonb blob; the structure varies by transport type. Do not add DDL constraints on its shape — validate at the service layer based on `transport`.
- The `slug` field on `mcp_connection` follows the same `^[a-z0-9-]+$` pattern as project slugs. Add a `CHECK` constraint in the migration.
- When `RefreshCatalog` is called on a `failed` connection, return `ErrConnectionFailed` and do not attempt the transport call. The connection must be re-enabled first.
