---
## Summary

This spec defines how OtterCamp V2 handles authentication, tenancy, and identity for both humans and AI agents. The **organization** is the top-level isolation boundary, enforced at the **database level** — every org gets its own PostgreSQL database, even in managed hosting. There is no shared database between tenants and no cross-tenant data access. Two deployment modes are supported: self-hosted (one instance, one database, one org) and managed hosting (shared application infrastructure, separate database per org with a routing layer for tenant resolution).

There are two kinds of first-class identities: **human users** and **agents**. Both live in the org's database — there are no cross-database identities. V2 launches with one human per org; multi-user is a near-term addition with four RBAC roles (owner, admin, member, viewer) on the `human_user` table. Both identity types are referenced throughout the system via a polymorphic **principal** convention using `(principal_type, principal_id)` — there is no unified principal table. A system actor uses a sentinel UUID (`00000000-...`) for automated actions. Delegation is tracked so that when a human authorizes an agent action, both the performer and the delegator are recorded in the audit trail.

Authentication for V2 GA is email + password (bcrypt, work factor 12) with server-side sessions (30-day sliding window, hashed tokens). A password-less local auth mode is available for self-hosted deployments. API keys provide programmatic access with the format `oc_<scope>_<random>`, stored as SHA-256 hashes, with three permission scopes: `full`, `read`, and `chat`. SSO/OIDC is deferred but the schema includes hooks for future support. Agents do not authenticate themselves — the platform asserts their identity via execution context. The spec defines five database tables: `organization`, `human_user`, `auth_session`, `api_key`, and `audit_event` (plus `agent`, whose schema lives in doc 05). Bootstrap creates the org, the first owner user, the starter trio agents (Frank, Lori, Ellie), and the General chat session, and is idempotent. Three CLI commands (`reset-password`, `magic-link`, `unlock-account`) handle account recovery for self-hosted deployments.

---

# 04. Auth, Tenancy, and Identity

## Goals

- Provide strong, org-level data isolation for all entities in the system.
- Support both human and agent identities as first-class principals.
- Keep the authorization model simple: coarse RBAC roles for access, fine-grained capabilities for tool/action permissions (defined in 16-agent-control-plane.md).
- Launch with single-user-per-org but design for multi-user from day one — it is a near-term addition, not a distant roadmap item.
- Enable programmatic API access alongside interactive sessions.
- Ensure every mutation in the system is traceable to a specific principal.

## Tenancy Model

### Organization as Isolation Boundary

The `organization` is the top-level tenant in OtterCamp. Every significant entity in the system carries an `organization_id` foreign key. There is no data sharing between organizations — they are hermetically sealed.

V2 launches with a single human operator per org, but multi-user support is a near-term addition — the schema is ready for it from day one (see `human_user.role` with four RBAC roles). The product is designed around a primary operator managing AI agents, with additional humans joining as collaborators shortly after launch.

### Database-Level Isolation

Every organization gets its own database. There is no shared database between orgs — not even in the managed hosting model. This is the strongest possible isolation guarantee:

1. **One org, one database**: the database contains exactly one organization's data. There is no `organization_id` filtering needed on queries because there is only one org's data present. Cross-tenant data leaks are architecturally impossible — there is no other tenant's data in the database to leak.

2. **CLI safety**: operators use CLI tools that connect directly to their database. With database-level isolation, there is no way for a CLI session to break out of its sandbox and access another tenant's data, even through SQL injection, query bugs, or misconfigured tooling.

3. **No RLS needed**: row-level security is unnecessary because the isolation boundary is the database itself, not rows within a shared database.

The application still carries `organization_id` on tables for structural consistency and to support the principal convention, but it is not a security boundary — the database boundary is.

### What Carries organization_id

Most domain entities carry `organization_id` directly: `human_user`, `agent`, `chat_session`, `project`, `flow_template`, `memory`, `audit_event`, and others. Some entities omit it because they are always accessed through a parent that carries it (e.g., `chat_message` via `chat_session`, `auth_session` and `api_key` via `human_user`).

These columns exist for referential integrity and query consistency, not for tenant isolation (which is handled at the database level).

### Deployment Modes

OtterCamp supports two deployment modes (see 08-deployment-and-self-hosting.md):

- **Self-hosted**: one instance, one database, one org. The operator runs OtterCamp on their own infrastructure. The org is created during bootstrap.
- **Managed hosting**: shared application infrastructure, separate database per org. Each tenant gets their own PostgreSQL database. The routing layer resolves the tenant from the request and connects to the correct database. Tenants are fully isolated — no shared state, no shared data, no cross-tenant queries.

The schema is identical in both cases. The difference is operational: self-hosted instances have a single hardcoded database connection; managed instances use a routing layer to map tenants to databases.

### Managed Hosting Routing Layer

In managed hosting, every request must resolve to a tenant database before any application logic runs. The routing layer is a thin lookup that maps the tenant identifier (org slug from subdomain or URL path) to a database connection. This layer is outside the per-org application — it is infrastructure, not part of the org's data. It enforces cross-tenant slug uniqueness and handles tenant provisioning (creating a new database for a new org). The routing layer's design is deferred to the managed hosting spec.

## Human Identity

### The human_user Entity

A `human_user` represents a real person who interacts with OtterCamp. Human users live in the org's database — they are scoped to the org, just like agents. If a person participates in multiple orgs (future, distant), they would have separate `human_user` records in each org's database.

V2 launches with one human user per org — the operator. The schema supports multiple humans per org with RBAC roles (the `role` column on `human_user`) for near-term multi-user expansion.

### Authentication Methods

V2 ships with two authentication methods:

**1. Email + password (primary)**

The operator creates an account with an email and a bcrypt-hashed password. This is the default path for self-hosted and managed deployments. Simple, no external dependencies.

Password requirements: minimum 12 characters. No complexity rules — length is the best predictor of password strength. Bcrypt with a work factor of 12.

**2. API key (programmatic access)**

For CLI tools, scripts, and external integrations. API keys are long-lived bearer tokens scoped to an org. See the API Keys section below.

**Deferred: SSO/OIDC**

SSO via OIDC (Google, GitHub, etc.) is deferred to post-GA. The `human_user` schema includes a nullable `external_auth_provider` and `external_auth_id` to support this without migration. When SSO is added, a user can link their OtterCamp identity to an external provider. The schema is ready; the implementation is not V2 GA scope.

### Session Management

When a human logs in (via email/password), the system creates a session. Sessions are server-side, stored in the database.

- **Session token**: a cryptographically random opaque token (256-bit, base64url-encoded). Sent as an `HttpOnly`, `Secure`, `SameSite=Lax` cookie for web clients, or as a `Bearer` token in the `Authorization` header for API/CLI clients.
- **Session lifetime**: 30 days by default. Configurable per instance. Extended on each active use (sliding window).
- **Session revocation**: explicit logout, password change, or admin action. When a session is revoked, the token is immediately invalid.
- **Concurrent sessions**: unlimited. A user can be logged in from multiple devices/browsers simultaneously. Each is an independent session.
- **Cleanup**: a daily job (via the worker process, see 12-api-events-and-realtime.md) purges expired and revoked sessions.

Sessions live in the org's database. Since each org has its own database, sessions are inherently org-scoped — there is no cross-org session concept.

### Password Reset

Self-hosted: the operator has direct server access, so account recovery is handled via CLI commands. No email infrastructure required.

- `ottercamp reset-password --email <email>` — prompts for a new password and updates the hash directly.
- `ottercamp magic-link --email <email>` — generates a time-limited, single-use login URL. Prints it to stdout. Useful for when you forget your password or want to log in from a new device without typing credentials. Token expires after 15 minutes and is invalidated after one use.
- `ottercamp unlock-account --email <email>` — resets the failed login attempt counter (see Rate Limiting below).

Managed: standard email-based password reset and magic link flows with time-limited, single-use tokens. Implementation details deferred to the managed hosting spec.

## Agent Identity

### The agent Entity

An `agent` is an AI entity that performs work in OtterCamp. Agents are first-class identities — they are not sub-accounts of human users. They have their own identity, their own permissions, and their own audit trail.

Agents are always scoped to an organization. An agent cannot exist outside an org, and an agent cannot belong to multiple orgs. Like human users, agents are born into an org's database and live there.

The full agent profile (prompt pack, tool policy, model policy, memory policy, lifecycle states) is defined in 05-agents-staff-and-temps.md. This document covers only the identity and authentication aspects.

### Agent Authentication

Agents do not authenticate the way humans do. They do not log in. Instead, agents are authenticated by the system itself:

- When the turn loop (see 02-chat.md) dispatches work to an agent, the system constructs an execution context that includes the agent's identity (`agent_id`, `organization_id`).
- When the control plane (see 16-agent-control-plane.md) evaluates a policy for an agent action, it receives the agent's principal identity from this context.
- Agents never hold credentials that they present to OtterCamp. The system knows who the agent is because the system created the execution context.

This is simpler and more secure than issuing tokens to agents. There is no agent credential to leak, rotate, or manage. The agent's identity is asserted by the platform, not claimed by the agent.

### Agent Credentials for External Systems

When agents need to call external services (via MCP connections or direct API calls), the credentials for those services are managed by the secrets system (see 13-security-observability-costs.md). The agent never sees the raw credential — the control plane resolves secret references at execution time and injects them into the sandboxed environment.

## The Principal Concept

Both humans and agents act on the system, and the system needs a uniform way to identify "who did this." OtterCamp uses the **principal** abstraction for this.

A principal is identified by two fields:

- `principal_type`: `human` or `agent`
- `principal_id`: the UUID of the `human_user` or `agent` record

This pattern appears everywhere:

- **Audit events**: who performed this action?
- **Chat messages**: who authored this message? (`author_type` / `author_id` in 02-chat.md)
- **Task events**: who changed this status? (`actor_type` / `actor_id` in 03-projects-and-task-flow.md)
- **Control plane requests**: who is requesting this capability? (`principal_type` / `principal_id` in 16-agent-control-plane.md)
- **Created-by fields**: who created this entity?

The principal is not a separate table — it is a polymorphic convention. The application layer validates that the `principal_id` exists in the correct table based on `principal_type`. This is a deliberate choice: a unified `principal` table would add a join to every query and provide no value beyond what the convention already gives.

### System as Actor

Some actions are performed by the system itself, not by a human or agent. Examples: scheduled task creation, automatic flow advancement after dependency resolution, merge queue processing, session cleanup.

For these, the convention is `actor_type = 'system'` with `actor_id` set to a well-known sentinel UUID (all zeros: `00000000-0000-0000-0000-000000000000`). This keeps the principal convention intact — every action has an actor — without pretending a human or agent did it.

### Delegation

When a human explicitly authorizes an agent to perform an action (e.g., approving a capability request in the inbox), the audit trail records both principals:

- `principal_type` / `principal_id`: the agent who performed the action.
- `delegated_by`: the human who authorized it.

This is captured in the control plane's principal model (see 16-agent-control-plane.md). The delegation trace is critical for security auditing — you can always answer "who actually approved this?"

## Authorization Model

### Design Philosophy

OtterCamp uses a two-layer authorization model:

1. **RBAC roles** for coarse access control: can this human access this org? Can they manage agents? Can they view projects?
2. **Capabilities** for fine-grained action permissions: can this agent execute CLI commands? Can it invoke this MCP tool? Can it advance a flow?

RBAC answers "what can this person do in general." Capabilities answer "what can this principal do specifically." The capability model is fully defined in 16-agent-control-plane.md; this document defines the role foundations that capabilities build on.

### RBAC Roles

Roles are assigned via the `role` column on `human_user`. A human's role determines their coarse permissions:

| Role | Description | Typical Use |
|------|-------------|-------------|
| `owner` | Full control. Can manage billing, delete the org, manage all members. | The operator (always). |
| `admin` | Can manage agents, projects, settings. Cannot delete the org or manage billing. | Trusted collaborator (future). |
| `member` | Can use the system: chat, view projects, interact with agents. Cannot change settings or manage agents. | Day-to-day user (future). |
| `viewer` | Read-only access. Can view projects, tasks, chat history. Cannot send messages or trigger actions. | Observer (future). |

For V2 GA, only `owner` matters — the single operator is always the owner. The other roles exist in the schema so multi-user support does not require a migration.

### Role-Based Permission Matrix

| Permission | Owner | Admin | Member | Viewer |
|------------|-------|-------|--------|--------|
| Manage org settings | Yes | Yes | No | No |
| Manage billing | Yes | No | No | No |
| Delete org | Yes | No | No | No |
| Manage members | Yes | Yes | No | No |
| Manage agents | Yes | Yes | No | No |
| Create projects | Yes | Yes | Yes | No |
| View projects/tasks | Yes | Yes | Yes | Yes |
| Chat with agents | Yes | Yes | Yes | No |
| Act on inbox items | Yes | Yes | Yes | No |
| View audit log | Yes | Yes | No | No |
| Manage API keys | Yes | Yes | No | No |

This matrix is enforced in the API layer. Each endpoint checks the requesting human's role before processing. The check is simple: load the `human_user` row from the authenticated session and compare `role` against the required permission.

### Agent Authorization

Agents do not have RBAC roles. Agents have **capabilities** — namespaced permissions that define exactly what they can do. The capability model is defined in 16-agent-control-plane.md and evaluated by the control plane's policy engine.

Key points from doc 16 relevant here:

- Capabilities are namespaced: `project.task.read`, `system.cli.execute`, `mcp.tool.invoke:<connection_id>:<tool_name>`.
- Policy evaluation outcomes: `allow`, `deny`.
- Policy layers (highest priority first): instance safety > org policy > project policy > agent profile policy > request-specific overrides.
- Default posture is **permissive via templates** — agents receive generous capability grants from templates (reader, worker, deployer, admin). Instance safety (layer 1) is the only hardcoded deny layer. Admins add deny rules when they want restrictions.

The relationship between docs 04 and 16: this document defines *who* agents and humans are (identity). Doc 16 defines *what agents can do* (capabilities and policy). RBAC roles (this doc) gate human access. Capabilities (doc 16) gate agent actions.

## API Keys

### Purpose

API keys enable programmatic access to OtterCamp for CLI tools, scripts, webhooks, and external integrations. They authenticate as a specific human user within the org.

### Key Properties

- **Scoped to org**: an API key lives in the org's database. Cross-org access is architecturally impossible.
- **Tied to a user**: an API key acts on behalf of a specific human user. Actions performed via the key are attributed to that user in the audit trail.
- **Permission level**: API keys carry a permission scope that caps their access. The scope cannot exceed the user's RBAC role. Available scopes:
  - `full`: same permissions as the user's role. For trusted scripts and admin tools.
  - `read`: read-only access. For monitoring, dashboards, and reporting.
  - `chat`: can send messages and read chat history. For chat integrations.

### Key Format

API keys are prefixed for easy identification and secret scanning:

- Format: `oc_<scope>_<random>` (e.g., `oc_full_a1b2c3d4e5f6...`)
- The prefix (`oc_full_`, `oc_read_`, `oc_chat_`) makes it immediately obvious what kind of key it is.
- The random portion is 32 bytes, base62-encoded (alphanumeric, no special characters).
- The full key is shown once at creation time. OtterCamp stores only a SHA-256 hash of the key.

### Key Lifecycle

- **Creation**: via the settings UI or CLI. The human specifies a name (for identification) and scope.
- **Display**: the full key is shown exactly once. After that, only the last 4 characters are visible.
- **Revocation**: immediate. The key hash is deleted. Any in-flight requests with the old key fail.
- **Expiry**: optional. Keys can have an expiry date. Expired keys are automatically revoked by a daily cleanup job.
- **Rotation**: not automatic. The human creates a new key and revokes the old one. A grace period (both keys valid simultaneously) is supported by simply creating the new key before revoking the old one.

### Rate Limiting

API key requests are rate-limited per key and per org. Default limits:

- Per key: 60 requests/minute for mutations, 300 requests/minute for reads.
- Per org: 600 requests/minute aggregate across all keys.

Limits are configurable per org. Rate limit headers (`X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`) are included on every response.

## Bootstrap Flow

### What Happens on First Install

When OtterCamp starts for the first time with an empty database, the bootstrap sequence runs:

1. **Run migrations**: create all database tables.

2. **Create organization**: a default organization is created. In self-hosted mode, this is the only org. The name defaults to "My Organization" and can be changed later.

3. **Create first user**: the operator provides an email and password (via CLI prompt or environment variables). This user is created as the `owner` of the default org.

4. **Seed starter trio**: the three foundational agents are created in the org:
   - **Frank** (Chief of Staff): org-level, the human's primary touchpoint. Default responder in the org session.
   - **Lori** (Agent Relations Expert): handles staffing, hiring agents for projects.
   - **Ellie** (Memory): dual role — background memory infrastructure AND conversational agent for memory queries.

   Each agent is created with its identity metadata, prompt pack, default tool policy, and default model policy as defined in 05-agents-staff-and-temps.md.

5. **Seed provider connections** — if `OTTERCAMP_MODEL_PROVIDER` and `OTTERCAMP_MODEL_API_KEY` are set, create the provider connection, encrypt the API key, and create default model profiles (agent and system). See `08-deployment-and-self-hosting.md` for the full provider connection bootstrap sub-steps. The starter trio's `default_model_profile_id` is back-filled to point to the newly created profiles.

6. **Create org session**: the persistent org-level chat session ("General") is created with Frank as the default responder. The human, Frank, Lori, and Ellie are added as participants.

7. **Record bootstrap event**: an audit event records that bootstrap completed, including the org ID, user ID, and agent IDs created.

### Bootstrap is Idempotent

If OtterCamp starts and the database already has an org and user, the bootstrap sequence is skipped. This is detected by checking for the existence of any `organization` row.

**Note on upgrades**: bootstrap only runs on first install. When a new OtterCamp version ships with updated prompt packs or policies for the starter trio, those updates are applied through the migration system, not bootstrap. See 15-migration-and-backward-compat.md for the upgrade path for system-managed agent profiles.

### CLI Bootstrap Command

For self-hosted deployments:

```
ottercamp bootstrap --email operator@example.com --password <password> --org-name "My Workspace"
```

All flags are optional — if omitted, the CLI prompts interactively. Environment variables (`OTTERCAMP_ADMIN_EMAIL`, `OTTERCAMP_ADMIN_PASSWORD`, `OTTERCAMP_ORG_NAME`) are also supported for automated deployments.

## Auditability

### Every Action Has an Actor

Every write operation in OtterCamp records who performed it. This is not optional — the system will not accept a mutation without a principal identity in the request context.

The principal is captured through two patterns:

1. **Inline on the entity**: tables carry `created_by_type` / `created_by_id` (and sometimes `updated_by_type` / `updated_by_id`) for creation and last modification.
2. **Audit event log**: a dedicated `audit_event` table records security-sensitive and system-critical actions with full context.

### What Gets an Audit Event

Not every database write produces an audit event — that would be noise. Audit events are reserved for actions that matter for security, compliance, and debugging:

- **Authentication**: login, logout, failed login attempt, session creation, session revocation.
- **Identity management**: user created, user updated, password changed, API key created, API key revoked.
- **Agent lifecycle**: agent created, agent activated, agent paused, agent retired.
- **Authorization changes**: role changed, capability granted, capability revoked, policy updated.
- **Sensitive operations**: org settings changed, project deleted, data exported, data redacted.
- **Control plane decisions**: policy evaluated (with outcome), approval granted, approval denied.
- **System events**: bootstrap completed, migration run, scheduled maintenance.

Routine operations (sending a chat message, reading a task, viewing a project) are captured by the domain's own event trail (e.g., `chat_message` records, `project_task_event` records) and do not produce audit events.

### Audit Event Structure

Each audit event captures:

- **Who**: principal type and ID (human, agent, or system).
- **What**: action type (e.g., `auth.login`, `agent.created`, `policy.evaluated`).
- **Where**: organization ID, and optionally project/task/session scope.
- **When**: timestamp with timezone.
- **Context**: a jsonb payload with action-specific details (IP address for auth events, old/new values for changes, policy evaluation inputs/outputs).
- **Delegation**: if the action was performed by an agent on behalf of a human, the delegating human's ID.

### Retention and Immutability

Audit events are **append-only**. They are never updated or deleted during normal operation. Retention policy is configurable per org (default: 1 year for self-hosted, longer for managed). Expired events are archived to object storage before deletion.

## Database Schema

### organization

```sql
create table organization (
  id          uuid primary key default gen_random_uuid(),
  name        text not null,
  slug        text not null unique,       -- url-safe identifier, unique across instance
  settings    jsonb not null default '{}', -- org-level configuration (retention, limits, etc.)
  status      text not null default 'active' check (status in ('active', 'suspended', 'deleted')),
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);
```

**Design notes:**
- `slug` is the URL-safe identifier used in paths and API requests. Unique within the database (trivially, since there's one org per database). For managed hosting, slug uniqueness across tenants is enforced by the routing layer. Set during bootstrap or org creation.
- `settings` is a jsonb bag for org-level config. Keeps the schema stable as configuration surface grows. New keys can be added without migration. `null` values mean "no limit" or "use system default." Expected top-level shape:
  ```json
  {
    "retention": {
      "audit_events_days": 365,
      "chat_messages_days": null,
      "completed_tasks_days": null
    },
    "budgets": {
      "monthly_cost_limit_usd": null,
      "monthly_cost_warning_usd": null
    },
    "models": {
      "default_profile_id": "<uuid>"
    },
    "notifications": {
      "inbox_digest_enabled": false
    },
    "auth": {
      "session_lifetime_days": 30,
      "api_rate_limit_per_key": 60,
      "api_rate_limit_per_org": 600
    }
  }
  ```
- `status`: `active` is normal operation. `suspended` is used by managed hosting to disable an org (billing issue, policy violation) — all API requests return 403. `deleted` is soft-delete; data is retained for the configured grace period before hard deletion.
- No `organization_id` on this table — it IS the top-level entity.

### human_user

```sql
create table human_user (
  id                    uuid primary key default gen_random_uuid(),
  organization_id       uuid not null references organization(id),
  email                 text not null unique,        -- unique within this org's database
  display_name          text not null,
  password_hash         text,                        -- bcrypt, nullable for future SSO-only users
  avatar_url            text,
  role                  text not null default 'member' check (role in ('owner', 'admin', 'member', 'viewer')),
  external_auth_provider text,                       -- nullable, for future SSO (e.g., 'google', 'github')
  external_auth_id      text,                        -- nullable, provider-specific user ID
  status                text not null default 'active' check (status in ('active', 'suspended', 'deleted')),
  last_login_at         timestamptz,
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now()
);

create unique index on human_user (external_auth_provider, external_auth_id)
  where external_auth_provider is not null;
```

**Design notes:**
- `email` is unique within the org's database. For managed hosting, cross-tenant email uniqueness (if needed) is enforced in the routing layer, not here.
- `role` is the RBAC role for this user. For V2 GA, the single operator is always `owner`. The other roles exist for near-term multi-user support.
- `password_hash` is nullable to support future SSO-only users who never set a password. For V2 GA, every user has a password.
- `external_auth_provider` and `external_auth_id` are the SSO hooks. Nullable now, populated when SSO is implemented. The partial unique index ensures no two users in this org share the same external identity.
- `organization_id` ties the user to the org. With database-per-org, this is always the single org in the database, but the FK exists for referential consistency.
- `status`: `active` is normal. `suspended` blocks login but preserves data. `deleted` is soft-delete.

### agent

The `agent` table schema is defined authoritatively in 05-agents-staff-and-temps.md. This document covers only the identity-relevant aspects:

- Agents are scoped to an org (`organization_id`). They cannot exist outside one, and they cannot be shared across orgs — same scoping model as `human_user`.
- `slug` is unique within the org — used for @mentions in chat (e.g., `@frank`, `@ellie`).
- `agent_class`: `staff` agents are durable and reusable; `temp` agents are ephemeral and task-scoped.
- `created_by_type = 'system'` with sentinel UUID (`00000000-0000-0000-0000-000000000000`) for `created_by_id` is used for the starter trio (Frank, Lori, Ellie) seeded during bootstrap, consistent with the system actor convention.
- Agents do not authenticate themselves — the platform asserts their identity via execution context (see Agent Authentication above).
- Retired agents are soft-deleted — their identity persists for audit trail integrity.

### session (auth session)

```sql
create table auth_session (
  id              uuid primary key default gen_random_uuid(),
  user_id         uuid not null references human_user(id),
  token_hash      text not null unique,   -- SHA-256 hash of the session token
  ip_address      inet,
  user_agent      text,
  expires_at      timestamptz not null,
  revoked_at      timestamptz,
  created_at      timestamptz not null default now(),
  last_active_at  timestamptz not null default now()
);

create index on auth_session (user_id);
create index on auth_session (expires_at) where revoked_at is null;
```

**Design notes:**
- Server-side session storage. The client holds an opaque token; we store the hash.
- `token_hash`: SHA-256 of the session token. We never store the raw token. On each request, hash the presented token and look up the row.
- No `organization_id` needed — the session lives in the org's database, so the org is implicit.
- `ip_address` and `user_agent`: captured at session creation for security auditing. Useful for detecting suspicious access patterns.
- `expires_at`: absolute expiry. Updated on each active use (sliding window) by the session middleware.
- `revoked_at`: non-null means the session is invalid, even if `expires_at` hasn't passed. Set on explicit logout or admin revocation.
- `last_active_at`: updated periodically (not on every request — batched, e.g., every 5 minutes) to avoid write amplification. Used for session management UX ("Last active 2 hours ago").
- The partial index on `expires_at` (where not revoked) supports the cleanup job that purges expired sessions.

### api_key

```sql
create table api_key (
  id              uuid primary key default gen_random_uuid(),
  user_id         uuid not null references human_user(id),
  name            text not null,          -- human-readable label ("CI pipeline", "monitoring")
  key_hash        text not null unique,   -- SHA-256 hash of the full key
  key_prefix      text not null,          -- first 8 chars for identification (e.g., "oc_full_a1b2")
  scope           text not null check (scope in ('full', 'read', 'chat')),
  expires_at      timestamptz,            -- nullable, null = no expiry
  revoked_at      timestamptz,
  last_used_at    timestamptz,
  created_at      timestamptz not null default now()
);

create index on api_key (user_id);
```

**Design notes:**
- API keys are bearer tokens for programmatic access. They act on behalf of a specific user. The org is implicit — the key lives in the org's database.
- `key_hash`: SHA-256 of the full key. The raw key is shown once at creation, never stored.
- `key_prefix`: the first 8 characters of the key, stored in plaintext. Used for identification in the UI ("Key ending in ...a1b2") and for the key lookup optimization (first narrow by prefix, then verify hash).
- `scope`: caps the key's permissions. `full` = same as user's role. `read` = read-only. `chat` = read + send messages.
- `expires_at`: optional. Null means no expiry. A daily cleanup job revokes expired keys.
- `last_used_at`: updated on each use (batched, not per-request). Useful for identifying stale keys.

### audit_event

```sql
create table audit_event (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  action          text not null,          -- namespaced action (e.g., 'auth.login', 'agent.created')
  principal_type  text not null check (principal_type in ('human', 'agent', 'system')),
  principal_id    uuid not null,          -- sentinel UUID 00000000-0000-0000-0000-000000000000 for system actions
  delegated_by    uuid references human_user(id),  -- set when an agent acts on behalf of a human
  scope_type      text check (scope_type in ('org', 'project', 'task', 'session')),
  scope_id        uuid,                   -- optional: the ID of the scoped entity
  context         jsonb not null default '{}', -- action-specific payload
  created_at      timestamptz not null default now()
);

create index on audit_event (organization_id, created_at);
create index on audit_event (organization_id, action);
create index on audit_event (principal_type, principal_id);
```

**Design notes:**
- Append-only. No `updated_at` — these rows never change.
- `action` is a namespaced string following the same pattern as capabilities: `auth.login`, `auth.failed_login`, `agent.created`, `policy.evaluated`, `api_key.created`, `org.settings_changed`, etc.
- `principal_type = 'system'` with sentinel UUID (`00000000-0000-0000-0000-000000000000`) for `principal_id` on system-initiated actions (bootstrap, scheduled cleanup, etc.), consistent with the system actor convention.
- `delegated_by` is the delegation trace — records which human authorized an agent's action. Null for direct human actions and for agent actions without explicit delegation.
- `scope_type` / `scope_id` is optional scoping for actions within a specific project, task, or session. Null for org-level actions (login, settings change).
- `context` is the rich payload: IP address for auth events, old/new values for setting changes, policy inputs/outputs for evaluations. Structure varies by action type. The application defines a schema per action type; the database stores it as jsonb.
- Indexed by `(organization_id, created_at)` for time-range queries and `(organization_id, action)` for filtering by action type. The `(principal_type, principal_id)` index supports "show me everything this agent did."

### Cross-Entity Relationships

```
organization (one per database)
  ├── human_user (with role)
  │     ├── auth_session (via user_id)
  │     └── api_key (via user_id)
  ├── agent (schema in doc 05)
  ├── audit_event
  ├── chat_session (doc 02)
  ├── project (doc 03)
  ├── memory (doc 06)
  └── ... (all other domain entities)
```

- `organization` is the root of the tenant tree. Everything hangs off it. One org per database.
- `human_user` is org-scoped — lives in the org's database with a `role` column for RBAC.
- `agent` is org-scoped — same as human_user.
- Both `human_user` and `agent` are referenced polymorphically throughout the system via the `(principal_type, principal_id)` convention.

## Authentication Flow

### Login (Email + Password)

```
1. Resolve tenant:
   - Self-hosted: single database, no resolution needed.
   - Managed hosting: tenant identified from request context (subdomain, org slug in URL path,
     or explicit org parameter). Routing layer connects to the correct org database.
2. Client sends POST /auth/login {email, password}
3. Server looks up human_user by email in the org's database
4. Server verifies password against password_hash (bcrypt)
5. If invalid: increment failed attempt counter (in-memory), return 401, record audit_event (auth.failed_login)
6. If valid:
   a. Create auth_session row (generate token, hash it, store hash)
   b. Return session token to client
   c. Record audit_event (auth.login)
7. Client stores token (cookie for web, file/keychain for CLI)
```

### API Key Authentication

```
1. Resolve tenant (same as login flow — subdomain, slug, or single-database for self-hosted).
2. Client sends request with Authorization: Bearer oc_full_a1b2c3d4...
3. Server extracts prefix (first 8 chars), looks up api_key rows matching prefix in the org's database
4. Server hashes the full key, compares against key_hash
5. If no match: return 401
6. If match:
   a. Check revoked_at (must be null)
   b. Check expires_at (must be null or future)
   c. Resolve user_id from the key
   d. Check scope against the requested operation
   e. Inject user identity and org context into request
   f. Update last_used_at (batched)
```

### Request Authentication Middleware

Every authenticated request passes through middleware that:

1. Extracts the token (from cookie or Authorization header).
2. Determines the auth method (session token vs API key — distinguished by prefix).
3. Validates the credential.
4. Loads the user, org, and role into the request context.
5. All downstream code accesses `ctx.UserID`, `ctx.OrganizationID`, `ctx.Role` — never the raw token.

Unauthenticated endpoints (login, health check, public info) are explicitly allowlisted. Everything else requires authentication.

## Rate Limiting and Brute Force Protection

### Login Rate Limiting

- **Per IP**: 10 failed login attempts per IP per 15-minute window. After that, the IP is blocked for 15 minutes.
- **Per account**: 5 failed attempts per email per hour. After that, the account is temporarily locked for 30 minutes. Self-hosted operators can bypass via `ottercamp unlock-account --email <email>`.
- **Exponential backoff**: after each failed attempt, the response is artificially delayed (100ms, 200ms, 400ms, ...) to slow down automated attacks.

These limits are stored in-memory in the application process. They do not require database writes or external dependencies. If the process restarts, counters reset — this is acceptable since a restart already disrupts an active brute force attempt.

### API Rate Limiting

See the API Keys section above. Implemented via a token bucket algorithm, tracked per key and per org.

## Resolved Decisions

- **Organization is the isolation boundary, enforced at the database level.** Every org gets its own database — no shared databases between tenants, even in managed hosting. This eliminates cross-tenant data leaks architecturally. CLI tools connect to a single-org database so there is no way to break out of the sandbox.
- **Single-user at launch, multi-user near-term.** V2 GA ships with one human per org. Multi-user support is a near-term addition — the schema is ready from day one with the `role` column on `human_user` supporting four RBAC roles. No migration needed to add users.
- **No `org_membership` table.** With database-per-org isolation, the role lives directly on `human_user`. No join table needed. Multi-org user identity (one person in multiple orgs) is deferred — if needed, each org's database would have its own `human_user` record.
- **Four RBAC roles: owner, admin, member, viewer.** Only `owner` is used in V2 GA. The others exist in the schema for future multi-user support.
- **Agent identity is separate from human identity.** Both are first-class principals. Both are org-scoped — they live in the org's database. The authoritative agent table schema lives in doc 05; this doc covers identity concepts only.
- **Agents do not authenticate themselves.** The platform asserts agent identity through execution context. No agent credentials to leak or manage. External service credentials are resolved at execution time by the control plane.
- **The principal convention is polymorphic, not a table.** `(principal_type, principal_id)` appears throughout the schema. No unified `principal` table — it would add joins for no value.
- **System actions use a well-known sentinel UUID.** `actor_type = 'system'`, `actor_id = 00000000-0000-0000-0000-000000000000`. Keeps the principal convention intact.
- **Email + password is the only auth method for V2 GA.** SSO/OIDC is deferred to post-GA. The schema includes `external_auth_provider` and `external_auth_id` fields so SSO can be added without migration.
- **No org-to-org sharing.** Orgs are hermetically sealed at the database level. There is no mechanism for cross-org data access.
- **API keys are prefixed, hashed, and scoped.** Format: `oc_<scope>_<random>`. Stored as SHA-256 hash. Three scopes: `full`, `read`, `chat`. Tied to a user (org is implicit from the database).
- **Sessions are server-side with hashed tokens.** 30-day sliding window. HttpOnly cookies for web, Bearer tokens for CLI/API. Concurrent sessions allowed.
- **Audit events are append-only and reserved for security-sensitive actions.** Routine operations are captured by domain-specific event tables (chat_message, project_task_event, etc.). Audit events cover auth, identity changes, authorization decisions, and system events.
- **Bootstrap seeds the starter trio.** First install creates the org, the first user (owner), Frank, Lori, Ellie, and the General chat session. Bootstrap is idempotent — skipped if an org already exists.
- **Service account keys deferred.** User-scoped API keys are sufficient for CI/CD at launch. A dedicated service account concept can be added later without schema breakage if the need is validated.
- **Password-less local auth for self-hosted.** Opt-in via `OTTERCAMP_AUTH_MODE=local`. Auto-authenticates all connections as the owner. Only allowed when binding to localhost — rejected if the listen address is non-local. Prominent warning logged on startup. Never available in managed hosting.
- **Password reset is CLI-based for self-hosted, email-based for managed.** No email infrastructure required for self-hosted deployments.
- **Delegation is traced in the audit trail.** When a human authorizes an agent action, both the agent (performer) and the human (delegator) are recorded.
- **Rate limiting for login uses per-IP and per-account limits.** Exponential backoff on failed attempts. Prevents brute force without requiring CAPTCHAs. Counters stored in-memory, no Redis dependency.
- **CSRF: `SameSite=Lax` is sufficient for GA.** Revisit CSRF tokens when multi-user ships.
- **CLI escape hatch for account lockout.** `ottercamp unlock-account --email <email>` resets the failed attempt counter. Self-hosted only — requires direct access to the server.
- **Three CLI account management commands.** `reset-password` (direct hash update), `magic-link` (time-limited single-use login URL, 15-minute expiry), `unlock-account` (reset brute force counter). All require server access, no email infrastructure needed.
- **auth_session is separate from chat_session.** `auth_session` is the authentication session (login state). `chat_session` (doc 02) is a conversation thread. Different concepts, different tables, no confusion.

## Open Questions

All open questions have been resolved — see Resolved Decisions above.

