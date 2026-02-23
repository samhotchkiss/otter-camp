---
## Summary

This spec defines the three cross-cutting concerns that apply to every other OtterCamp V2 spec: security, observability, and cost controls. OtterCamp is a single-human-operator platform where AI agents act on the operator's behalf, so the security model is primarily concerned with protecting the operator from rogue agent behavior, data leakage, and cost overruns rather than traditional external attackers. Security is implemented as six defense-in-depth layers: org isolation (hermetic `organization_id` scoping on every query), authentication (email/password or scoped API keys; agents have no credentials of their own), authorization (RBAC for humans, capability-based for agents with a default-deny posture), control plane gating (all agent mutations pass through the execution broker), agent sandboxing (constrained CLI, isolated browser contexts, per-connection MCP capability checks), and an immutable audit trail. The threat model explicitly centers on rogue agent behavior, secret exposure, prompt injection, external tool abuse, cost explosions, and denial of service.

Secret management uses AES-256-GCM encryption at rest in PostgreSQL with a master key provided by the operator (env var or KMS). Five codebase-wide invariants ensure secrets never appear in prompts, logs, API responses, audit events, or memory. The `secret` table stores encrypted blobs with per-secret nonces and key version tracking to support zero-downtime master key rotation. Privacy and retention are handled through configurable per-org policies enforced by a daily background job: 90-day default for chat transcripts and run records, 30 days for model invocation logs, 1 year for audit events (3 years managed), and indefinite for active memories. The spec includes GDPR-aware design (access, erasure, portability, minimization) without formal certification at GA.

Observability is built into the platform, not bolted on. It consists of structured JSON logging (with mandatory trace_id, component, and secret scrubbing), OpenTelemetry-compatible distributed tracing (stored in a `trace_span` table with configurable sampling, default 100%), and Prometheus-compatible metrics exposed at `/metrics` across four categories: API, model, queue/execution, and memory. Four built-in web UI dashboards (overview, cost, performance, agents) surface key operational indicators, with OTLP export available for operators who prefer external tools like Grafana or Datadog. Reliability features include per-dependency circuit breakers (model providers, MCP servers, git remotes), graceful degradation strategies, SLOs for managed deployments (99.9% API availability), and incident severity classification.

Cost controls are integrated into the control plane execution broker. Every `ModelInvocation` records per-request cost using a provider pricing table. Costs are aggregated into hourly, daily, and monthly summaries in a `cost_summary` table. Budgets are configurable at org and project levels with soft limits (warn once per period) and hard limits (block execution entirely). A critical design decision: hard limits fail closed -- they block execution and notify the human rather than silently degrading to cheaper models. Anomaly detection runs every 15 minutes and alerts when hourly spend exceeds 3x the 7-day average. Key schemas include `secret`, `cost_summary`, and `cost_budget`.

---

# 13. Security, Observability, and Cost Controls

## Purpose

Define the cross-cutting security posture, observability infrastructure, and cost management system for OtterCamp V2. These concerns apply to every other spec — they are not standalone features but constraints and capabilities that the entire platform must satisfy.

OtterCamp is a single-human-operator platform where AI agents act on the operator's behalf. The security model protects the operator from rogue agent behavior, data leakage, and cost overruns. The observability model gives the operator visibility into what agents are doing and what it costs. The cost model ensures the operator stays in control of spend.

## Security Model

### Defense in Depth

Security is layered. No single mechanism is the sole defense — every layer assumes the one above it might fail.

**Layer 1: Org Isolation (Perimeter)**

All data is scoped to an organization. Every API endpoint and database query includes `organization_id` in its filter. There is no code path that queries data without org scope (see 04-auth-tenancy-and-identity.md Row-Level Enforcement).

- API middleware extracts `organization_id` from the authenticated session or token and injects it into every request context.
- Database queries always include `organization_id` in WHERE clauses.
- Row-level security (RLS) in PostgreSQL is available as defense-in-depth for managed multi-tenant deployments.
- Cross-org data sharing does not exist. Orgs are hermetically sealed.

**Layer 2: Authentication**

Two authentication methods (see 04-auth-tenancy-and-identity.md):

- Email + password (bcrypt, work factor 12, minimum 12 characters) for interactive sessions.
- API keys (`oc_<scope>_<random>`, SHA-256 hashed, scoped to org) for programmatic access.

Agents do not authenticate themselves. Agent identity is asserted by the platform through execution context — there is no agent credential to leak or manage.

Sessions are server-side with hashed tokens. 30-day sliding window. HttpOnly + Secure + SameSite=Lax cookies for web. Bearer tokens for CLI/API.

**Layer 3: Authorization**

Two-tier authorization model:

- RBAC roles (owner, admin, member, viewer) for coarse human access control.
- Capabilities for fine-grained agent action permissions (see 16-agent-control-plane.md).

Default posture is **deny**. Agents can only perform actions for which they have been explicitly granted capabilities. Policy evaluation produces one of three outcomes: `allow`, `deny`, `require_approval`.

Policy layers, highest priority first:
1. Instance safety policy
2. Organization policy
3. Project policy
4. Agent profile policy
5. Request-specific overrides (most restrictive only)

**Layer 4: Control Plane Gating**

All agent mutations pass through the control plane execution broker (see 16-agent-control-plane.md). There is no privileged execution path outside the broker/worker pipeline. The broker evaluates policy, dispatches to the worker, and records the outcome. High-risk actions are gated by `require_approval` policy and queued for human review.

**Layer 5: Agent Sandboxing**

Agents execute in constrained environments (see 11-system-integration-cli-and-browser.md):

- **CLI execution**: constrained working directories, restricted environment variables, time and resource limits, configurable network policy.
- **Browser execution**: isolated browser contexts per run/session, domain allowlists/denylists, artifact capture.
- **MCP execution**: per-connection capability checks, request/response schema validation (see 09-mcp-integration.md).

**Layer 6: Audit Trail**

Every security-sensitive action produces an immutable audit event (see 04-auth-tenancy-and-identity.md Auditability). Audit events are append-only — never updated or deleted during normal operation. The audit trail answers: who did what, when, where, and who authorized it.

### Network Security

OtterCamp is designed for two deployment modes with different network postures:

**Self-hosted (single-node)**:
- The operator controls the network. OtterCamp binds to localhost by default. The operator is responsible for TLS termination and access control if exposing to a network.
- Outbound connections: model provider APIs (OpenAI, Anthropic, Google), MCP server endpoints, git remotes. All over HTTPS/TLS.
- No inbound connections required unless the operator configures webhooks or exposes the API.

**Managed (multi-tenant)**:
- TLS everywhere. No unencrypted connections.
- API gateway with rate limiting and DDoS protection.
- Internal service communication over private networks.
- Outbound connections to model providers over HTTPS.
- Database connections encrypted in transit.

**Shared requirements**:
- All external API calls (model providers, MCP servers, git remotes) use TLS. Unencrypted HTTP is not supported for external connections.
- Secrets are never transmitted in query parameters — always in headers or request bodies.
- CORS is restricted to known origins for the web UI.

## Threat Model

### What We Are Protecting Against

OtterCamp's threat model is shaped by its architecture: a single human operator with AI agents that have real operational control. The primary threats are not external attackers (though they matter for managed deployments) — they are the agents themselves acting beyond their intended boundaries.

### Threat: Rogue Agent Behavior

An agent attempts actions outside its permitted scope — modifying files it shouldn't touch, calling tools it shouldn't access, escalating its own privileges.

**Mitigations**:
- Default-deny capability posture. Agents can only do what is explicitly permitted.
- All actions pass through the control plane broker. No direct execution path.
- Policy evaluation on every action request with full audit trail.
- High-risk actions require human approval.
- Agent identity is asserted by the platform, not claimed by the agent — an agent cannot impersonate another agent.

### Threat: Data Leakage Between Orgs

In managed multi-tenant deployments, one org's data is exposed to another org through a query that fails to scope correctly.

**Mitigations**:
- `organization_id` is injected by middleware from the authenticated session, not from user input. Application code cannot omit it.
- Every table that carries `organization_id` has it in primary query indexes.
- Database-level RLS as defense-in-depth for managed deployments.
- Integration tests validate org isolation on all query paths.

### Threat: Secret Exposure

Model provider API keys, SSH keys, MCP credentials, or other secrets are exposed in logs, agent prompts, or stored unencrypted.

**Mitigations**:
- Secrets are encrypted at rest in PostgreSQL (see Secret Management below).
- Secrets are decrypted only at execution time by the control plane worker.
- Secrets are never included in agent prompts — they are injected into the sandboxed execution environment.
- Secrets are never written to logs. Log scrubbing rejects known secret patterns (API key prefixes, SSH key headers, bearer tokens).
- Secrets are never returned in API responses after creation.

### Threat: Prompt Injection

An external input (MCP tool response, file content, user-uploaded document) contains instructions that attempt to override the agent's behavior or exfiltrate data.

**Mitigations**:
- Agent identity layer (the system prompt containing who the agent is and its policies) is the highest-priority prompt layer and is never cut from context (see 05-agents-staff-and-temps.md Prompt Layers). External content cannot override identity.
- MCP tool responses are treated as untrusted data — they are injected as tool results, not as system instructions.
- Memory sensitivity classification prevents restricted memories (credentials, access controls) from appearing in passive injection.
- The control plane evaluates every action the agent attempts regardless of why the agent decided to attempt it — even a successfully injected prompt still hits policy evaluation.

### Threat: External Tool Abuse

An agent uses MCP connections or CLI access to perform harmful operations on external systems — deleting production data, exfiltrating information, making unauthorized API calls.

**Mitigations**:
- MCP connections require per-connection capability grants (see 09-mcp-integration.md).
- MCP tool invocations require per-tool capability grants: `mcp.tool.invoke:<connection_id>:<tool_name>`.
- CLI execution is constrained to allowed working directories with restricted environment variables.
- Browser automation uses domain allowlists/denylists.
- All external actions are logged in the audit trail with full input/output capture.

### Threat: Cost Explosion

A runaway agent enters a loop, makes excessive model calls, or triggers cascading work that burns through the model provider budget.

**Mitigations**:
- Per-request cost tracking on every ModelInvocation.
- Per-org and per-project budget limits (soft and hard).
- Hard limits fail closed — execution is blocked, not degraded.
- Per-turn and per-run timeout budgets prevent infinite loops.
- Anomaly detection flags sudden usage spikes.
- Queue depth limits prevent unbounded work accumulation.

### Threat: Denial of Service (Managed Only)

External attackers attempt to overwhelm the managed service with requests.

**Mitigations**:
- Rate limiting per API key and per org (see 04-auth-tenancy-and-identity.md).
- Login rate limiting with exponential backoff (per-IP and per-account).
- API gateway DDoS protection (infrastructure-level, not application-level).
- Concurrency limits on model provider calls prevent a single org from monopolizing provider quotas.

## Secret Management

### What Secrets OtterCamp Stores

OtterCamp manages several categories of secrets on behalf of the operator:

- **Model provider API keys**: OpenAI, Anthropic, Google API keys for inference.
- **SSH keys**: for git remote access (pushing/pulling code repositories).
- **MCP credentials**: API keys, OAuth tokens, or other credentials for MCP server connections.
- **Webhook secrets**: signing keys for outbound webhook payloads (future).

### Storage

Secrets are stored in PostgreSQL, encrypted at rest using AES-256-GCM. The encryption key is derived from a master key that is:

- **Self-hosted**: provided by the operator via environment variable (`OTTERCAMP_SECRET_KEY`) or injected by a secret manager (Vault, AWS Secrets Manager, etc.). The operator is responsible for protecting this key.
- **Managed**: managed by the hosting infrastructure's KMS. The application never sees the raw master key.

Each secret is encrypted with a unique nonce. The encrypted blob and nonce are stored together. The plaintext is never written to disk, logs, or any persistent store other than the encrypted column.

### Schema

```sql
create table secret (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  name            text not null,                        -- human-readable label ("OpenAI Production", "GitHub SSH")
  secret_type     text not null,                        -- model_provider, ssh_key, mcp_credential, webhook_signing
  encrypted_value bytea not null,                       -- AES-256-GCM encrypted
  nonce           bytea not null,                       -- unique per encryption
  key_version     int not null default 1,               -- for key rotation tracking
  metadata        jsonb not null default '{}',          -- type-specific metadata (provider name, connection_id, etc.)
  created_by_type text not null check (created_by_type in ('human', 'system')),
  created_by_id   uuid,
  last_rotated_at timestamptz,
  expires_at      timestamptz,                          -- optional expiry
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),

  unique (organization_id, name)
);

create index on secret (organization_id);
create index on secret (secret_type, organization_id);
```

### Lifecycle

- **Creation**: the operator provides a secret value through the settings UI, CLI, or API. The value is encrypted immediately and stored. The plaintext is never echoed back after creation.
- **Usage**: when the control plane worker needs a secret (to call a model provider, connect to an MCP server, or authenticate with a git remote), it reads the encrypted value, decrypts it in memory, injects it into the sandboxed execution environment, and discards the plaintext after the operation completes.
- **Rotation**: the operator provides a new value. The old encrypted blob is overwritten. `key_version` is incremented. `last_rotated_at` is updated. An audit event is recorded. Active sessions using the old value will fail on next use and retry with the new value.
- **Deletion**: the encrypted blob is zeroed and the row is deleted. An audit event is recorded.
- **Expiry**: secrets with an `expires_at` in the past are flagged in the settings UI and in the operator's inbox. Expired secrets are not automatically deleted — they continue to work until manually rotated or deleted. The warning is informational.

### Secret Safety Rules

These rules are enforced across the entire codebase:

1. **Never in prompts**: secrets are never included in agent system prompts, context windows, or memory items. When an agent needs to use a credential, it references it by ID — the control plane resolves the reference at execution time.
2. **Never in logs**: structured logging scrubs known secret patterns before writing. Secret values are replaced with `[REDACTED]`. Patterns include: API key prefixes (sk-, key-, etc.), SSH private key headers, bearer token values, base64-encoded blobs over 32 characters in secret-adjacent fields.
3. **Never in API responses**: after creation, the secret value is never returned by the API. Only the name, type, creation date, and last-rotated date are visible.
4. **Never in audit events**: audit events for secret operations record the action (created, rotated, deleted) and the secret name/ID, but never the value.
5. **Never in memory**: Ellie's extraction pipeline skips secret-pattern content. Memory items with sensitivity `restricted` that match secret patterns are rejected at extraction time.

### Master Key Rotation

When the master encryption key needs to be rotated:

1. The operator provides a new master key.
2. A background job decrypts all secrets with the old key and re-encrypts with the new key.
3. `key_version` is incremented on each re-encrypted row.
4. The old key can be removed after all rows have been re-encrypted.
5. An audit event records the rotation.

This is a maintenance operation, not a routine action. It does not require downtime — the application supports reading secrets encrypted with any known key version.

## Privacy and Retention

### Retention Policy Defaults

Retention policies are configurable per organization via `organization.settings`. Defaults:

| Data Type | Default Retention | Rationale |
|---|---|---|
| Chat session transcripts | 90 days | Conversations are voluminous. Valuable context is extracted to memory. Raw transcripts are useful for debugging and audit but not indefinitely. |
| Memory items (active) | Indefinite | Memories are the distilled knowledge of the organization. They have their own lifecycle (decay, consolidation, archival) but are not deleted by retention policy. |
| Memory items (archived) | 1 year | Archived memories are preserved for provenance and temporal queries. After 1 year, they can be purged. |
| Audit events | 1 year (self-hosted), 3 years (managed) | Audit events are the security compliance record. Longer retention for managed deployments where compliance requirements are stricter. |
| Run records and events | 90 days | Execution history is useful for debugging and cost analysis. After 90 days, aggregate metrics are sufficient. |
| Model invocation logs | 30 days | Detailed prompt/completion logs are large and sensitive. Token/cost aggregates are retained indefinitely. |
| Object storage artifacts | 90 days | Screenshots, generated files, log outputs. Artifacts linked to active tasks are exempt from retention until the task completes. |

### Retention Enforcement

A daily background job enforces retention policies:

1. Query for records older than the retention threshold.
2. For chat transcripts and model invocation logs: archive to object storage (compressed) before deletion if the org has archival enabled. Otherwise, hard delete.
3. For audit events: archive to object storage before deletion. Never hard delete without archival.
4. For run records: delete. Aggregate metrics (cost, token usage, latency) are retained in summary tables indefinitely.
5. Record the retention run as an audit event.

### Data Deletion

The operator can request deletion of specific data:

- **Delete a chat session**: all messages, tool call records, and linked artifacts are deleted. Memories extracted from the session are NOT deleted — they have their own lifecycle and provenance chain. An audit event records the deletion.
- **Delete a project**: all tasks, flow executions, events, and linked artifacts are deleted. Memories scoped to the project are archived (not deleted) with reason `project_deleted`. An audit event records the deletion.
- **Delete an agent**: the agent is retired (soft-deleted). All identity records are preserved for audit trail integrity. Run records are retained for their normal retention period. An audit event records the retirement.
- **Delete all data (org wipe)**: a destructive operation that deletes all org data except audit events. Audit events for the wipe operation are the only records that remain. This requires the operator to confirm by typing the org slug. Available via CLI only — not exposed in the web UI.

### Session Transcript Redaction

The operator can redact specific messages or message ranges from chat session transcripts. Redaction replaces the message content with `[REDACTED]` and records an audit event. Redacted messages retain their metadata (timestamp, author, message type) for structural integrity but lose their content.

Redaction is a human-only operation — agents cannot redact messages. This prevents an agent from covering its tracks.

### GDPR-Aware Design

OtterCamp V2 is not GDPR-certified at GA. However, the architecture is designed to support GDPR compliance when needed:

- **Right to access**: all data for a principal can be queried via the `(principal_type, principal_id)` convention across all tables. An export API generates a JSON archive of all data associated with a human user.
- **Right to erasure**: the data deletion workflow supports removing a human user's data across all tables. Audit events are retained (legitimate interest basis) but can be anonymized (replace `principal_id` with a sentinel).
- **Data portability**: the export API produces a machine-readable JSON archive. Chat transcripts, memories, project data, and agent configurations can all be exported.
- **Data minimization**: retention policies ensure data is not kept longer than needed. Model invocation logs (which contain prompts and completions) have the shortest retention.
- **Consent**: OtterCamp is an operator-controlled platform, not a consumer service. The operator is both the data controller and the primary data subject. In managed multi-tenant, the Terms of Service cover data processing.

Full GDPR compliance (DPA, formal DPIA, external audit) is post-GA scope.

### Other Compliance Considerations

- **SOC 2**: not targeted for V2 GA. The audit trail, access controls, and encryption-at-rest provide the technical foundations. Formal SOC 2 certification requires operational procedures and third-party audit — post-GA.
- **HIPAA**: not in scope. OtterCamp is not designed for healthcare data.
- **Data residency**: not enforced at V2 GA. Self-hosted deployments inherently satisfy data residency (the operator chooses where to host). Managed deployments will add region selection post-GA.

## Observability

### Design Principles

Observability in OtterCamp serves one primary user: the operator. The operator needs to answer three questions:

1. **What are my agents doing right now?** (Operational awareness)
2. **Is something wrong?** (Anomaly detection)
3. **What did it cost?** (Financial accountability)

The observability system is built into OtterCamp, not bolted on as a separate tool. Operational dashboards live in the web UI (see 18-web-ui.md). Self-hosted operators who prefer external tools can export metrics and traces to Grafana, Datadog, or any OpenTelemetry-compatible backend.

### Structured Logging

All application logging uses structured JSON format. Every log entry includes:

```json
{
  "timestamp": "2026-02-22T14:30:00.000Z",
  "level": "info",
  "message": "model invocation completed",
  "trace_id": "abc123def456",
  "span_id": "789ghi012",
  "organization_id": "org-uuid",
  "principal_type": "agent",
  "principal_id": "agent-uuid",
  "component": "model_gateway",
  "attributes": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-20250514",
    "input_tokens": 1200,
    "output_tokens": 450,
    "latency_ms": 3200
  }
}
```

**Required fields on every log entry**:
- `timestamp` (ISO 8601 with timezone)
- `level` (`debug`, `info`, `warn`, `error`)
- `message` (human-readable description)
- `trace_id` (links all log entries for a single request/run)
- `component` (which subsystem emitted the log)

**Required fields when available**:
- `organization_id` (always present after authentication middleware)
- `principal_type` and `principal_id` (who triggered this)
- `span_id` (for distributed tracing spans)
- `project_id`, `task_id`, `session_id`, `run_id` (scope context)

**Log levels**:
- `debug`: internal details useful for development. Disabled in production by default.
- `info`: normal operational events. Model calls, task transitions, session activity.
- `warn`: unusual but handled situations. Approaching budget limits, retry attempts, deprecated API usage.
- `error`: failures that require attention. Model provider errors, policy violations, unhandled exceptions.

**Log scrubbing**: before writing, all log entries pass through a scrubbing filter that replaces known secret patterns with `[REDACTED]`. This is a safety net — secrets should never reach the logging layer, but the scrubber catches mistakes.

**Log output**: logs are written to stdout in JSON format. The deployment environment handles routing (Docker log driver, systemd journal, CloudWatch, etc.). OtterCamp does not manage log storage or rotation — that is the operator's infrastructure concern.

### Distributed Tracing

OtterCamp implements OpenTelemetry-compatible distributed tracing. A trace follows a request from the API layer through the worker, model calls, tool executions, and back.

**Trace structure for a typical agent action**:

```
[API Request]
  └── [Control Plane: Policy Evaluation]
  └── [Worker: Run Execution]
        └── [Model Gateway: Model Invocation]
        │     └── [Provider: API Call]
        └── [Tool Execution: CLI Command]
        └── [Tool Execution: MCP Call]
        │     └── [MCP Server: External Request]
        └── [Memory: Passive Retrieval]
        └── [Model Gateway: Model Invocation (follow-up)]
              └── [Provider: API Call]
```

Each span captures:
- Start/end timestamps (latency)
- Status (ok, error)
- Attributes specific to the span type (model name, token counts, command executed, etc.)
- Parent span ID (for the trace tree)

**Trace propagation**: trace context is propagated through all internal calls via context injection. For external calls (model providers, MCP servers), trace context is included in request headers where the external service supports it.

**Trace storage**: traces are stored in a `trace_span` table with a configurable retention (default: 7 days). The table is append-only and partitioned by day for efficient cleanup. Self-hosted operators can export traces to Jaeger, Zipkin, or any OTLP-compatible backend by configuring an OTLP exporter endpoint.

**Trace sampling**: in high-throughput scenarios, traces can be sampled to reduce storage costs. Default: 100% sampling (record every trace). Configurable down to 1% for high-volume managed deployments. Error traces are always recorded regardless of sampling rate.

### Metrics

OtterCamp exposes Prometheus-compatible metrics via a `/metrics` endpoint (pull model) and optionally pushes to an OTLP metrics endpoint (push model for managed deployments).

Metrics are organized into four categories:

**API Metrics**:
- `ottercamp_http_request_duration_seconds` (histogram): request latency by endpoint and method. p50, p95, p99.
- `ottercamp_http_request_total` (counter): total requests by endpoint, method, and status code.
- `ottercamp_http_request_in_flight` (gauge): currently processing requests.

**Model Metrics**:
- `ottercamp_model_invocation_duration_seconds` (histogram): model call latency by provider and model.
- `ottercamp_model_invocation_total` (counter): total model calls by provider, model, and status.
- `ottercamp_model_tokens_input_total` (counter): total input tokens by provider and model.
- `ottercamp_model_tokens_output_total` (counter): total output tokens by provider and model.
- `ottercamp_model_cost_dollars_total` (counter): total cost in dollars by provider and model.
- `ottercamp_model_invocation_in_flight` (gauge): currently active model calls.

**Queue and Execution Metrics**:
- `ottercamp_queue_depth` (gauge): number of runs waiting to execute, by priority tier.
- `ottercamp_queue_wait_seconds` (histogram): time spent in queue before execution starts.
- `ottercamp_run_duration_seconds` (histogram): total run duration by status (completed, failed, cancelled).
- `ottercamp_run_active` (gauge): currently executing runs.
- `ottercamp_approval_queue_depth` (gauge): actions awaiting human approval.
- `ottercamp_approval_queue_age_seconds` (gauge): age of the oldest pending approval.

**Memory Metrics**:
- `ottercamp_memory_retrieval_duration_seconds` (histogram): memory retrieval latency by mode (passive, active, tool).
- `ottercamp_memory_items_total` (gauge): total memory items by status (active, archived, candidate).
- `ottercamp_memory_extraction_total` (counter): memories extracted by kind.
- `ottercamp_memory_consolidation_duration_seconds` (histogram): consolidation job duration by type.

All metrics carry an `organization_id` label for multi-tenant deployments.

### Key Metrics

The following metrics are the essential operational indicators. These should be on the primary operational dashboard and have alerting thresholds:

| Metric | Description | Alert Threshold (Default) |
|---|---|---|
| API p50/p95/p99 latency | Request latency by endpoint | p99 > 5s for any endpoint |
| API error rate | 5xx responses / total requests | > 1% over 5-minute window |
| Model call p50/p95/p99 latency | Model invocation latency by provider | p99 > 30s |
| Model error rate | Failed model calls / total | > 5% over 5-minute window |
| Queue depth | Pending runs waiting for execution | > 50 for > 10 minutes |
| Queue wait time p95 | How long runs wait before starting | > 5 minutes |
| Token usage rate | Tokens consumed per hour | Configurable per org |
| Cost per hour | Dollar spend rate | Configurable per org |
| Active runs | Currently executing runs | > configured concurrency limit |
| Agent utilization | % of time agents are actively executing vs idle | Informational, no alert |
| Memory retrieval p95 latency | Memory query latency (passive path) | > 500ms (sync sessions are latency-sensitive) |
| Approval queue age | Time since oldest pending approval | > 1 hour |
| Failed run rate | Failed runs / total runs | > 10% over 1-hour window |

### Operational Dashboards

Dashboards are built into the web UI (see 18-web-ui.md) as part of the Settings/Observability section. They are not a separate tool.

**Dashboard: Overview**
- Current active runs and their status.
- Today's spend vs budget (bar chart).
- Error rate trend (24 hours).
- Approval queue depth and oldest pending item.
- Agent activity feed (last 50 events).

**Dashboard: Cost**
- Spend by provider and model (stacked area chart, 30-day view).
- Spend by project (bar chart).
- Spend by agent (bar chart).
- Budget utilization gauges (per org, per project).
- Cost trend and projection (extrapolate current rate to end of billing period).

**Dashboard: Performance**
- API latency percentiles (line chart, 24-hour view).
- Model latency by provider (line chart).
- Queue depth over time (area chart).
- Memory retrieval latency (line chart).
- Error rate by component (stacked area chart).

**Dashboard: Agents**
- Per-agent activity: runs completed, runs failed, tokens consumed, cost.
- Agent utilization timeline (gantt-style: active, idle, blocked, waiting for approval).
- Per-agent error rate.

**Export for external tools**: self-hosted operators can configure an OTLP exporter endpoint or scrape the Prometheus `/metrics` endpoint to feed data into Grafana, Datadog, or other monitoring tools. The built-in dashboards are sufficient for most operators, but the data is not locked in.

## Reliability

### Service Level Objectives

SLOs differ by deployment mode because the operator's expectations and control differ:

**Self-hosted (single-node)**:

Self-hosted operators control their own infrastructure. OtterCamp does not make SLO guarantees for self-hosted deployments. Instead, it provides:
- Health check endpoints (`/health/live`, `/health/ready`) for monitoring.
- Structured logs and metrics for diagnosing issues.
- Graceful degradation when external dependencies fail.
- Documentation on recommended hardware, backup procedures, and upgrade paths.

**Managed (multi-tenant)**:

| Objective | Target | Measurement |
|---|---|---|
| API availability | 99.9% (43 min/month downtime budget) | Successful responses / total requests, measured over calendar month |
| API latency p95 | < 500ms for CRUD endpoints | Excluding model invocation endpoints (those depend on provider latency) |
| Run execution start | < 30s from `allow` to worker pickup | Time between policy approval and execution beginning |
| Realtime event delivery | < 2s from emission to client receipt | WebSocket/SSE event latency |
| Data durability | 99.99% | No data loss from infrastructure failure |

These are operational targets, not contractual SLAs. Contractual SLAs are a commercial concern, not a product spec concern.

### Failure Domains

OtterCamp identifies and isolates the following failure domains:

**Model Providers (external)**:
- Provider API goes down or returns errors.
- Provider rate limits are exceeded.
- Provider latency spikes.

Impact: agents cannot make model calls. Runs that require model invocations fail or queue.
Mitigation: circuit breaker per provider. Fallback chain in model profiles (see 07-models-and-inference.md). Clear error surfaced to the operator.

**MCP Servers (external)**:
- MCP server is unreachable or returns errors.
- MCP server latency spikes.

Impact: tool calls to that MCP server fail.
Mitigation: circuit breaker per MCP connection (see 09-mcp-integration.md). Per-connection health checks. Timeout enforcement. The run step fails with a clear error — the agent can retry or escalate.

**Database (internal)**:
- PostgreSQL is down or unreachable.
- Connection pool exhaustion.

Impact: total system failure. No reads or writes possible.
Mitigation: connection pool limits with queue. Health check detects database unavailability. Self-hosted: operator responsibility. Managed: automated failover with read replicas.

**Object Storage (internal)**:
- S3-compatible storage is unreachable.

Impact: artifact storage/retrieval fails. Runs that produce artifacts (screenshots, files) fail at the artifact storage step.
Mitigation: artifact storage is asynchronous and retried. The run itself can complete even if artifact storage fails temporarily. Artifacts are queued for retry.

**Worker (internal)**:
- Worker process crashes or hangs.

Impact: runs in progress on that worker fail.
Mitigation: health heartbeats from running workers (see 16-agent-control-plane.md). Orphaned run detection and recovery by the supervisor. Runs are retried on a new worker.

### Circuit Breakers

Circuit breakers prevent cascading failures when an external dependency is unhealthy. They apply to:

- **Model provider calls**: per-provider circuit breaker. Opens after 5 consecutive failures or error rate > 50% over 1 minute. Half-open after 30 seconds (allow one probe request). Closes after 3 consecutive successes.
- **MCP server calls**: per-connection circuit breaker. Same thresholds as model providers.
- **Git remote operations**: per-remote circuit breaker. Opens after 3 consecutive failures. Half-open after 60 seconds.

When a circuit breaker is open:
- New requests to that dependency fail immediately with a clear error (not a timeout).
- The operator is notified via the activity feed and/or dashboard alert.
- The system does not attempt to fail over to a different service — it fails fast and lets the agent/operator decide what to do.

### Graceful Degradation

When external dependencies are unavailable, OtterCamp degrades gracefully rather than failing entirely:

- **Model provider down**: runs that require that provider fail. Runs using other providers continue. The queue holds new requests for the down provider. The operator can switch agents to a different model profile.
- **MCP server down**: tool calls to that server fail. Agents can continue work that doesn't require that tool. The failed tool call is surfaced in the run timeline.
- **Object storage down**: artifact storage is deferred and retried. Core functionality (chat, task management, model calls) continues.
- **Memory retrieval slow**: passive injection falls back to cached results or skips injection entirely rather than blocking the agent's turn. Active queries return a partial-results indicator.

### Incident Classification

For managed deployments, incidents are classified by impact:

| Severity | Definition | Response |
|---|---|---|
| S1 — Critical | Total service outage or data loss risk | Immediate response. All hands. |
| S2 — Major | Core functionality degraded for multiple orgs | Response within 15 minutes. |
| S3 — Minor | Non-core functionality degraded, or single org affected | Response within 1 hour. |
| S4 — Low | Cosmetic issues, non-urgent bugs | Next business day. |

Incident playbooks are operational documentation, not product spec content. They are maintained separately and linked from the operator handbook.

## Cost Controls

### Cost Tracking Architecture

Cost tracking is built into the execution pipeline at the point where costs are incurred: model invocations.

Every `ModelInvocation` record (see 16-agent-control-plane.md) captures:

```sql
-- These fields exist on the model_invocation table (defined in doc 16)
-- Repeated here for cost-tracking context:
--   provider          text not null
--   model             text not null
--   input_tokens      int not null
--   output_tokens     int not null
--   cost_estimate     numeric(10,6) not null   -- in USD
--   organization_id   uuid not null
--   project_id        uuid                     -- nullable, for org-level calls
--   agent_id          uuid not null
--   run_id            uuid not null
--   created_at        timestamptz not null
```

Cost is calculated at invocation time using a provider pricing table maintained in the application configuration. Pricing is per-model, per-token-type (input vs output), and updated when providers change their pricing. The cost estimate is stored on each invocation record for historical accuracy — even if pricing changes later, the recorded cost reflects what was charged at the time.

### Cost Aggregation

Raw per-invocation costs are aggregated into summary tables for efficient querying:

```sql
create table cost_summary (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  project_id      uuid references project(id),         -- null = org-level aggregate
  agent_id        uuid references agent(id),            -- null = project or org-level aggregate
  provider        text not null,
  model           text not null,
  period_start    timestamptz not null,                  -- start of the aggregation period
  period_type     text not null,                         -- hourly, daily, monthly
  input_tokens    bigint not null default 0,
  output_tokens   bigint not null default 0,
  invocation_count int not null default 0,
  total_cost      numeric(12,6) not null default 0,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),

  unique (organization_id, project_id, agent_id, provider, model, period_start, period_type)
);

create index on cost_summary (organization_id, period_start, period_type);
create index on cost_summary (organization_id, project_id, period_start) where project_id is not null;
```

Aggregation runs as a periodic background job:
- **Hourly summaries**: computed every hour from raw `ModelInvocation` records.
- **Daily summaries**: computed from hourly summaries at end of day.
- **Monthly summaries**: computed from daily summaries at end of month.

Raw `ModelInvocation` records are retained for 30 days (see Retention). After that, only the aggregated summaries remain.

### Budget System

Budgets are configurable at two levels:

**Org-level budget**: total spend cap for the entire organization across all projects and agents.

**Project-level budget**: spend cap for a specific project across all agents working on it.

```sql
create table cost_budget (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  project_id      uuid references project(id),         -- null = org-level budget
  period_type     text not null,                        -- daily, monthly
  soft_limit      numeric(10,2),                        -- warning threshold in USD (nullable = no soft limit)
  hard_limit      numeric(10,2),                        -- block threshold in USD (nullable = no hard limit)
  created_by_type text not null check (created_by_type in ('human', 'system')),
  created_by_id   uuid,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),

  unique (organization_id, project_id, period_type)
);

create index on cost_budget (organization_id);
```

### Budget Enforcement

Budget checks happen in the control plane execution broker, before a model invocation is dispatched:

1. **Pre-check**: before dispatching a run that will involve model calls, the broker checks current period spend against the applicable budgets (org + project).
2. **Soft limit**: if current spend exceeds the soft limit, the run proceeds but a warning is emitted. The warning appears in the operator's activity feed and on the cost dashboard. A warning is emitted at most once per budget per period (not on every invocation after the limit).
3. **Hard limit**: if current spend exceeds the hard limit, the run is **blocked**. The policy evaluation returns `deny` with reason `budget_exceeded`. The agent receives a clear error. An inbox item is created for the operator with the details.

### Cost Limit Behavior: Fail Closed

**This is a critical design decision.** When a hard cost limit is reached, the system blocks execution entirely. It does NOT silently degrade to a cheaper model.

Rationale:
- The human chose the model for a reason. Silently switching to a cheaper model changes the quality of work without the human's knowledge or consent.
- Cost spikes are usually a sign of something unexpected happening (a loop, an overly broad task, a poorly scoped agent). The right response is to stop and surface the issue, not to keep going at lower quality.
- The human can explicitly switch to a cheaper model profile if they want to continue at lower cost. That is a conscious decision.

When execution is blocked by a cost limit:
1. The run fails with status `budget_exceeded`.
2. An inbox item is created for the operator: "Project X has exceeded its daily budget of $Y. Current spend: $Z. [Review spend] [Increase limit] [Switch model]."
3. The operator can: increase the budget, switch the project to a cheaper model profile, or investigate the spend pattern.
4. Queued runs for the affected scope remain queued until the budget is reset (next period) or increased.

### Cost Dashboard (Web UI)

The cost dashboard is part of the operational dashboards in the web UI:

- **Current period spend**: real-time gauge showing spend against budget for org and each project.
- **Spend breakdown**: by provider, model, project, and agent. Interactive drill-down.
- **Trend chart**: daily spend over the last 30 days. Trendline projection to end of current period.
- **Top consumers**: ranked list of agents by spend in the current period.
- **Anomaly indicator**: highlighted when current spend rate significantly exceeds the 7-day average.
- **Budget status**: for each configured budget, show utilization percentage and whether soft/hard limits have been hit.

### Anomaly Detection

The system detects unusual cost patterns and alerts the operator:

**Spike detection**: if the hourly spend rate exceeds 3x the 7-day hourly average, an alert is emitted. This catches runaway agents, infinite loops, or unexpectedly expensive operations.

**Implementation**: a background job runs every 15 minutes:
1. Calculate current hour's spend from raw invocation records.
2. Compare against the average hourly spend over the last 7 days (from hourly summaries).
3. If current > 3x average, emit an alert to the operator's activity feed and create an inbox item.
4. The alert is per-org and deduplicated — at most one spike alert per hour per org.

**Zero-to-something detection**: if an org had zero spend in the previous 24 hours and suddenly incurs cost, flag it as informational (not necessarily anomalous, but worth noting in the activity feed).

### Provider Pricing Configuration

Model provider pricing is stored in application configuration, not in the database:

```yaml
model_pricing:
  anthropic:
    claude-sonnet-4-20250514:
      input_per_million: 3.00
      output_per_million: 15.00
    claude-haiku-3-5:
      input_per_million: 0.80
      output_per_million: 4.00
  openai:
    gpt-4o:
      input_per_million: 2.50
      output_per_million: 10.00
  # ... etc
```

Pricing is maintained by the OtterCamp team for managed deployments. Self-hosted operators can override pricing in their configuration. When a model is not found in the pricing table, cost is recorded as $0.00 with a warning log — the invocation still succeeds, but cost tracking is incomplete.

## Resolved Decisions

1. **No compliance certification at V2 GA.** GDPR-aware design (data isolation, deletion, export) but not GDPR-certified. SOC 2, HIPAA are post-GA. The architecture supports future certification without fundamental redesign.

2. **Default retention: 90 days for transcripts, indefinite for memories, 1 year for audit events.** Configurable per org. Retention is enforced by a daily background job. Audit events are archived before deletion, never hard-deleted without archival.

3. **Cost limits fail closed.** Block execution, notify human, create inbox item. Do NOT silently degrade to cheaper models. The human makes model selection decisions consciously. This is the answer to the open question from the stub.

4. **Structured logging with JSON format.** Every log entry includes timestamp, level, message, trace_id, and component. Organization and principal context are included when available. Secret scrubbing before write.

5. **OpenTelemetry-compatible traces.** Distributed tracing follows requests through API, worker, model calls, and tool executions. Exportable to Jaeger, Zipkin, or any OTLP backend.

6. **Prometheus-compatible metrics.** Exposed via `/metrics` endpoint (pull model). Optionally pushed via OTLP for managed deployments. Four categories: API, model, queue/execution, memory.

7. **Operational dashboards built into the web UI.** Not a separate tool. Overview, cost, performance, and agent dashboards. Self-hosted operators can also export to Grafana/Datadog via Prometheus scrape or OTLP export.

8. **Secrets encrypted at rest with AES-256-GCM.** Master key provided by operator (env var or secret manager) for self-hosted. KMS-managed for managed deployments. Secrets never in prompts, logs, API responses, or memory.

9. **Secret safety rules are codebase-wide invariants.** Never in prompts, never in logs, never in API responses, never in audit events, never in memory. These are not guidelines — they are enforced by the scrubbing layer and extraction pipeline.

10. **Defense in depth: six security layers.** Org isolation, authentication, authorization, control plane gating, agent sandboxing, audit trail. No single layer is the sole defense.

11. **Threat model centered on rogue agent behavior, not external attackers.** The primary threats are agents acting beyond their boundaries, cost explosions, and secret exposure. External attack surface (DDoS, brute force) matters for managed deployments and is handled at the infrastructure layer.

12. **Privacy: GDPR-aware but not certified.** Right to access, erasure, and portability are architecturally supported. Data minimization via retention policies. Formal compliance is post-GA.

13. **Circuit breakers on all external dependencies.** Model providers, MCP servers, git remotes. Open after consecutive failures or high error rate. Half-open after configurable interval. Fail fast, not fail slow.

14. **Cost aggregation: hourly, daily, monthly summaries.** Raw invocation records retained for 30 days. Summaries retained indefinitely. Aggregation runs as a periodic background job.

15. **Anomaly detection via hourly spend rate comparison.** 3x the 7-day average triggers an alert. Deduplicated to at most one alert per hour per org.

16. **Budget enforcement in the control plane broker.** Pre-check before dispatching model invocations. Soft limits warn (once per period). Hard limits block (deny with reason `budget_exceeded`).

17. **Redaction is human-only.** Agents cannot redact chat messages. This prevents agents from covering their tracks.

18. **Trace sampling defaults to 100%.** Error traces always recorded regardless of sampling rate. Configurable for high-volume deployments.

19. **Provider pricing in application configuration, not database.** Maintained by OtterCamp team for managed. Overridable by self-hosted operators. Unknown models tracked at $0.00 with warning.

20. **Master key rotation is supported without downtime.** Application supports reading secrets encrypted with any known key version. Background job re-encrypts all secrets with the new key.

## Open Questions

1. **Per-agent budget limits**: should budgets be configurable per agent in addition to per-org and per-project? This would let the operator cap a specific agent's spend. Current design requires the operator to limit the agent's model profile or project assignment instead.

2. **Cost allocation for shared operations**: when an org-level agent (Frank, Ellie) performs work that benefits a specific project, should the cost be allocated to the project or to the org? Currently, cost follows the `project_id` on the `ModelInvocation` — org-level agents without a project context are org-level costs.

3. **Alerting channels**: the built-in alerting surfaces to the web UI activity feed and inbox. Should OtterCamp also support external alerting (email, Slack webhook, PagerDuty) for cost and reliability alerts? Or is that a post-GA integration?

4. **Log retention for self-hosted**: should OtterCamp offer built-in log rotation and retention, or is stdout-to-infrastructure sufficient? Current design outputs to stdout and lets the deployment environment handle retention.
