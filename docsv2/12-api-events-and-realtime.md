---
## Summary

This spec defines OtterCamp's entire API surface, event system, realtime communication contracts, and CLI client. The API is the single access layer — every piece of OtterCamp functionality is accessible through it, with no UI-only features. It is a versioned REST API (all routes under `/v1/`) that uses standard REST for CRUD operations and explicit command endpoints (verb-noun POST routes like `/tasks/:id/advance-flow`) for actions with side effects. All responses use a consistent JSON envelope with `data`/`error` and `meta` fields. List endpoints use cursor-based pagination exclusively. Authentication is via Bearer token (API keys) for programmatic access and session cookies for the web UI; all requests are scoped to a single organization with row-level enforcement. Section 4 contains a comprehensive endpoint catalog covering every domain: auth, chat sessions/messages, projects, tasks/flow, flow templates, inbox, merge queue, scheduling, agents, memory, models, MCP connections, skills, tools/policies, control plane, system integration, delivery/environments, and events.

The event system is the central nervous system of the platform. Every significant state change emits a domain event into a durable `domain_event` PostgreSQL table. Events follow a uniform structure with dot-namespaced types (`{domain}.{entity}.{action}`), a monotonic sequence number (`seq`) for strict ordering, an actor (human/agent/system), hierarchical scope fields (org, project, task, session) denormalized as columns for efficient filtering, and a type-specific JSON payload. Each consumer (realtime push, memory pipeline, notification evaluator, internal reactions) independently tracks its position in the event log via a `consumer_cursor` table. Events are written transactionally with domain operations; LISTEN/NOTIFY provides near-instant consumer wake-up, with polling as fallback. Consumers receive at-least-once delivery and must be idempotent. A slow or crashed consumer catches up from its own cursor without affecting others. The spec defines roughly 140 event types across 13 domains (chat, task, flow, dependency, agent, memory, model, control plane, project, merge/delivery, system/inbox).

For realtime transport, SSE is the default (simpler, works through proxies, built-in reconnection via `Last-Event-ID`), with WebSocket available as an opt-in secondary channel for bidirectional needs like typing indicators. Clients subscribe to hierarchical scopes (`org:id`, `project:id`, `task:id`, `session:id`) and receive filtered events. Asynchronous work is processed through a PostgreSQL-backed job queue (no Redis) using `SELECT ... FOR UPDATE SKIP LOCKED` for concurrent worker claiming, with exponential backoff retries, dead letter handling, priority tiers (100 for sync/human-waiting, 50 for standard async, 25 for background, 10 for maintenance), and stale claim recovery. The spec also covers idempotency keys (org-scoped, 24-hour TTL, stored in an `idempotency_key` table) and notes that webhooks are deferred to V2.1 but the event log is designed to support them. There is no API-layer rate limiting — the only rate-limiting concern is upstream LLM providers, handled at the model layer (doc 07). Four database tables are introduced: `domain_event`, `consumer_cursor`, `job_queue`, and `idempotency_key`.

---

# 12. API, Events, and Realtime Contracts

> Status: Draft
> Depends on: 01-architecture-and-domain.md (architecture, eventing model), 02-chat.md (realtime event payloads, streaming), 03-projects-and-task-flow.md (task lifecycle, merge queue, inbox), 03a-shipping-and-delivery.md (deploy events, push hooks), 04-auth-tenancy-and-identity.md (auth model, tenancy), 07-models-and-inference.md (rate limiting at model layer), 16-agent-control-plane.md (control plane API surface)

## Purpose

Define the API surface, event system, and realtime contracts that bind together every component of OtterCamp. The API is the backbone — all clients (TUI, web, mobile) connect through it. The event system is the nervous system — domain events drive realtime updates, memory capture, notification delivery, inbox creation, and (eventually) webhook dispatch.

---

## 1. API Design Principles

### API-First: The Single Access Layer

Every piece of OtterCamp functionality is available through the API. There is no functionality that exists only in the web UI, only in the TUI, or only accessible to agents. If a human can do it in the browser, they can do it via the API and the CLI. If an agent can do it internally, a human can inspect and replicate it through the same API surface.

This is a hard constraint, not an aspiration. It means:

- **No UI-only features.** Every button, form, and action in the web UI maps to an API call. The web UI is a client of the API, not a separate application.
- **No hidden internal APIs.** Agent-internal operations (tool execution, memory extraction, flow advancement) have corresponding human-accessible API endpoints for inspection, debugging, and manual override.
- **The CLI wraps the API.** The `ottercamp` CLI (Section 14) provides complete access to every API endpoint with self-documentation and shell completion. Anything you can do in the UI, you can script.
- **Scriptability is a design requirement.** Every operation that a human might want to automate (create a project, assign agents, trigger a deploy, query memories) has a clean, scriptable API path.

### REST for CRUD, Commands for Actions

The API uses REST semantics for resource CRUD (create, read, update, delete) and explicit command endpoints for actions that are not pure CRUD. The distinction matters because many operations have side effects beyond writing a row.

- **CRUD endpoints** follow standard REST patterns: `POST /v1/projects` creates a project, `GET /v1/projects/:id` reads it, `PATCH /v1/projects/:id` updates it.
- **Command endpoints** model actions with side effects: `POST /v1/tasks/:id/advance-flow` advances a task's flow (which triggers scheduling, events, and possibly async work). `POST /v1/sessions/:id/cancel-turn` cancels an in-progress turn. Commands use verb-noun naming in the URL path.

A PATCH to a task's `work_status` is wrong — status transitions have validation rules, trigger events, and may cascade to dependencies. These are commands, not field updates. The REST patch route must fail closed on task state-machine fields such as `work_status`, `flow_template_id`, and `current_flow_node_id` rather than silently ignoring them or trying to reinterpret them as metadata edits.

### JSON Throughout

All request and response bodies are JSON. No XML, no form encoding, no multipart except for file uploads (which use `multipart/form-data` and return JSON). Content-Type is always `application/json` unless explicitly noted.

### Consistent Envelope

Every response uses the same envelope shape (see Section 5). Clients never have to guess the response structure.

### Scoping

All API requests are scoped to an organization. The org is determined from the authenticated principal's context (API key org binding or session cookie). There is no cross-org API access.

### Naming Conventions

- URL paths use lowercase kebab-case: `/v1/chat-sessions`, `/v1/flow-templates`.
- JSON fields use snake_case: `created_at`, `work_status`, `flow_node_id`.
- Resource IDs are UUIDs throughout.
- Boolean fields use affirmative naming: `is_current`, `requires_human_review` (not `no_review_needed`).

---

## 2. API Versioning

### Version in URL Path

All API endpoints are prefixed with a version: `/v1/...`. The version is part of the URL, not a header.

### Breaking vs Non-Breaking Changes

- **Breaking changes** increment the version number. Breaking changes include: removing a field, changing a field's type, changing the semantics of an endpoint, removing an endpoint, changing error codes.
- **Non-breaking additions** do not require a new version. Non-breaking changes include: adding a new optional field to a response, adding a new endpoint, adding a new event type, adding a new enum value to an existing field.

Clients should ignore unknown fields in responses (forward-compatible parsing).

### Version Lifecycle

- Only one version is active at a time in V2. There is no version compatibility split between self-host and cloud — they run the same API version.
- When a new version ships, the previous version is supported for a deprecation period (minimum 6 months for managed, self-host controls their own timeline).
- Deprecation is communicated via a `Sunset` response header on the old version and documentation.

---

## 3. Authentication

### Bearer Token (API Key)

API keys are created and managed per the auth model in doc 04. Each key is scoped to an organization and carries the permissions of the human or agent it was created for.

```
Authorization: Bearer oc_full_a1b2c3d4e5f6...
```

API keys use three scope prefixes: `oc_full_` (full access), `oc_read_` (read-only), `oc_chat_` (chat-only). See doc 04 for the authoritative API key format.

API keys are the primary auth mechanism for programmatic access — the TUI, CI integrations, and any external tooling.

### Session Cookie

The web UI uses session cookies for authentication. Cookie-based auth follows standard secure practices: `HttpOnly`, `Secure`, `SameSite=Lax`, short-lived with refresh.

### Agent Authentication

Agents authenticate via internal service credentials, not API keys. Agent requests carry the agent's principal identity and are validated by the control plane (doc 16). Agents never hold user-facing API keys.

### All Requests Scoped to Org

Every authenticated request resolves to an `organization_id`. The API enforces row-level access — no request can read or write data outside the authenticated org. This is enforced at the API layer, not just the database layer.

---

## 4. Endpoint Catalog

The catalog below shows the shape of each endpoint family. Full request/response schemas are defined in the implementation, not in this spec. Each family references its authoritative spec document.

### Auth and Org (doc 04)

```
POST   /v1/auth/login                    Session login
POST   /v1/auth/logout                   Session logout
POST   /v1/auth/refresh                  Refresh session token
GET    /v1/auth/me                       Current user profile + org membership

GET    /v1/organizations/:id             Org details
PATCH  /v1/organizations/:id             Update org settings
GET    /v1/organizations/:id/members     List org members
POST   /v1/organizations/:id/api-keys    Create API key
GET    /v1/organizations/:id/api-keys    List API keys
DELETE /v1/organizations/:id/api-keys/:key_id  Revoke API key
```

### Chat Sessions and Messages (doc 02)

```
POST   /v1/chat-sessions                 Create session (scope_type + scope_id)
GET    /v1/chat-sessions                 List sessions (filterable by scope)
GET    /v1/chat-sessions/:id             Session detail
PATCH  /v1/chat-sessions/:id             Update session (title, mode)
POST   /v1/chat-sessions/:id/close       Close session (command)

GET    /v1/chat-sessions/:id/messages    List messages (cursor-paginated)
POST   /v1/chat-sessions/:id/messages    Send message (human input)
PATCH  /v1/chat-sessions/:id/messages/:mid  Edit queued message (pending only)
DELETE /v1/chat-sessions/:id/messages/:mid  Delete queued message (pending only)
POST   /v1/chat-sessions/:id/messages/:mid/steer  Steer to this message (command)

POST   /v1/chat-sessions/:id/cancel-turn Cancel in-progress turn (command)

GET    /v1/chat-sessions/:id/participants  List participants
POST   /v1/chat-sessions/:id/participants  Invite participant
DELETE /v1/chat-sessions/:id/participants/:pid  Remove participant

GET    /v1/chat-sessions/:id/turns       List turns
GET    /v1/chat-sessions/:id/artifacts   List artifacts

POST   /v1/chat-sessions/:id/messages/:mid/reactions  Add/update reaction
DELETE /v1/chat-sessions/:id/messages/:mid/reactions/:rid  Remove reaction

POST   /v1/chat-sessions/:id/read-cursor Update read cursor (sequence number)
```

### Projects (doc 03)

```
POST   /v1/projects                      Create project
GET    /v1/projects                      List projects
GET    /v1/projects/:id                  Project detail
PATCH  /v1/projects/:id                  Update project (name, context_block, delivery config)
POST   /v1/projects/:id/archive          Archive project (command)
```

### Tasks and Flow (docs 03, 03a)

```
POST   /v1/projects/:pid/tasks           Create task
GET    /v1/projects/:pid/tasks           List tasks (filterable by status, priority, assignee)
GET    /v1/tasks/:id                     Task detail (includes current flow state)
PATCH  /v1/tasks/:id                     Update task fields (description, criteria, priority)

POST   /v1/tasks/:id/queue               Transition draft -> queued (command)
POST   /v1/tasks/:id/advance-flow        Advance flow to next node (command)
POST   /v1/tasks/:id/reject-review       Reject review with feedback (command)
POST   /v1/tasks/:id/approve-review      Approve review (command)
POST   /v1/tasks/:id/hold                Put task on hold (command)
POST   /v1/tasks/:id/resume              Resume held task (command)
POST   /v1/tasks/:id/cancel              Cancel task (command)

GET    /v1/tasks/:id/subtasks            List subtasks
POST   /v1/tasks/:id/subtasks            Create subtask
PATCH  /v1/tasks/:id/subtasks/:sid       Update subtask
POST   /v1/tasks/:id/subtasks/:sid/complete  Complete subtask (command)

GET    /v1/tasks/:id/dependencies        List dependencies
POST   /v1/tasks/:id/dependencies        Add dependency
DELETE /v1/tasks/:id/dependencies/:did   Remove dependency

GET    /v1/tasks/:id/events              Task event log (audit trail)

GET    /v1/tasks/:id/participants        Task participants
POST   /v1/tasks/:id/participants        Add participant (command)
DELETE /v1/tasks/:id/participants/:pid   Remove participant
```

### Flow Templates (doc 03)

```
POST   /v1/projects/:pid/flow-templates  Create flow template
GET    /v1/projects/:pid/flow-templates  List flow templates
GET    /v1/flow-templates/:id            Template detail (includes nodes and edges)
PATCH  /v1/flow-templates/:id            Update template metadata (name, description, is_current)
POST   /v1/flow-templates/:id/new-version  Create versioned copy (command)

POST   /v1/flow-templates/:id/nodes      Add a node
PATCH  /v1/flow-templates/:id/nodes/:nid  Update node (actor, edges, description)
DELETE /v1/flow-templates/:id/nodes/:nid  Remove node

POST   /v1/flow-templates/:id/nodes/:nid/skills  Attach skill to flow node
DELETE /v1/flow-templates/:id/nodes/:nid/skills/:sid  Detach skill from flow node
```

### Inbox (doc 03)

```
GET    /v1/inbox                         List inbox items (filterable by status, type)
GET    /v1/inbox/:id                     Inbox item detail
POST   /v1/inbox/:id/act                 Act on inbox item (command: approve, reject, defer, respond)
```

### Merge Queue (doc 03)

```
GET    /v1/projects/:pid/merge-queue     List merge queue entries
GET    /v1/merge-queue/:id               Entry detail
```

### Scheduling (doc 03)

```
POST   /v1/projects/:pid/schedules       Create task schedule
GET    /v1/projects/:pid/schedules       List schedules
GET    /v1/schedules/:id                 Schedule detail
PATCH  /v1/schedules/:id                 Update schedule
POST   /v1/schedules/:id/pause           Pause schedule (command)
POST   /v1/schedules/:id/resume          Resume schedule (command)
```

### Agents (doc 05)

```
POST   /v1/agents                        Create agent (optionally from template_id)
GET    /v1/agents                        List agents (filterable by class, status)
GET    /v1/agents/:id                    Agent detail (profile, capabilities, assignments)
PATCH  /v1/agents/:id                    Update agent profile
POST   /v1/agents/:id/activate           Activate agent (command)
POST   /v1/agents/:id/pause              Pause agent (command)
POST   /v1/agents/:id/retire             Retire agent (command)

GET    /v1/agents/:id/skills             List skills attached to agent
POST   /v1/agents/:id/skills             Attach skill to agent
DELETE /v1/agents/:id/skills/:sid        Detach skill from agent
```

### Agent Project Assignments (doc 05)

```
GET    /v1/projects/:pid/agents          List agents assigned to project
POST   /v1/projects/:pid/agents          Assign agent to project (role, is_primary)
PATCH  /v1/projects/:pid/agents/:aid     Update assignment (role)
DELETE /v1/projects/:pid/agents/:aid     Unassign agent from project
```

### Agent Profile Templates (doc 05)

```
GET    /v1/agent-templates               Browse templates (filterable by domain_tags, class)
GET    /v1/agent-templates/:id           Template detail
POST   /v1/agent-templates               Create org-customized template
PATCH  /v1/agent-templates/:id           Update org template
DELETE /v1/agent-templates/:id           Deactivate template
POST   /v1/agent-templates/:id/fork      Fork a shipped template into org-customized (command)
```

### Memory (doc 06)

```
POST   /v1/memory/query                  Query memory (Ellie's retrieval pipeline)
GET    /v1/memory/items                  List memory items (filterable by scope, type, entity)
GET    /v1/memory/items/:id              Memory item detail
PATCH  /v1/memory/items/:id              Edit memory item (content, tags, scope)
POST   /v1/memory/items/:id/archive     Archive a memory item (command)
POST   /v1/memory/items/:id/feedback     Submit feedback on a memory item
GET    /v1/memory/entities               List known entities
GET    /v1/memory/entities/:id           Entity detail with related memories

GET    /v1/memory/taxonomy               Browse taxonomy tree
POST   /v1/memory/taxonomy               Create taxonomy node
PATCH  /v1/memory/taxonomy/:id           Rename or reparent node
DELETE /v1/memory/taxonomy/:id           Prune taxonomy node

POST   /v1/memory/import                 Upload JSONL zip for import
GET    /v1/memory/imports                List import jobs
GET    /v1/memory/imports/:id            Import job detail (progress, errors)

POST   /v1/memory/consolidate            Trigger consolidation run (dedup, synthesis, decay)
GET    /v1/memory/consolidation-runs     List consolidation runs
GET    /v1/memory/consolidation-runs/:id  Run detail (type, status, counts)
```

### Models and Inference (doc 07)

```
GET    /v1/model-profiles                List model profiles
POST   /v1/model-profiles                Create model profile
GET    /v1/model-profiles/:id            Model profile detail
PATCH  /v1/model-profiles/:id            Update model profile

GET    /v1/model-providers               List provider connections
POST   /v1/model-providers               Create provider connection
GET    /v1/model-providers/:id           Provider connection detail (health, usage)
PATCH  /v1/model-providers/:id           Update provider connection
DELETE /v1/model-providers/:id           Remove provider connection
POST   /v1/model-providers/:id/test      Test provider connection (command)
```

### MCP Connections (doc 09)

```
POST   /v1/mcp/connections               Create MCP connection
GET    /v1/mcp/connections               List connections
GET    /v1/mcp/connections/:id           Connection detail (includes available tools)
PATCH  /v1/mcp/connections/:id           Update connection config
DELETE /v1/mcp/connections/:id           Remove connection
POST   /v1/mcp/connections/:id/test      Test connection (command)
POST   /v1/mcp/connections/:id/refresh   Refresh tool catalog
GET    /v1/mcp/connections/:id/catalog   List cataloged tools
PATCH  /v1/mcp/connections/:id/catalog/:entry_id  Toggle tool enabled/disabled
GET    /v1/mcp/connections/:id/executions  Execution log
```

### Skills (doc 10)

```
POST   /v1/skills                        Create skill
GET    /v1/skills                        List skills (filterable by scope, is_default, source)
GET    /v1/skills/:id                    Skill detail
PATCH  /v1/skills/:id                    Update skill (content, metadata, is_default)
DELETE /v1/skills/:id                    Delete skill
GET    /v1/skills/catalog                Browse shipped skill templates (system-provided)
```

### Tools and Tool Policy (doc 20)

```
GET    /v1/tools                         List registered tools (across all sources)
GET    /v1/tools/:id                     Tool detail (tier, policy, schema)
GET    /v1/tool-policies                 List tool policies
POST   /v1/tool-policies                 Create/update tool policy
```

### Control Plane (doc 16)

```
POST   /v1/control/runs                  Request an action run
GET    /v1/control/runs                  List runs (filterable by status, agent, project)
GET    /v1/control/runs/:id              Run detail
GET    /v1/control/runs/:id/events       Run event stream
GET    /v1/control/runs/:id/steps        List run steps
GET    /v1/control/runs/:rid/steps/:sid  Run step detail (attempts, output, timing)
GET    /v1/control/runs/:id/artifacts    List run artifacts
POST   /v1/control/runs/:id/cancel       Cancel run (command)
POST   /v1/control/runs/:id/retry        Retry a failed run (command)
GET    /v1/control/policies              List policies
POST   /v1/control/policies              Create policy
PUT    /v1/control/policies/:id          Replace policy
DELETE /v1/control/policies/:id          Delete policy
GET    /v1/control/policies/evaluate     Dry-run policy simulation
GET    /v1/control/cost/summary          Cost summary (filterable by time range, project, agent)
GET    /v1/control/health                Control plane health check
```

> **Note:** Capability approval is conversational (via inbox items), not a synchronous run-blocking mechanism. See doc 16.

### System Integration (doc 11)

```
POST   /v1/system/cli/execute            Execute CLI command (sandboxed)
POST   /v1/system/browser/sessions       Create/resume browser session
DELETE /v1/system/browser/sessions/:id   Close browser session
POST   /v1/system/browser/navigate       Browser navigation action
POST   /v1/system/browser/interact       Click, type, scroll, press key
GET    /v1/system/browser/screenshot     Capture browser screenshot
POST   /v1/system/browser/extract        Extract structured data from page
POST   /v1/system/browser/handoff        Create human handoff (transfer browser control)
POST   /v1/system/browser/handoff/:id/return  Return control from human
```

### Delivery and Environments (doc 03a)

```
GET    /v1/projects/:pid/remotes         List project remotes
POST   /v1/projects/:pid/remotes         Add remote
PATCH  /v1/projects/:pid/remotes/:rid    Update remote
DELETE /v1/projects/:pid/remotes/:rid    Remove remote
POST   /v1/projects/:pid/remotes/:rid/push  Manual push (command)

GET    /v1/projects/:pid/environments    List environments
GET    /v1/projects/:pid/environments/:eid  Environment detail (current deploy state)
```

### Events and System

> **Scope enforcement:** `oc_chat_` keys can only query `chat.*` events and subscribe to `session:{id}` scopes. `oc_read_` keys can query/subscribe to any scope. See Section 9 for details.

```
GET    /v1/events                        Query event log (filterable by type, scope, time range)
GET    /v1/events/:id                    Event detail
GET    /v1/events/stream                 SSE endpoint for realtime events
GET    /v1/ws                            WebSocket endpoint for bidirectional realtime

GET    /v1/health                        Health check (unauthenticated)
GET    /v1/version                       API version info (unauthenticated)
```

`GET /v1/version` returns the binary build identity that operators and clients can use to reason about freshness:

- `version`: semantic application version (`MAJOR.MINOR.PATCH` or `dev` locally)
- `commit`: embedded VCS revision for the running binary
- `built_at`: UTC build timestamp for the running binary
- `go_version`: Go runtime version used to build the binary
- `repo_version`: monotonic repo-stored build counter from `internal/version/repo_version.txt`

`repo_version` is part of the stale-binary contract described in doc 08 and surfaced in the TUI status bar (doc 17).

---

## 5. Request/Response Envelope

### Success Response

```json
{
  "data": { ... },
  "meta": {
    "request_id": "req_abc123",
    "timestamp": "2026-02-22T14:30:00Z"
  }
}
```

### Success Response (List)

```json
{
  "data": [ ... ],
  "meta": {
    "request_id": "req_abc123",
    "timestamp": "2026-02-22T14:30:00Z",
    "pagination": {
      "cursor": "eyJpZCI6Ij...",
      "has_more": true,
      "page_size": 50
    }
  }
}
```

### Error Response

```json
{
  "error": {
    "code": "task_invalid_transition",
    "message": "Cannot transition task from 'done' to 'in_progress'. Terminal states are final.",
    "details": {
      "current_status": "done",
      "requested_status": "in_progress",
      "allowed_transitions": []
    }
  },
  "meta": {
    "request_id": "req_abc123",
    "timestamp": "2026-02-22T14:30:00Z"
  }
}
```

### Envelope Rules

- **`data`** is always present on success. It is an object for single resources and an array for collections. Never null — empty collections return `[]`.
- **`error`** is present only on failure. Never both `data` and `error` in the same response.
- **`meta`** is always present. Contains `request_id` (for tracing and support), `timestamp`, and `pagination` for list endpoints.
- HTTP status codes are canonical: `200` for reads, `201` for creates, `204` for deletes, `4xx` for client errors, `5xx` for server errors.

### Pagination

All list endpoints use **cursor-based pagination**. No offset-based pagination — it is unreliable with concurrent writes.

- Request: `?cursor=eyJpZCI6Ij...&page_size=50`
- Default page size: 50. Maximum: 200.
- The cursor is an opaque, URL-safe string encoding the position. Clients must not parse or construct cursors.
- `has_more: true` means there are more results beyond the current page. The next cursor is included when `has_more` is true.

### Error Codes

Error codes are namespaced by domain and are stable — they do not change between versions.

```
auth_invalid_token          401  Token is invalid, expired, or revoked
auth_insufficient_scope     403  Token does not have required permissions
auth_org_mismatch           403  Resource belongs to a different organization

resource_not_found          404  Requested resource does not exist
resource_conflict           409  Resource state conflict (duplicate, version mismatch)

task_invalid_transition     422  Invalid work status transition
task_dependency_cycle       422  Adding this dependency would create a cycle
task_node_has_open_subtasks 422  Cannot advance flow — open subtasks remain

session_closed              422  Cannot post to a closed session
session_turn_in_progress    409  A turn is already in progress (for steer/cancel timing)

idempotency_conflict        409  Idempotency key reused with different parameters

internal_error              500  Unexpected server error
service_unavailable         503  Service temporarily unavailable (maintenance, overload)
```

---

## 6. Idempotency

### Idempotency Keys

All mutation endpoints (POST, PATCH, DELETE) accept an `Idempotency-Key` header.

```
Idempotency-Key: idem_a1b2c3d4e5
```

- The key is a client-generated unique string (UUID recommended).
- The server stores the key, request fingerprint (method + path + body hash), and response for a retention period (default: 24 hours).
- If the same key is sent again with the same request fingerprint, the server returns the stored response without re-executing the operation.
- If the same key is sent with a different request fingerprint, the server returns a `409 idempotency_conflict` error.
- Idempotency keys live in the org's database (db-per-org), so different orgs can use the same key string without conflict.

### Replay Safety

The idempotency mechanism makes all mutations replay-safe. Network timeouts, retries, and duplicate submissions are safe — the operation executes at most once per key.

Command endpoints (advance-flow, cancel-turn, etc.) are inherently non-idempotent in their semantics, but the key ensures the same command is not accidentally applied twice. For example, if `advance-flow` is called twice with the same idempotency key, the second call returns the result of the first.

### DDL: Idempotency Store

```sql
create table idempotency_key (
  key               text primary key,    -- org isolation is implicit (db-per-org)
  method            text not null,
  path              text not null,
  request_hash      text not null,       -- SHA-256 of canonical request body
  response_status   int not null,
  response_body     jsonb,             -- null for 204 responses
  created_at        timestamptz not null default now(),
  expires_at        timestamptz not null
);

create index on idempotency_key (expires_at);  -- cleanup query
```

A periodic job purges expired rows.

---

## 7. Event System

### The Durable Event Log

The event system is the backbone of OtterCamp's asynchronous communication. Domain events are emitted on every significant state change and written to a durable event log table. The log serves four primary consumers:

1. **Realtime push** (`realtime_push`) — events are pushed to connected clients via SSE/WebSocket.
2. **Memory pipeline** (`memory_pipeline`) — evaluates events for memory extraction, enqueues extraction jobs (doc 06).
3. **Notification evaluator** (`notification_evaluator`) — evaluates events against human preferences, enqueues delivery jobs (doc 02).
4. **Internal reactions** (`internal_reactions`) — inbox item creation, merge queue hooks, deploy task creation, dependency resolution, and any other system behavior triggered by state changes.

The event log is the single source of truth for "what happened." Other systems subscribe to it rather than directly coupling to each other.

### Event Structure

Every event has the same top-level structure:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "seq": 42,
  "type": "task.flow.advanced",
  "occurred_at": "2026-02-22T14:30:00.123Z",
  "actor": {
    "type": "agent",
    "id": "660f9500-f3ac-52e5-b827-557766550000"
  },
  "scope": {
    "organization_id": "770a0600-04bd-63f6-c938-668877660000",
    "project_id": "880b1700-15ce-74g7-d049-779988770000",
    "task_id": "990c2800-26df-85h8-e150-880099880000",
    "session_id": null
  },
  "payload": {
    "task_id": "990c2800-26df-85h8-e150-880099880000",
    "from_node_id": "aa0d3900-37e0-96i9-f261-991100990000",
    "to_node_id": "bb0e4a00-48f1-a7j0-0372-002211000000",
    "visit": 1
  },
  "metadata": {
    "idempotency_key": "idem_abc123",
    "request_id": "req_xyz789"
  }
}
```

**Field definitions:**

- `id` — globally unique event UUID.
- `seq` — monotonically increasing sequence number. Used for ordering, consumer cursors, and SSE reconnection. Guaranteed unique and strictly ordered even for events written in the same transaction.
- `type` — dot-namespaced event type (see Section 8).
- `occurred_at` — UTC timestamp with millisecond precision. When the event was created.
- `actor` — who caused the event. Type is `human`, `agent`, `system`, or `supervisor` (automated control-plane actions such as stuck task recovery). ID is the actor's entity UUID. System events (e.g., scheduled task creation) have actor type `system` and null ID. See A10 note below on `supervisor`.
- `scope` — hierarchical scope for event routing and filtering. All fields nullable except `organization_id`. A chat event has `session_id` set; a task event has `project_id` and `task_id` set; an org-level event has only `organization_id`.
- `payload` — event-type-specific data. Structure varies by type. Always a JSON object.
- `metadata` — operational metadata. Idempotency key, request ID, trace ID. Not part of the business event.

> **Note on `supervisor` actor type:** Doc 04's canonical principal convention defines three types: `human`, `agent`, `system`. The `supervisor` type extends this convention for domain events only — it distinguishes automated control-plane actions (stuck run recovery, timeout cancellation) from general system actions (scheduled task creation, retention purges). Code that switches on actor type must handle all four values. See doc 16 for supervisor behavior.

### DDL: domain_event

```sql
create table domain_event (
  id                uuid primary key default gen_random_uuid(),
  seq               bigint generated always as identity,  -- strict total ordering for consumer cursors
  type              text not null,
  occurred_at       timestamptz not null default now(),    -- when the event happened (business time)
  actor_type        text not null check (actor_type in ('human', 'agent', 'system', 'supervisor')),  -- supervisor: automated control-plane actions (e.g., run cancellation, stuck task recovery)
  actor_id          uuid,
  organization_id   uuid not null,                         -- structural consistency (db-per-org makes this implicit, but kept for query uniformity — see doc 04)
  project_id        uuid,
  task_id           uuid,
  session_id        uuid,
  payload           jsonb not null,
  metadata          jsonb not null default '{}'
);

-- Consumer pickup: strict ordering by sequence
create unique index on domain_event (seq);

-- Scope-based queries for client subscription filtering
create index on domain_event (organization_id, seq);
create index on domain_event (project_id, seq) where project_id is not null;
create index on domain_event (task_id, seq) where task_id is not null;
create index on domain_event (session_id, seq) where session_id is not null;

-- Type-based queries for consumers that care about specific event types
create index on domain_event (type, seq);

-- Retention management
create index on domain_event (occurred_at);
```

**Design notes:**

- The `seq` column is a `BIGSERIAL` identity that provides strict total ordering for consumer cursors. Unlike UUIDs or timestamps, sequence values are guaranteed unique and monotonically increasing even for events written in the same transaction. All consumer queries and indexes use `seq` for ordering.
- Events are truly append-only — no updates. Each consumer tracks its own position via the `consumer_cursor` table. This eliminates write contention on the event table from dispatch.
- The `scope` fields are denormalized from the event payload for efficient subscription filtering. Consumers do not need to parse the payload to route events.
- `organization_id` is present for structural consistency and query uniformity, not for tenant isolation (db-per-org provides that). See doc 04 for the rationale.
- A retention policy purges old events (configurable per org, default 90 days). Before purging, the system verifies all consumer cursors have advanced past the retention boundary. If a consumer is stalled for more than 24 hours, an alert fires and the operator can manually advance or disable that consumer to unblock retention purges. The effects of events — memories, notifications, audit records — persist independently of event log retention.
- The `id` column uses UUIDs for global uniqueness. The `evt_` prefix in the JSON representation is a display convention applied at the API layer, not stored in the database.

### Event Dispatch (Per-Consumer Cursors)

Each consumer independently tracks its position in the event log using a `consumer_cursor` table. This means consumers are decoupled — if the memory pipeline goes down for 5 minutes, it catches up from where it left off without affecting realtime push or notifications.

**How it works:**

1. A domain operation writes its primary data and the event row in the same database transaction.
2. After commit, the transaction issues `NOTIFY domain_events` to wake up any listening consumers immediately.
3. Each consumer process maintains its own cursor (the `seq` value of the last event it successfully processed). On startup or wake-up, it queries `domain_event WHERE seq > $cursor ORDER BY seq` to get unprocessed events.
4. After processing an event, the consumer advances its cursor in the `consumer_cursor` table.
5. If a consumer crashes, it resumes from its last committed cursor. Events are delivered at-least-once; consumers must be idempotent.

**Consumers:**

- **`realtime_push`** — fans out events to all connected SSE/WebSocket clients. This is one consumer that handles all connected clients (web UI, TUI, mobile app). The push infrastructure broadcasts to every connected socket; individual browser tabs and app instances are not separate consumers. Processes events in-line (no job queue hop — latency matters).
- **`memory_pipeline`** — evaluates each event for memory relevance. When an event warrants extraction, enqueues a `memory_extraction` job (Section 11) for the worker to process. The consumer is lightweight (a filter); the LLM call happens in the worker.
- **`notification_evaluator`** — evaluates each event against human notification preferences. When a notification should be sent, enqueues a `notification_delivery` job for the worker. The consumer is a filter; actual delivery (push notification, email) happens in the worker.
- **`internal_reactions`** — inbox item creation, dependency resolution, merge queue hooks, deploy task creation, and other system behaviors triggered by state changes. Most reactions are lightweight writes; complex reactions enqueue jobs.

**LISTEN/NOTIFY optimization:** PostgreSQL `LISTEN/NOTIFY` provides near-instant wake-up when new events are written. Consumers also poll on a fallback interval (every 5 seconds) to recover from missed notifications (which can happen if the connection drops momentarily). LISTEN/NOTIFY is a latency optimization, not a durability mechanism — the `consumer_cursor` table is the source of truth for each consumer's position.

**Attach-gap rule:** any consumer or SSE stream that reads backlog and then attaches `LISTEN` must immediately re-read backlog once after `LISTEN` succeeds and before it blocks on `WaitForNotification`. Without that post-attach drain/read, an event written in the gap between the initial backlog fetch and the completed `LISTEN` can be missed until the fallback poll interval. The implementation rule is: `read backlog -> attach LISTEN -> read backlog again -> block on notifications`.

### DDL: consumer_cursor

```sql
create table consumer_cursor (
  consumer_name   text primary key,          -- e.g., 'realtime_push', 'memory_pipeline'
  last_seq        bigint not null default 0,  -- seq of last successfully processed event (0 = start from beginning)
  last_event_at   timestamptz,               -- occurred_at of that event (for monitoring lag)
  updated_at      timestamptz not null default now()
);
```

**Bootstrap:** On first startup, the system inserts a row for each consumer with `last_seq = 0`. A consumer with `last_seq = 0` starts processing from the oldest available event. In practice, on a fresh install there are no events yet, so this is a no-op.

The lag between `last_event_at` and `now()` is a key health metric — if any consumer falls more than a few seconds behind, something is wrong.

---

## 8. Event Types

Events are organized by domain. Each event type is a dot-separated string: `{domain}.{entity}.{action}`.

> **Note:** Event payload structures are defined in their respective domain specs. This catalog lists event types with brief descriptions. See the domain spec (noted in the "Emitted by" column) for authoritative payload schemas.

### Chat Events

```
chat.session.created           A new chat session was created
chat.session.closed            A session was closed
chat.session.mode_changed      Session mode switched (sync <-> async)

chat.message.created           A message was added to a session
chat.message.delta             Streaming text chunk for an agent message
chat.message.finalized         A message reached final state
chat.message.failed            A message failed
chat.message.redacted          A message was redacted

chat.turn.started              An agent turn began
chat.turn.completed            An agent turn finished successfully
chat.turn.failed               An agent turn failed (model error, session closed)
chat.turn.stopped              An agent turn was interrupted (user_cancelled, user_steered, max_tool_calls, max_duration)

chat.summary.created           A progressive summary was generated for a message range
chat.read_cursor.updated       A human's read cursor advanced (payload: user_id, last_read_seq)

chat.listening_eval.completed  Listening eval finished, interjection decisions made

chat.participant.joined        A participant was added to a session
chat.participant.left          A participant left a session

chat.reaction.created          A reaction was added to a message
chat.reaction.updated          A reaction sentiment was changed
chat.reaction.deleted          A reaction was removed
```

### Task Events

> **Naming note:** Task events use the two-part form `task.{action}` rather than `task.task.{action}`. When the entity matches the domain, the entity segment is omitted for readability. Sub-entities (subtask, flow, dependency, participant) use the full three-part form.

```
task.created                   A new task was created
task.updated                   Task fields were updated (title, description, criteria)
task.status_changed            Work status transitioned (payload includes from/to)
task.queued                    Task moved to queued
task.started                   Task moved to in_progress (agent picked it up)
task.blocked                   Task became blocked (dependency added)
task.unblocked                 Task was unblocked (all dependencies resolved)
task.held                      Task put on hold
task.resumed                   Task resumed from hold
task.review_started            Task entered review (at a review node)
task.completed                 Task reached done
task.cancelled                 Task was cancelled

task.subtask.created           A subtask was created
task.subtask.status_changed    Subtask work status changed
task.subtask.completed         A subtask reached done

task.participant.added         A participant was added to a task
task.participant.removed       A participant was removed from a task
```

### Flow Events

```
task.flow.advanced             Flow moved to the next node
task.flow.rejected             Review node rejected — flow loops back
task.flow.node_started         A flow node execution began (new visit)
task.flow.node_completed       A flow node execution completed
task.flow.node_blocked         A flow node execution became blocked
```

### Dependency Events

```
task.dependency.added          A dependency was added
task.dependency.removed        A dependency was removed
task.dependency.resolved       A dependency task reached done
task.dependency.cancelled      A dependency task was cancelled (triggers resolution task)
```

### Agent Events

```
agent.created                  A new agent was created
agent.activated                Agent moved to active
agent.paused                   Agent was paused
agent.retired                  Agent was retired
agent.cancelled                Agent draft was rejected (never activated)
agent.expired                  Temp agent auto-expired (TTL or scope ended)
agent.promoted                 Temp agent promoted to staff
agent.profile_updated          Agent profile was updated (prompt, policies)
agent.project.assigned         Agent assigned to a project (payload: project_id, role)
agent.project.unassigned       Agent removed from a project
```

### Memory Events

```
memory.item.created            A new memory item was extracted
memory.item.updated            A memory item was updated (confidence, content)
memory.item.archived           A memory item was archived (manual or decay)
memory.item.superseded         A memory item was superseded by a newer one
memory.item.promoted           A task-scoped memory was promoted to project scope (on task completion)

memory.entity.created          A new entity was discovered during extraction
memory.entity.synthesized      An entity synthesis was completed

memory.contradiction.detected  A contradiction was found between memories (payload: new_id, superseded_id)

memory.consolidation.started   A consolidation run began (dedup, synthesis, decay, etc.)
memory.consolidation.completed A consolidation run finished (payload: run_type, counts)

memory.import.started          A memory import job began
memory.import.completed        A memory import job finished successfully
memory.import.failed           A memory import job failed
```

### Model Events

```
model.request.started          An LLM request began
model.request.completed        An LLM request succeeded
model.request.failed           An LLM request failed (rate limit, error, timeout)
model.request.fallback         An LLM request fell back to a secondary model
model.budget.warning           Token/cost budget approaching limit
model.budget.exceeded          Token/cost budget exceeded
```

### Control Plane Events

```
control.run.created            A control plane run was created
control.run.started            A run began execution
control.run.completed          A run completed successfully
control.run.failed             A run failed
control.run.cancelled          A run was cancelled
control.run.timed_out          Run exceeded time limit
control.run.paused             Run paused (e.g., browser handoff)
control.run.resumed            Run resumed after pause (e.g., handoff completed)
control.run.dead_lettered      Run exhausted retries and was dead-lettered
control.run.stuck_detected     Supervisor detected stuck run
control.run.escalated          Run escalated for human attention

control.policy.created         A capability policy was created
control.policy.updated         A capability policy was updated
control.policy.deleted         A capability policy was deleted
control.policy.evaluated       A policy evaluation occurred (allow/deny)
control.policy.denied          Policy evaluation denied the action
```

### Project Events

```
project.created                A new project was created
project.updated                Project settings were updated
project.archived               A project was archived
```

### Merge and Delivery Events

```
project.merge.queued           A task entered the merge queue
project.merge.started          A merge operation began
project.merge.completed        A merge succeeded
project.merge.conflict         A merge conflict was detected

project.push.succeeded         A push to a remote succeeded
project.push.failed            A push to a remote failed

project.deploy.started         A deploy task began execution
project.deploy.completed       A deploy task succeeded
project.deploy.failed          A deploy task failed
project.deploy.rollback        A rollback deploy was triggered

project.remote.created         A remote was added to a project
project.remote.updated         A remote configuration was changed
project.remote.removed         A remote was removed from a project

project.environment.deployed   An environment was updated to a new commit
```

### System Events

```
system.health.degraded         System health degraded (provider down, queue backlog)
system.health.recovered        System health recovered

system.schedule.created        A task schedule was created
system.schedule.updated        A task schedule was modified
system.schedule.paused         A task schedule was paused
system.schedule.resumed        A task schedule was resumed
system.schedule.fired          A task schedule tick created a new task
system.schedule.skipped        A schedule tick was skipped (overlap policy)

system.job.started             A background job started
system.job.completed           A background job completed
system.job.failed              A background job failed
system.job.dead_lettered       A job exhausted retries and was dead-lettered
```

### System Integration Events

```
system.cli.executed            A CLI command completed (payload: command, exit_code, risk_level)
system.cli.denied              A CLI command was denied by policy

system.browser.session_created A browser session was created (payload: task_id)
system.browser.session_closed  A browser session was closed (payload: close_reason)
system.browser.handoff_created Human handoff created (transfer browser control to human)
system.browser.handoff_completed  Human handoff completed (control returned)
system.browser.handoff_expired Human handoff expired (no human action within timeout)
system.browser.domain_blocked  Navigation to a blocked domain was denied
```

### MCP Events

| Event | Payload highlights | Emitted by |
|---|---|---|
| `mcp.connection.created` | connection_id, name, transport | MCP layer |
| `mcp.connection.updated` | connection_id, changed_fields | MCP layer |
| `mcp.connection.deleted` | connection_id, name | MCP layer |
| `mcp.connection.status_changed` | connection_id, old_status, new_status | MCP layer |
| `mcp.connection.health_changed` | connection_id, old_health, new_health | MCP layer |
| `mcp.catalog.changed` | connection_id, added_count, removed_count | MCP layer |
| `mcp.tool.executed` | connection_id, tool_name, duration_ms, status | MCP layer |
| `mcp.tool.denied` | connection_id, tool_name, agent_id, reason | MCP layer |
| `mcp.tool.preload_failed` | connection_id, tool_name, error | MCP layer |

### Inbox Events

```
inbox.item.created             A new inbox item was created
inbox.item.acted               An inbox item was acted on
inbox.item.deferred            An inbox item was deferred
```

---

## 9. Realtime Transport

### SSE as Default

Server-Sent Events (SSE) is the default realtime transport for server-to-client push. SSE is chosen over WebSocket as the primary channel because:

- **Simpler** — SSE is HTTP. It works through proxies, CDNs, and load balancers without special configuration.
- **Sufficient** — the majority of realtime needs are server-to-client push: streaming agent responses, task status changes, notification delivery, event updates.
- **Reliable reconnection** — SSE has built-in reconnection with `Last-Event-ID`, making it resilient to network interruptions without client-side complexity.
- **Debuggable** — SSE connections are visible in standard HTTP tooling. No protocol upgrade negotiation.

### WebSocket for Bidirectional Needs

WebSocket is available as a secondary transport for use cases that require bidirectional communication with lower overhead than repeated HTTP requests:

- **Chat input streaming** — when the client needs to stream partial input to the server (e.g., "user is typing" indicators, or future voice-to-text streaming).
- **Interactive tool sessions** — when a human is live-pairing with an agent and needs a persistent bidirectional channel.

WebSocket is NOT required for normal chat. Sending a message is a POST; receiving streamed responses is SSE. WebSocket is opt-in for clients that need it.

### SSE Endpoint

```
GET /v1/events/stream?scopes=org:uuid,project:uuid,session:uuid
```

- Authenticated via Bearer token or session cookie.
- `scopes` parameter defines what events the client wants. Multiple scopes can be subscribed simultaneously.
- **API key scope enforcement:** `oc_chat_` keys can only subscribe to `session:{id}` scopes and only receive `chat.*` event types. `oc_read_` keys can subscribe to any scope (read-only). `oc_full_` keys have unrestricted access. The server rejects scope subscriptions that exceed the key's permissions with `403 auth_insufficient_scope`.
- The server filters events from the durable event log and sends relevant ones to the client.
- Each SSE message includes:

```
id: 42
event: chat.message.delta
data: {"message_id":"550e8400-e29b-41d4-a716-446655440000","delta":{"text":"Here's what"}}

id: 43
event: task.status_changed
data: {"task_id":"660f9500-f3ac-52e5-b827-557766550000","from_status":"in_progress","to_status":"review"}
```

For flow-owned node transitions, `task.status_changed` may also carry `transition_source = "flow_transition"`, `flow_event_type` (`flow.advanced` or `flow.rejected`), and `to_flow_execution_id`. Consumers that also handle flow events should treat that metadata as the dedupe key that prevents starting a second generic wakeup for the same new execution.

- The `id` field is the event's `seq` value (the monotonic sequence number from the `domain_event` table). This provides strict ordering and makes reconnection trivial.
- On reconnect, the client sends `Last-Event-ID` (a numeric seq value) and the server replays all events with `seq > Last-Event-ID`.
- If `Last-Event-ID` references a seq that has been purged (older than the retention window), the server responds with the oldest available event and includes an `X-Events-Gap: true` header to signal that events were missed.
- The server sends periodic `:keepalive` comments (every 15 seconds) to prevent connection timeout.

### WebSocket Endpoint

```
GET /v1/ws?scopes=org:uuid,project:uuid
Upgrade: websocket
```

- Same authentication as SSE.
- Same scope-based subscription model.
- Client-to-server messages use a simple JSON protocol:

```json
{"action": "subscribe", "scope": "session:uuid"}
{"action": "unsubscribe", "scope": "session:uuid"}
{"action": "typing", "session_id": "uuid", "is_typing": true}
```

- Server-to-client messages use the same event structure as SSE.

---

## 10. Realtime Subscription Model

### Scope-Based Filtering

Clients subscribe to one or more scopes. The server filters events from the event log and sends only events whose scope matches the client's subscriptions.

**Scope hierarchy:**

- **`org:{id}`** — receives all events in the org. The broadest scope. Used by dashboards and admin views.
- **`project:{id}`** — receives all events for a project: task changes, flow events, merge queue, deploy events. Used by project views.
- **`task:{id}`** — receives events for a specific task: status changes, flow progression, subtask events. Used by task detail views.
- **`session:{id}`** — receives events for a specific chat session: messages, deltas, turns, reactions. Used by the chat pane.

Scope is hierarchical — subscribing to `org:{id}` includes all projects, tasks, and sessions in that org. Subscribing to `project:{id}` includes all tasks and sessions in that project. The server resolves the hierarchy and deduplicates.

### Subscription Management

- Clients can subscribe and unsubscribe to scopes dynamically over the same connection.
- For SSE, scope changes require a new connection (SSE is unidirectional — the client cannot send messages on the same connection). The reconnection mechanism via `Last-Event-ID` makes this seamless.
- For WebSocket, scope changes are sent as client messages on the existing connection.

### Client-Side Filtering

The server performs coarse scope filtering. Clients may receive events they do not need (e.g., subscribed to a project, but only displaying one task). Fine-grained filtering is the client's responsibility — the event type and payload provide enough information for the client to decide what to render.

### Backpressure

If a client falls behind (slow consumer), the server buffers up to a configurable limit (default: 1000 events). If the buffer overflows, the server drops the connection. The client reconnects and uses `Last-Event-ID` to catch up. Events that were dropped from the buffer are still in the durable event log and are replayed on reconnect.

WebSocket connections use the same buffer overflow policy. On disconnect, WebSocket clients must re-subscribe and fetch missed events from the `GET /v1/events` REST endpoint, as WebSocket has no built-in `Last-Event-ID` equivalent.

---

## 11. Internal Job/Worker API

### Architecture

The API service and Worker service are built from the same Go codebase. In local mode they run as goroutines within a single process; in server mode they can be split into separate processes for resource isolation (see doc 08 for deployment modes). They communicate via a PostgreSQL-backed job queue — no Redis, no external message broker. Fewer moving parts, fewer things to break.

### How It Works

1. The API service (or an event handler) inserts a job into the `job_queue` table.
2. The Worker service polls for claimable jobs using `SELECT ... FOR UPDATE SKIP LOCKED`.
3. The Worker claims a job, executes it, and updates the status.
4. On completion, the Worker writes results and emits domain events.

### Job Types

Jobs represent units of asynchronous work:

```
agent_turn              Execute an agent turn (the tool-call loop)
flow_node_start         Start a flow node execution (resolve actor, create session, kick off)
memory_extraction       Extract memories from an event
memory_consolidation    Run memory consolidation (dedup, synthesis, decay)
notification_delivery   Deliver a notification (push, email) for a matched event
merge_execute           Execute a merge queue entry
push_execute            Push main to a remote
deploy_task_create      Create a deploy task from a merge event
schedule_tick           Process a schedule tick (create task instance)
summary_generate        Generate a chat summary for a message range
cleanup_ephemeral       Purge ephemeral messages (daily)
idempotency_cleanup     Purge expired idempotency keys
event_log_retention     Purge old events past retention period
session_cleanup         Purge expired and revoked auth sessions (referenced by doc 04)
api_key_cleanup         Revoke expired API keys (referenced by doc 04)
```

### DDL: job_queue

```sql
create table job_queue (
  id              uuid primary key default gen_random_uuid(),
  job_type        text not null,
  priority        int not null default 0,          -- higher = picked first
  payload         jsonb not null,
  status          text not null default 'pending'
                  check (status in ('pending', 'claimed', 'running', 'completed', 'failed', 'dead_letter')),
  attempts        int not null default 0,
  max_attempts    int not null default 3,
  claimed_by      text,                             -- worker instance ID
  claimed_at      timestamptz,
  started_at      timestamptz,
  completed_at    timestamptz,
  failed_at       timestamptz,
  error_message   text,
  error_details   jsonb,
  result          jsonb,
  run_after       timestamptz not null default now(), -- for delayed/scheduled jobs and retry backoff
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

-- Primary pickup query: unclaimed jobs ready to run, ordered by priority then age
create index on job_queue (status, run_after, priority desc, created_at)
  where status = 'pending';

-- Stale claim detection: jobs that were claimed but never completed
create index on job_queue (status, claimed_at)
  where status in ('claimed', 'running');

-- Dead letter review
create index on job_queue (status, created_at)
  where status = 'dead_letter';

-- Job type queries for monitoring
create index on job_queue (job_type, status);
```

### Claim Protocol

```sql
-- Worker claims the next available job
update job_queue
set status = 'claimed',
    claimed_by = $1,  -- worker instance ID
    claimed_at = now(),
    attempts = attempts + 1,
    updated_at = now()
where id = (
  select id from job_queue
  where status = 'pending'
    and run_after <= now()
  order by priority desc, created_at
  limit 1
  for update skip locked
)
returning *;
```

`FOR UPDATE SKIP LOCKED` ensures multiple workers can poll concurrently without contention or double-claiming. Each worker gets a different job.

The job queue also uses PostgreSQL `LISTEN/NOTIFY` for immediate wake-up when new jobs are inserted (matching doc 08's `WORKER_POLL_INTERVAL` description). Workers fall back to polling on a configurable interval (default 1 second) if a notification is missed. The same attach-gap rule applies there: once `LISTEN` is established, the worker immediately does one synthetic wake/drain pass before sleeping on notifications so jobs inserted just before the subscription attached are not left waiting for the next poll tick.

### Retry Policy

- On failure, the job's status returns to `pending` with `run_after` set to the backoff time.
- Backoff is exponential: `base_delay * 2^(attempts - 1)` with jitter. Default base delay: 5 seconds. Max delay: 5 minutes.
- When `attempts >= max_attempts`, the job moves to `dead_letter` status.
- `max_attempts` defaults to 3 but is configurable per job type. Some jobs (e.g., `agent_turn`) may have higher retry counts.

### Dead Letter Queue

Jobs in `dead_letter` status are not retried automatically. They require operator attention:

- The operator dashboard shows dead-lettered jobs with their error history.
- An operator can manually retry (reset status to `pending`), inspect the payload and errors, or dismiss.
- For agent-related dead letters (failed turns, stuck flow nodes), the supervisor process (doc 16) detects the failure and escalates — filing a blocker task or notifying the PM.

### Stale Claim Recovery

A background process monitors for stale claims — jobs that were claimed or started but never completed within a timeout (configurable per job type, default 30 minutes for agent turns, 5 minutes for lightweight jobs).

If a claimed job exceeds its timeout:
1. The job's status is reset to `pending` (if attempts remain) or `dead_letter` (if exhausted).
2. The `claimed_by` and `claimed_at` are cleared.
3. A `system.job.failed` event is emitted with the timeout details.

This handles worker crashes, OOM kills, and network partitions without manual intervention.

### Job Priority

Priority is an integer — higher values are picked first. The mapping:

- `100` — sync-related jobs (human is waiting). Agent turns in sync sessions, steer/cancel operations.
- `50` — standard async work. Agent turns, flow node starts, merge execution.
- `25` — background processing. Memory extraction, notification dispatch, summary generation.
- `10` — maintenance. Cleanup jobs, retention purging.

Priority can be adjusted per job at creation time based on context (e.g., an agent turn for a task with `urgent` priority gets a higher job priority).

---

## 12. Rate Limiting

OtterCamp does not impose API-layer rate limits (requests per minute, per endpoint, etc.). The only rate-limiting concern is avoiding overwhelming upstream LLM providers, and that is handled entirely at the model layer (doc 07) via provider connection health status, failover priority, and concurrency management.

If API-layer rate limiting becomes necessary in the future (e.g., for managed multi-tenant hosting), it will be added as a non-breaking addition.

---

## 13. Webhooks (V2.1)

Webhooks are **not included in V2 GA**. They are planned for V2.1.

The event system is explicitly designed to support webhooks when they are added:

- Every event in the durable log has a stable type, consistent structure, and scope information.
- Webhook subscriptions will filter by event type and scope, same as realtime subscriptions.
- Delivery will use the same per-consumer cursor pattern with retry and dead-letter semantics.
- Webhook payloads will be the same event JSON structure clients already see via SSE/WebSocket.

When implemented, webhooks will add:

- `webhook_subscription` table (URL, event type filters, scope filters, secret for signature verification).
- `webhook_delivery` table (delivery attempts, status, response code).
- Signature verification using HMAC-SHA256 with a per-subscription secret.
- Retry with exponential backoff (same policy as job queue).
- Automatic disabling after sustained delivery failures.

No new event types or structures will be needed — webhooks consume the same event log as every other subscriber.

---

## 14. CLI Client

The `ottercamp` binary includes both server management commands (doc 08: `serve`, `migrate`, `bootstrap`, `backup`) and a full API client CLI. The API client provides complete access to every endpoint in Section 4 from the command line.

### Command Structure

Commands follow a `noun verb` pattern that maps directly to the API:

```
ottercamp <resource> <action> [flags]
```

The resource names match the API path segments. The action maps to the HTTP method or command endpoint:

```
ottercamp projects list                           # GET  /v1/projects
ottercamp projects create --name "Auth Rewrite"   # POST /v1/projects
ottercamp projects get <id>                       # GET  /v1/projects/:id
ottercamp projects update <id> --name "New Name"  # PATCH /v1/projects/:id
ottercamp projects archive <id>                   # POST /v1/projects/:id/archive

ottercamp tasks list --project <pid>              # GET  /v1/projects/:pid/tasks
ottercamp tasks create --project <pid> --title "Fix login"  # POST /v1/projects/:pid/tasks
ottercamp tasks advance-flow <id>                 # POST /v1/tasks/:id/advance-flow
ottercamp tasks hold <id>                         # POST /v1/tasks/:id/hold

ottercamp chat sessions list --scope project:<pid>  # GET  /v1/chat-sessions?scope=...
ottercamp chat send <session-id> "Can you look at this?"  # POST /v1/chat-sessions/:id/messages
ottercamp chat watch <session-id>                 # SSE  /v1/events/stream?scopes=session:<id>

ottercamp agents list                             # GET  /v1/agents
ottercamp agents create --template <tid>          # POST /v1/agents
ottercamp agents assign <aid> --project <pid>     # POST /v1/projects/:pid/agents

ottercamp memory query "what do we know about auth?"  # POST /v1/memory/query
ottercamp memory items list --scope project       # GET  /v1/memory/items?scope=...
ottercamp memory import upload ./memories.zip     # POST /v1/memory/import

ottercamp events stream --scope org               # SSE  /v1/events/stream?scopes=org:<id>
ottercamp events list --type task.completed        # GET  /v1/events?type=...

ottercamp control runs list --status running      # GET  /v1/control/runs?status=running
ottercamp control policies list                   # GET  /v1/control/policies
```

Sub-resources use dotted or nested syntax:

```
ottercamp tasks subtasks list <task-id>
ottercamp tasks participants add <task-id> --agent <aid>
ottercamp flow-templates nodes add <template-id> --type work --actor agent
ottercamp agents skills attach <agent-id> --skill <sid>
```

### Authentication

The CLI authenticates via API key, stored in one of:

1. `--api-key` flag (per-command, for scripts)
2. `OTTERCAMP_API_KEY` environment variable
3. `~/.ottercamp/credentials` file (written by `ottercamp auth login`)

The `ottercamp auth login` command opens the browser for session-based login, exchanges the session for an API key, and stores it locally. This is the recommended flow for interactive use.

```
ottercamp auth login                    # Browser login → stores API key
ottercamp auth login --url https://my-ottercamp.example.com
ottercamp auth status                   # Show current auth context (org, user, key scope)
ottercamp auth logout                   # Revoke stored key and remove credentials
```

### Output Formats

All commands support three output modes:

- `--output table` (default for interactive) — human-readable tables, truncated to terminal width.
- `--output json` — full JSON response (the API envelope `data` field). Pipe to `jq` for processing.
- `--output quiet` — IDs only, one per line. For scripting composition.

```
# Get task IDs for all in-progress tasks
ottercamp tasks list --project <pid> --status in_progress -o quiet | \
  xargs -I {} ottercamp tasks hold {}

# Full JSON for scripting
ottercamp agents get <id> -o json | jq '.model_profile_id'
```

### Streaming

Commands that map to SSE endpoints stream events to stdout in real time:

```
# Watch a chat session (streams message deltas)
ottercamp chat watch <session-id>

# Stream all events for a project
ottercamp events stream --scope project:<pid>

# Follow a control plane run
ottercamp control runs watch <run-id>
```

Streaming commands run until interrupted (`Ctrl+C`) or until the stream ends (e.g., run completes). With `--output json`, each event is printed as a newline-delimited JSON object for piping to other tools.

### Self-Documentation and Completion

The CLI is extensively self-documenting:

- **`ottercamp help`** — top-level help with all resource groups.
- **`ottercamp <resource> help`** — all actions for a resource, with descriptions.
- **`ottercamp <resource> <action> --help`** — full flag documentation, examples, and the corresponding API endpoint.
- **`ottercamp api-docs`** — opens the API documentation in the browser.
- **`ottercamp api-docs <resource>`** — opens the specific section.

Every help page shows the underlying API call:

```
$ ottercamp tasks advance-flow --help
Advance a task's flow to the next node.

Usage:
  ottercamp tasks advance-flow <task-id> [flags]

API: POST /v1/tasks/:id/advance-flow

Flags:
  --output, -o   Output format: table, json, quiet (default: table)
  --api-key      API key (overrides stored credentials)

Examples:
  ottercamp tasks advance-flow 550e8400-e29b-41d4-a716-446655440000
```

Shell completion is built in for bash, zsh, fish, and PowerShell:

```
ottercamp completion bash > /etc/bash_completion.d/ottercamp
ottercamp completion zsh > "${fpath[1]}/_ottercamp"
ottercamp completion fish > ~/.config/fish/completions/ottercamp.fish
```

Completion covers:
- All resource names and actions
- Flag names and their valid values (e.g., `--status` completes to `draft`, `queued`, `in_progress`, etc.)
- Resource IDs where feasible (e.g., project names, agent names) via live API lookup with caching

### Server URL

The CLI needs to know where the OtterCamp server is:

1. `--server` flag
2. `OTTERCAMP_URL` environment variable
3. Stored in `~/.ottercamp/credentials` (set during `ottercamp auth login`)
4. Default: `http://localhost:4110` (the default serve port from doc 08)

---

## Database Schema Summary

This spec introduces four new tables:

### domain_event

The durable event log. DDL in Section 7.

### consumer_cursor

Per-consumer position tracking for event dispatch. DDL in Section 7.

### job_queue

The PostgreSQL-backed job queue for async work. DDL in Section 11.

### idempotency_key

The idempotency key store. DDL in Section 6.

These are infrastructure tables that support the rest of the system. They do not duplicate domain data — they coordinate, dispatch, and ensure reliability.

---

## Resolved Decisions

1. **SSE as default realtime transport.** WebSocket available for bidirectional needs (chat input streaming, typing indicators). SSE is simpler, works through proxies and CDNs, and is sufficient for server-to-client push. No need for WebSocket for normal chat flow — sending a message is POST, receiving is SSE.

2. **No public webhook system in V2 GA.** Planned for V2.1. The event log is designed to support webhooks — stable types, consistent structure, scope-based filtering. When added, webhooks consume the same event stream as realtime and internal subscribers.

3. **API version compatibility: self-host and cloud run the same API version.** No compatibility split. One version active at a time. Breaking changes increment version; non-breaking additions do not.

4. **PostgreSQL-backed job queue.** No Redis dependency. `SELECT ... FOR UPDATE SKIP LOCKED` provides efficient concurrent claiming. Fewer moving parts, fewer failure modes. The system already depends on PostgreSQL; adding another data store for the job queue adds operational complexity without proportional benefit.

5. **No API-layer rate limiting.** The only rate-limiting concern is upstream LLM providers, handled at the model layer (doc 07). API-layer rate limits add complexity with no proportional benefit — self-host operators own their infrastructure, and the model layer already prevents provider overload. Can be added later if managed multi-tenant hosting requires it.

6. **Event dispatch uses per-consumer cursor tracking on a monotonic sequence.** Each consumer (realtime push, memory pipeline, notification evaluator, internal reactions) independently tracks its position via a `seq`-based cursor in the `consumer_cursor` table. The `seq` column (`BIGSERIAL`) provides strict total ordering that eliminates the timestamp-collision problem. Events are written transactionally with domain operations. LISTEN/NOTIFY provides near-instant wake-up; polling is the fallback. At-least-once delivery; consumers must be idempotent. This decouples consumers from each other — one slow consumer does not block others.

7. **Cursor-based pagination only.** No offset-based pagination. Cursors are opaque and client-generated construction is not supported. Reliable under concurrent writes.

8. **Version in URL path, not headers.** `/v1/...` is explicit and unambiguous. Works in browser address bars, curl commands, and documentation links.

9. **Command endpoints for non-CRUD operations.** Actions with side effects (advance flow, cancel turn, approve review) are explicit POST commands, not PATCH updates to status fields. Makes the operation's semantics and validation requirements clear.

10. **Event types use dot-separated namespaces.** `{domain}.{entity}.{action}` format. Stable across versions. New event types are non-breaking additions.

11. **Scope fields denormalized on domain_event.** `organization_id`, `project_id`, `task_id`, `session_id` are columns, not buried in the payload. Enables efficient subscription filtering without JSON parsing.

12. **Job priority is integer-based with semantic tiers.** Sync-related work (100) beats async work (50) beats background (25) beats maintenance (10). Maps directly to the doc 02 principle that sync sessions get priority over async.

13. **Dead letter queue with operator tooling.** Failed jobs that exhaust retries are not silently dropped. They are visible, inspectable, and manually retriable. For agent-related failures, the supervisor process (doc 16) detects and escalates.

14. **Stale claim recovery via background process.** Handles worker crashes without manual intervention. Configurable timeout per job type — longer for agent turns (which may involve long model calls), shorter for lightweight jobs.

15. **Idempotency keys in org database with 24-hour TTL.** Client-generated, server-stored. Same key with same request returns stored response. Same key with different request returns 409. Prevents accidental duplicate mutations. Org scoping is implicit from db-per-org — no `organization_id` column needed.

16. **SSE reconnection via Last-Event-ID using monotonic seq.** The SSE `id` field is the event's `seq` value — a monotonic integer that provides strict ordering. On reconnect, `Last-Event-ID` is a simple numeric comparison (`WHERE seq > $last_seq`). No UUID parsing, no timestamp ambiguity.

17. **Backpressure via buffer overflow and reconnect.** If a client falls behind, the server drops the connection after the buffer fills. The client reconnects and catches up from the event log. Simple, no flow control negotiation needed.

18. **API-first: no UI-only features.** Every piece of functionality is accessible through the REST API. The web UI, TUI, mobile app, and CLI are all API clients. There is no hidden internal API — agent operations have corresponding human-accessible endpoints for inspection and override.

19. **CLI wraps the API with `noun verb` commands.** The `ottercamp` binary includes a full API client alongside server management commands. Resource names map to API paths, actions map to HTTP methods/commands. Three output modes (table, json, quiet) support both interactive use and scripting. Shell completion and self-documentation are built in — every command shows its underlying API call.

## Open Questions

_None currently outstanding._
