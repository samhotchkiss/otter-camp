# 054: Control Plane API Endpoints

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 16 §ControlPlaneAPI, doc 12 §APIConventions, doc 16 §RunHistory, doc 16 §LiveStreaming |
| Spec status | finished |
| Depends on | 053, 052, 007, 047, 067 |
| Blocks | 077, 087 |

## Scope

Build the control plane REST API endpoints: run CRUD, step and event listing, artifact
retrieval, run cancellation and retry, cost summary, and health. All endpoints use the
standard `{data, meta}` envelope from task 067 and live streaming via the SSE layer from
task 047.

### Must build

**Routes** (all under `/v1/control/`):

**Run endpoints:**

`POST /v1/control/runs`
- Body: `{trigger_type, task_id?, flow_node_id?, session_id?, turn_id?, idempotency_key?, metadata?}`
- Principal comes from the authenticated session/API key (middleware from task 007).
- Calls `RunService.CreateRun`.
- Returns `201 Created` with `{data: Run}`.
- If idempotency key matches existing non-failed run: returns `200 OK` with existing run and header `Idempotency-Key-Hit: true`.
- Policy denied: returns `403 Forbidden` with `{error: {code: "run_policy_denied", message: "..."}}`; the run row is still created with `status='failed'`.
- Budget exceeded: returns `402 Payment Required` with `{error: {code: "budget_exceeded"}}`.

`GET /v1/control/runs`
- Query params: `status`, `task_id`, `flow_node_id`, `principal_id`, `trigger_type`, `limit` (default 50, max 200), `cursor`
- All filters are optional and AND-combined.
- Returns `{data: [Run], meta: {cursor, total_count?}}`.
- Scoped to the requesting principal's organization.

`GET /v1/control/runs/:id`
- Returns `{data: Run}` including `step_count`, `latest_status`, `duration_ms`.
- `404` if not found or in different org.

`GET /v1/control/runs/:id/steps`
- Returns `{data: [RunStep]}` sorted by `step_number` ascending.
- Each step includes `attempt_count` (count of associated `run_attempt` rows).

`GET /v1/control/runs/:id/steps/:step_id/attempts`
- Returns `{data: [RunAttempt]}` sorted by `attempt_number` ascending.

`GET /v1/control/runs/:id/events`
- Query params: `from_sequence` (default 0), `limit` (default 100, max 500), `event_type` (comma-separated filter)
- Returns `{data: [RunEvent], meta: {next_sequence}}`.
- Used for run history polling and log replay.

`GET /v1/control/runs/:id/events/stream` (SSE)
- Streams live `run_event` rows as they are appended, using LISTEN/NOTIFY from task 047.
- Event format: `event: run_event\ndata: {RunEvent JSON}\n\n`
- Supports `Last-Event-ID: <sequence>` for reconnect (replays missed events from that sequence).
- Terminates the stream when run reaches terminal state (`completed|failed|cancelled|dead_letter|timed_out`) and all pending events are flushed.
- Returns `404` if run not found or already in terminal state and all events already streamed.

`GET /v1/control/runs/:id/artifacts`
- Query params: `artifact_type` (filter), `run_step_id` (filter), `limit` (default 20)
- Returns `{data: [RunArtifact]}`.
- `inline_content` is included in the response if populated on the row.
- For large artifacts (`inline_content=null`): response includes `download_url` (presigned object storage URL, 1-hour TTL).

`GET /v1/control/runs/:id/artifacts/:artifact_id/download`
- Redirects (`302 Found`) to a presigned object storage URL (1-hour TTL).
- Returns `404` if artifact not in this run.

`POST /v1/control/runs/:id/cancel`
- Body: `{}` (no body required)
- Calls `RunService.RequestCancel(ctx, runID, principal)`.
- Returns `202 Accepted` with `{data: Run}` (status will be `cancelling` or `cancelled`).
- `409 Conflict` if run is already in a terminal state.

`POST /v1/control/runs/:id/retry`
- Body: `{run_step_id?}` — if omitted, retries the latest failed step
- Calls `RunService.CreateRetryAttempt`.
- Returns `201 Created` with `{data: RunAttempt}`.
- `409 Conflict` if max attempts exceeded (returns `ErrMaxAttemptsExceeded` message).
- Only allowed if run is in `failed` state.

**Tool execution endpoints:**

`GET /v1/control/tool-executions`
- Query params: `run_id`, `tool_name`, `tool_domain`, `policy_decision`, `status`, `limit`, `cursor`
- Returns `{data: [ToolExecution], meta: {cursor}}`.
- Scoped to org.
- `policy_decision=denied` filter is important for audit dashboards.

`GET /v1/control/tool-executions/:id`
- Returns `{data: ToolExecution}` with full input/output.

**Cost and health endpoints:**

`GET /v1/control/cost/summary`
- Query params: `period` (e.g. `7d`, `30d`; default `30d`), `project_id` (optional), `group_by` (`project|agent|tool_domain`)
- Aggregates `run_attempt.input_tokens + output_tokens` for the period.
- Returns `{data: {total_tokens, by_group: [{group_key, tokens}], period_start, period_end}}`.
- This is a read-only aggregation; no new tables.

`GET /v1/control/health`
- Returns `{data: {status: "ok", active_runs: N, supervisor_last_tick: <ts>}}`.
- Does not check external dependencies (that is `/health/ready` from task 063).

### Must NOT build

- Run service logic (task 053)
- Schema DDL (task 052)
- Tool execution dispatch (task 055)
- SSE infrastructure (task 047) — this task is a consumer only
- Policy API endpoints (task 034)
- General auth middleware (task 007)
- Pagination/envelope middleware (task 067)

## Acceptance Criteria

- [ ] `POST /v1/control/runs` with valid payload returns `201` with a run in `status='created'`
- [ ] `POST /v1/control/runs` with duplicate `idempotency_key` of non-failed run returns `200` with `Idempotency-Key-Hit: true` header and same run ID
- [ ] `GET /v1/control/runs` respects `status` and `task_id` query filters; scoped to requesting org only
- [ ] `GET /v1/control/runs/:id/events/stream` SSE endpoint streams new `run_event` rows in real time; reconnects correctly with `Last-Event-ID`
- [ ] `POST /v1/control/runs/:id/cancel` on an already-completed run returns `409 Conflict`
- [ ] `POST /v1/control/runs/:id/retry` on a non-failed run returns `409 Conflict`
- [ ] `GET /v1/control/runs/:id/artifacts/:artifact_id/download` redirects to a presigned URL for large artifacts
- [ ] `GET /v1/control/tool-executions?policy_decision=denied` returns only denied tool executions for the org

## Tests Required

**Unit tests:**
- `POST /v1/control/runs`: mock service returns existing run for duplicate idempotency key → `200 OK` with correct header
- `POST /v1/control/runs`: mock service returns policy denied error → `403` with correct error code and body
- `GET /v1/control/runs` scope guard: request with org_A credentials → response includes only org_A runs (mock repo returns org_A result only)
- `POST /v1/control/runs/:id/cancel`: mock service returns `ErrTerminalState` → `409`
- `GET /v1/control/runs/:id/artifacts/:artifact_id/download`: mock artifact with `inline_content=null`, `storage_key="k"` → redirect to presigned URL generated by storage abstraction (task 004)

**Integration tests:**
- Full API round-trip: create run via API → start it (via service) → list via `GET /v1/control/runs?status=in_progress` → cancel → verify status=`cancelling`
- SSE streaming: create run, start appending `run_event` rows; SSE client receives events in order; reconnect with `Last-Event-ID=2` → only events with sequence>2 replayed
- Cost summary: seed 3 run_attempts with known token counts; `GET /v1/control/cost/summary?period=30d` → sum matches

**E2E tests:**
- None — covered by dedicated E2E task 087
