---
## Summary

This spec defines OtterCamp's entire API surface, event system, and realtime communication contracts. The API is a versioned REST API (all routes under `/v1/`) that uses standard REST for CRUD operations and explicit command endpoints (verb-noun POST routes like `/tasks/:id/advance-flow`) for actions with side effects. All responses use a consistent JSON envelope with `data`/`error` and `meta` fields. List endpoints use cursor-based pagination exclusively. Authentication is via Bearer token (API keys) for programmatic access and session cookies for the web UI; all requests are scoped to a single organization with row-level enforcement. Section 4 contains a comprehensive endpoint catalog covering every domain: auth, chat sessions/messages, projects, tasks/flow, flow templates, inbox, merge queue, scheduling, agents, memory, models, MCP connections, skills, tools/policies, control plane, system integration, delivery/environments, and events.

The event system is the central nervous system of the platform. Every significant state change emits a domain event into a durable `domain_event` PostgreSQL table. Events follow a uniform structure with dot-namespaced types (`{domain}.{entity}.{action}`), an actor (human/agent/system), hierarchical scope fields (org, project, task, session) denormalized as columns for efficient filtering, and a type-specific JSON payload. The dispatch uses an outbox pattern: events are written transactionally with domain operations, then an async dispatcher polls for unpublished events and fans them out to four consumer categories -- realtime push (SSE/WebSocket), memory capture (Ellie), notification delivery, and internal reaction handlers. Consumers receive at-least-once delivery and must be idempotent. The spec defines roughly 70 event types across 11 domains (chat, task, flow, dependency, agent, memory, model, control plane, project, merge/delivery, system/inbox).

For realtime transport, SSE is the default (simpler, works through proxies, built-in reconnection via `Last-Event-ID`), with WebSocket available as an opt-in secondary channel for bidirectional needs like typing indicators. Clients subscribe to hierarchical scopes (`org:id`, `project:id`, `task:id`, `session:id`) and receive filtered events. Asynchronous work is processed through a PostgreSQL-backed job queue (no Redis) using `SELECT ... FOR UPDATE SKIP LOCKED` for concurrent worker claiming, with exponential backoff retries, dead letter handling, priority tiers (100 for sync/human-waiting, 50 for standard async, 25 for background, 10 for maintenance), and stale claim recovery. The spec also covers idempotency keys (org-scoped, 24-hour TTL, stored in an `idempotency_key` table), per-API-key rate limiting with configurable defaults for self-host vs managed, and notes that webhooks are deferred to V2.1 but the event log is designed to support them. Three database tables are introduced: `domain_event`, `job_queue`, and `idempotency_key`.

---

# 12. API, Events, and Realtime Contracts

> Status: Draft
> Depends on: 01-architecture-and-domain.md (architecture, eventing model), 02-chat.md (realtime event payloads, streaming), 03-projects-and-task-flow.md (task lifecycle, merge queue, inbox), 03a-shipping-and-delivery.md (deploy events, push hooks), 04-auth-tenancy-and-identity.md (auth model, tenancy), 16-agent-control-plane.md (control plane API surface)

## Purpose

Define the API surface, event system, and realtime contracts that bind together every component of OtterCamp. The API is the backbone — all clients (TUI, web, mobile) connect through it. The event system is the nervous system — domain events drive realtime updates, memory capture, notification delivery, inbox creation, and (eventually) webhook dispatch.

---

## 1. API Design Principles

### REST for CRUD, Commands for Actions

The API uses REST semantics for resource CRUD (create, read, update, delete) and explicit command endpoints for actions that are not pure CRUD. The distinction matters because many operations have side effects beyond writing a row.

- **CRUD endpoints** follow standard REST patterns: `POST /v1/projects` creates a project, `GET /v1/projects/:id` reads it, `PATCH /v1/projects/:id` updates it.
- **Command endpoints** model actions with side effects: `POST /v1/tasks/:id/advance-flow` advances a task's flow (which triggers scheduling, events, and possibly async work). `POST /v1/sessions/:id/cancel-turn` cancels an in-progress turn. Commands use verb-noun naming in the URL path.

A PATCH to a task's `work_status` is wrong — status transitions have validation rules, trigger events, and may cascade to dependencies. These are commands, not field updates.

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
Authorization: Bearer ottercamp_sk_live_abc123...
```

API keys are the primary auth mechanism for programmatic access — the TUI, CI integrations, and any external tooling.

### Session Cookie

The web UI uses session cookies for authentication. Cookie-based auth follows standard secure practices: `HttpOnly`, `Secure`, `SameSite=Strict`, short-lived with refresh.

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
```

### Flow Templates (doc 03)

```
POST   /v1/projects/:pid/flow-templates  Create flow template
GET    /v1/projects/:pid/flow-templates  List flow templates
GET    /v1/flow-templates/:id            Template detail (includes nodes and edges)
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
POST   /v1/agents                        Create agent
GET    /v1/agents                        List agents (filterable by class, status)
GET    /v1/agents/:id                    Agent detail (profile, capabilities, assignments)
PATCH  /v1/agents/:id                    Update agent profile
POST   /v1/agents/:id/activate           Activate agent (command)
POST   /v1/agents/:id/pause              Pause agent (command)
POST   /v1/agents/:id/retire             Retire agent (command)
```

### Memory (doc 06)

```
POST   /v1/memory/query                  Query memory (Ellie's retrieval pipeline)
GET    /v1/memory/items                  List memory items (filterable by scope, type, entity)
GET    /v1/memory/items/:id              Memory item detail
POST   /v1/memory/items/:id/feedback     Submit feedback on a memory item
GET    /v1/memory/entities               List known entities
GET    /v1/memory/entities/:id           Entity detail with related memories
GET    /v1/memory/taxonomy               Browse taxonomy tree
```

### Models and Inference (doc 07)

```
GET    /v1/model-profiles                List model profiles
POST   /v1/model-profiles                Create model profile
GET    /v1/model-profiles/:id            Model profile detail
PATCH  /v1/model-profiles/:id            Update model profile
GET    /v1/model-providers               List configured providers
```

### MCP Connections (doc 09)

```
POST   /v1/mcp-connections               Create MCP connection
GET    /v1/mcp-connections               List connections
GET    /v1/mcp-connections/:id           Connection detail (includes available tools)
PATCH  /v1/mcp-connections/:id           Update connection config
DELETE /v1/mcp-connections/:id           Remove connection
POST   /v1/mcp-connections/:id/test      Test connection (command)
```

### Skills (doc 10)

```
POST   /v1/skills                        Create skill
GET    /v1/skills                        List skills
GET    /v1/skills/:id                    Skill detail
PATCH  /v1/skills/:id                    Update skill
DELETE /v1/skills/:id                    Delete skill
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
GET    /v1/control/runs/:id              Run detail
GET    /v1/control/runs/:id/events       Run event stream
POST   /v1/control/runs/:id/cancel       Cancel run (command)
POST   /v1/control/approvals/:id/approve Approve blocked action (command)
POST   /v1/control/approvals/:id/reject  Reject blocked action (command)
GET    /v1/control/policies/evaluate     Dry-run policy simulation
```

### System Integration (doc 11)

```
POST   /v1/system/cli/execute            Execute CLI command (sandboxed)
POST   /v1/system/browser/navigate       Browser navigation action
GET    /v1/system/browser/screenshot     Capture browser screenshot
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

```
GET    /v1/events                        Query event log (filterable by type, scope, time range)
GET    /v1/events/:id                    Event detail

GET    /v1/health                        Health check (unauthenticated)
GET    /v1/version                       API version info (unauthenticated)
```

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

rate_limit_exceeded         429  Rate limit reached for this API key
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
- Idempotency keys are scoped to the authenticated org — different orgs can use the same key string without conflict.

### Replay Safety

The idempotency mechanism makes all mutations replay-safe. Network timeouts, retries, and duplicate submissions are safe — the operation executes at most once per key.

Command endpoints (advance-flow, cancel-turn, etc.) are inherently non-idempotent in their semantics, but the key ensures the same command is not accidentally applied twice. For example, if `advance-flow` is called twice with the same idempotency key, the second call returns the result of the first.

### DDL: Idempotency Store

```sql
create table idempotency_key (
  key               text not null,
  organization_id   uuid not null,
  method            text not null,
  path              text not null,
  request_hash      text not null,       -- SHA-256 of canonical request body
  response_status   int not null,
  response_body     jsonb not null,
  created_at        timestamptz not null default now(),
  expires_at        timestamptz not null,

  primary key (organization_id, key)
);

create index on idempotency_key (expires_at);  -- cleanup query
```

A periodic job purges expired rows.

---

## 7. Event System

### The Durable Event Log

The event system is the backbone of OtterCamp's asynchronous communication. Domain events are emitted on every significant state change and written to a durable event log table. The log serves four primary consumers:

1. **Realtime fanout** — events are pushed to connected clients via SSE/WebSocket.
2. **Memory capture** — Ellie subscribes to the event bus and extracts memories from relevant events (doc 06).
3. **Notification delivery** — the notification layer subscribes to events, applies human preferences, and delivers notifications (doc 02).
4. **Internal reactions** — inbox item creation, merge queue hooks, deploy task creation, dependency resolution, and any other system behavior triggered by state changes.

The event log is the single source of truth for "what happened." Other systems subscribe to it rather than directly coupling to each other.

### Event Structure

Every event has the same top-level structure:

```json
{
  "id": "evt_01H2X3Y4Z5...",
  "type": "task.flow.advanced",
  "timestamp": "2026-02-22T14:30:00.123Z",
  "actor": {
    "type": "agent",
    "id": "uuid-of-agent"
  },
  "scope": {
    "organization_id": "uuid-of-org",
    "project_id": "uuid-of-project",
    "task_id": "uuid-of-task",
    "session_id": null
  },
  "payload": {
    "task_id": "uuid-of-task",
    "from_node_id": "uuid-of-node-a",
    "to_node_id": "uuid-of-node-b",
    "visit": 1
  },
  "metadata": {
    "idempotency_key": "idem_abc123",
    "request_id": "req_xyz789"
  }
}
```

**Field definitions:**

- `id` — globally unique event ID. Prefixed with `evt_` for human readability.
- `type` — dot-namespaced event type (see Section 8).
- `timestamp` — UTC timestamp with millisecond precision. When the event was created, not when the underlying action started.
- `actor` — who caused the event. Type is `human`, `agent`, or `system`. ID is the actor's entity ID. System events (e.g., scheduled task creation) have actor type `system` and null ID.
- `scope` — hierarchical scope for event routing and filtering. All fields nullable except `organization_id`. A chat event has `session_id` set; a task event has `project_id` and `task_id` set; an org-level event has only `organization_id`.
- `payload` — event-type-specific data. Structure varies by type. Always a JSON object.
- `metadata` — operational metadata. Idempotency key, request ID, trace ID. Not part of the business event.

### DDL: domain_event

```sql
create table domain_event (
  id                uuid primary key default gen_random_uuid(),
  type              text not null,
  timestamp         timestamptz not null default now(),
  actor_type        text not null check (actor_type in ('human', 'agent', 'system', 'supervisor')),  -- supervisor: automated control-plane actions (e.g., run cancellation, stuck task recovery)
  actor_id          uuid,
  organization_id   uuid not null,
  project_id        uuid,
  task_id           uuid,
  session_id        uuid,
  payload           jsonb not null,
  metadata          jsonb not null default '{}',
  published         boolean not null default false,  -- has this event been dispatched to consumers?
  created_at        timestamptz not null default now()
);

-- Primary query: consume unpublished events in order
create index on domain_event (published, created_at) where published = false;

-- Scope-based queries for client subscription filtering
create index on domain_event (organization_id, created_at);
create index on domain_event (project_id, created_at) where project_id is not null;
create index on domain_event (task_id, created_at) where task_id is not null;
create index on domain_event (session_id, created_at) where session_id is not null;

-- Type-based queries for consumers that care about specific event types
create index on domain_event (type, created_at);

-- Retention management
create index on domain_event (created_at);
```

**Design notes:**

- `published` is a dispatch flag. The event dispatcher reads unpublished events, fans them out to consumers (realtime, memory, notifications, internal handlers), and marks them published. This is an outbox pattern — events are written transactionally with the domain operation and dispatched asynchronously.
- The `scope` fields are denormalized from the event payload for efficient subscription filtering. The dispatcher does not need to parse the payload to route events.
- Events are append-only. Never updated or deleted during normal operation. A retention policy purges old events (configurable per org, default 90 days for the event log; the effects of events — memories, notifications, audit records — persist independently).
- The `id` column uses UUIDs for global uniqueness. The `evt_` prefix mentioned in the JSON representation is a display convention, not stored in the database.

### Event Dispatch (Outbox Pattern)

Events are dispatched using a poll-based outbox pattern:

1. A domain operation writes its primary data and the event row in the same database transaction.
2. A dispatcher process polls `domain_event` for unpublished events ordered by `created_at`.
3. For each event, the dispatcher fans it out to all registered consumers: realtime push, memory pipeline, notification evaluator, internal reaction handlers.
4. After all consumers have acknowledged (or the event has been queued for each), the dispatcher marks the event as `published`.
5. If the dispatcher crashes, it resumes from the last unpublished event. Events are delivered at-least-once to consumers; consumers must be idempotent.

The poll interval is short (100-500ms) to keep latency low for realtime consumers. Under load, the dispatcher processes events in batches.

---

## 8. Event Types

Events are organized by domain. Each event type is a dot-separated string: `{domain}.{entity}.{action}`.

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
chat.turn.completed            An agent turn finished (any terminal status)

chat.listening_eval.completed  Listening eval finished, interjection decisions made

chat.participant.joined        A participant was added to a session
chat.participant.left          A participant left a session

chat.reaction.created          A reaction was added to a message
chat.reaction.updated          A reaction sentiment was changed
chat.reaction.deleted          A reaction was removed
```

### Task Events

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
agent.profile_updated          Agent profile was updated (prompt, policies)
agent.assigned                 Agent was assigned to a project or task role
```

### Memory Events

```
memory.item.created            A new memory item was extracted
memory.item.updated            A memory item was updated (confidence, content)
memory.item.archived           A memory item was archived
memory.item.superseded         A memory item was superseded by a newer one
memory.entity.synthesized      An entity synthesis was completed
memory.dedup.completed         A deduplication pass completed
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

control.policy.evaluated       A policy evaluation occurred (allow/deny)
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
```

### System Events

```
system.health.degraded         System health degraded (provider down, queue backlog)
system.health.recovered        System health recovered

system.schedule.fired          A task schedule tick created a new task
system.schedule.skipped        A schedule tick was skipped (overlap policy)

system.job.started             A background job started
system.job.completed           A background job completed
system.job.failed              A background job failed
system.job.dead_lettered       A job exhausted retries and was dead-lettered
```

### MCP

| Event | Payload highlights | Emitted by |
|---|---|---|
| `mcp.connection.created` | connection_id, name, transport | MCP layer |
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
- The server filters events from the durable event log and sends relevant ones to the client.
- Each SSE message includes:

```
id: evt_01H2X3Y4Z5
event: chat.message.delta
data: {"message_id":"uuid","delta":{"text":"Here's what"}}

id: evt_01H2X3Y4Z6
event: task.status_changed
data: {"task_id":"uuid","from_status":"in_progress","to_status":"review"}
```

- The `id` field enables reconnection — on reconnect, the client sends `Last-Event-ID` and the server replays missed events.
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

---

## 11. Internal Job/Worker API

### Architecture

The API service and Worker service are separate processes from the same codebase (doc 01). They communicate via a PostgreSQL-backed job queue — no Redis, no external message broker. Fewer moving parts, fewer things to break.

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
notification_dispatch   Evaluate and deliver notifications for an event
merge_execute           Execute a merge queue entry
push_execute            Push main to a remote
deploy_task_create      Create a deploy task from a merge event
schedule_tick           Process a schedule tick (create task instance)
summary_generate        Generate a chat summary for a message range
cleanup_ephemeral       Purge ephemeral messages (daily)
idempotency_cleanup     Purge expired idempotency keys
event_log_retention     Purge old events past retention period
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

### Per-API-Key Limits

Rate limiting is enforced per API key. Each key has configurable limits:

- **Requests per minute** — overall request rate.
- **Requests per minute per endpoint family** — prevent one endpoint from monopolizing the budget.
- **Concurrent SSE connections** — limit simultaneous realtime subscriptions per key.

### Defaults

| Deployment | Requests/min | Per-endpoint/min | SSE connections |
|------------|-------------|-------------------|-----------------|
| Self-host  | 10,000      | 2,000             | 50              |
| Managed    | 1,000       | 200               | 10              |

Self-host defaults are generous — the operator owns the infrastructure. Managed defaults are stricter to protect shared infrastructure.

### Response

When rate limited, the server returns `429 Too Many Requests` with:

```
Retry-After: 5
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1708617000
```

### Implementation

Rate limiting uses a sliding window counter in the API service's memory. No external dependency. State is per-process — in a multi-process deployment, the effective limit is the configured limit per process. This is intentionally simple; if precise cross-process limiting becomes necessary, it can move to a shared store later.

---

## 13. Webhooks (V2.1)

Webhooks are **not included in V2 GA**. They are planned for V2.1.

The event system is explicitly designed to support webhooks when they are added:

- Every event in the durable log has a stable type, consistent structure, and scope information.
- Webhook subscriptions will filter by event type and scope, same as realtime subscriptions.
- Delivery will use the standard outbox pattern with retry and dead-letter semantics.
- Webhook payloads will be the same event JSON structure clients already see via SSE/WebSocket.

When implemented, webhooks will add:

- `webhook_subscription` table (URL, event type filters, scope filters, secret for signature verification).
- `webhook_delivery` table (delivery attempts, status, response code).
- Signature verification using HMAC-SHA256 with a per-subscription secret.
- Retry with exponential backoff (same policy as job queue).
- Automatic disabling after sustained delivery failures.

No new event types or structures will be needed — webhooks consume the same event log as every other subscriber.

---

## Database Schema Summary

This spec introduces two new tables:

### domain_event

The durable event log. DDL in Section 7.

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

5. **Rate limiting: per-API-key with configurable limits.** Default generous for self-host (operator owns the infra), stricter for managed. Sliding window counter in process memory — no external dependency. Cross-process precision deferred until needed.

6. **Event dispatch uses the outbox pattern.** Events are written transactionally with domain operations and dispatched asynchronously. At-least-once delivery to consumers; consumers must be idempotent. This decouples event producers from consumers and ensures no events are lost on crash.

7. **Cursor-based pagination only.** No offset-based pagination. Cursors are opaque and client-generated construction is not supported. Reliable under concurrent writes.

8. **Version in URL path, not headers.** `/v1/...` is explicit and unambiguous. Works in browser address bars, curl commands, and documentation links.

9. **Command endpoints for non-CRUD operations.** Actions with side effects (advance flow, cancel turn, approve review) are explicit POST commands, not PATCH updates to status fields. Makes the operation's semantics and validation requirements clear.

10. **Event types use dot-separated namespaces.** `{domain}.{entity}.{action}` format. Stable across versions. New event types are non-breaking additions.

11. **Scope fields denormalized on domain_event.** `organization_id`, `project_id`, `task_id`, `session_id` are columns, not buried in the payload. Enables efficient subscription filtering without JSON parsing.

12. **Job priority is integer-based with semantic tiers.** Sync-related work (100) beats async work (50) beats background (25) beats maintenance (10). Maps directly to the doc 02 principle that sync sessions get priority over async.

13. **Dead letter queue with operator tooling.** Failed jobs that exhaust retries are not silently dropped. They are visible, inspectable, and manually retriable. For agent-related failures, the supervisor process (doc 16) detects and escalates.

14. **Stale claim recovery via background process.** Handles worker crashes without manual intervention. Configurable timeout per job type — longer for agent turns (which may involve long model calls), shorter for lightweight jobs.

15. **Idempotency keys scoped to org with 24-hour TTL.** Client-generated, server-stored. Same key with same request returns stored response. Same key with different request returns 409. Prevents accidental duplicate mutations.

16. **SSE reconnection via Last-Event-ID.** Built into the SSE protocol. Client reconnects and replays missed events from the durable log. No client-side tracking needed beyond the standard `EventSource` API.

17. **Backpressure via buffer overflow and reconnect.** If a client falls behind, the server drops the connection after the buffer fills. The client reconnects and catches up from the event log. Simple, no flow control negotiation needed.

## Open Questions

_None currently outstanding._
