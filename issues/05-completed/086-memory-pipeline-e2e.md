# 086: Memory Pipeline E2E

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1–2 days) |
| Spec refs | doc 06 §MemoryExtraction, doc 06 §MemoryRetrieval, doc 06 §MemoryCompaction, doc 06 §PassiveInjection, doc 21 §E2ETests |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

E2E test scenario for the memory pipeline. Uses only the `ottercamp` CLI binary and REST
API. Verifies: chat messages with extractable facts trigger memory extraction, `memory`
rows are created with correct scope, the memory retrieval API returns relevant memories,
subsequent message turns inject memory into the model context (verified via
`model_invocation.metadata` token counts), and memory compaction distills episodic
memories.

### Must build

**Test file:** `e2e/memory_pipeline_test.go`

Build tag: `//go:build e2e`

Test setup: calls `POST /test/reset` and `ottercamp bootstrap` before each scenario.
Uses standard `e2e/testutil/` helpers plus:
- `WaitForMemory(t, baseURL, token, filter, timeout)` — polls `GET /v1/memory/items` with
  filter parameters until at least one matching memory exists or timeout expires; returns
  the first matching memory
- `TriggerExtractionJob(t, baseURL, token, sessionID)` — calls
  `POST /v1/memory/consolidate` or a test-mode endpoint to trigger extraction for a
  specific session synchronously
- `TriggerCompactionRun(t, baseURL, token, orgID)` — calls
  `POST /v1/memory/consolidate` with `type: "compaction"` to trigger compaction
  synchronously in test mode

**Scenario: `TestMemory_ExtractionAndRetrieval`**

Step 1 — Reset, bootstrap, get token:
```
POST /test/reset → 204
ottercamp bootstrap → exit 0
admin token via POST /v1/auth/login
```

Step 2 — Create chat session and add Frank as participant:
```
POST /v1/chat-sessions
{ "scope_type": "organization", "mode": "sync" }
→ 201 → session_id

GET /v1/agents?name=Frank → frank_id
POST /v1/chat-sessions/<session_id>/participants
{ "participant_type": "agent", "participant_id": "<frank_id>" }
→ 201
```

Step 3 — Send messages containing extractable facts (test mode uses deterministic
extraction — these keywords cause the extraction pipeline to recognize them as facts):
```
POST /v1/chat-sessions/<session_id>/messages
{
  "content": "[memory-fact] The primary database server is named db-prod-01 and runs PostgreSQL 16.",
  "role": "human"
}
→ 201

Wait for agent response (SSE turn.completed within 30s)

POST /v1/chat-sessions/<session_id>/messages
{
  "content": "[memory-fact] The deployment pipeline runs every Monday at 09:00 UTC.",
  "role": "human"
}
→ 201

Wait for agent response (SSE turn.completed within 30s)
```

Step 4 — Trigger memory extraction job for this session:
```
TriggerExtractionJob(sessionID: session_id)
→ 200 or 204
```

Step 5 — Verify `memory` rows created with correct scope:
```
WaitForMemory(filter: { scope_type: "organization", contains_text: "db-prod-01" }, timeout: 30s)
→ memory found
→ memory.organization_id is non-empty
→ memory.scope == "organization" or memory.project_id is null
→ memory.content contains "db-prod-01"
→ memory.is_active == true
→ memory.confidence >= 0.0

WaitForMemory(filter: { contains_text: "deployment pipeline" }, timeout: 30s)
→ memory found
→ memory.content contains "Monday" or "09:00"
```

Step 6 — Query memory retrieval API:
```
POST /v1/memory/query
Authorization: Bearer <token>
{
  "query": "database server configuration",
  "scope": { "organization_id": "<org_id>" },
  "limit": 10
}
→ 200
→ body.data length >= 1
→ body.data[0].content contains "db-prod-01"  (most relevant memory returned first)
→ body.data[0].score > 0.0  (relevance score present)
```

Step 7 — Send another message and verify memory injection occurs:
```
POST /v1/chat-sessions/<session_id>/messages
{
  "content": "What do you know about our infrastructure?",
  "role": "human"
}
→ 201

Wait for agent response (SSE turn.completed within 30s)
```

Step 8 — Verify memory injection in model invocation metadata:
```
GET /v1/chat-sessions/<session_id>/turns
→ body.data[last].id (latest_turn_id)

GET /v1/control/runs?session_id=<session_id>
→ find run associated with the latest turn → run_id

GET /v1/control/runs/<run_id>
→ check metadata or related model_invocation
```

Note: the memory injection evidence is in `model_invocation.metadata.layer_token_counts`.
Access via:
```
GET /v1/model/invocations?run_id=<run_id>
→ 200
→ body.data[0].metadata.layer_token_counts.memory_injection > 0
   OR
   body.data[0].metadata.memory_layer_tokens > 0
```

**Scenario: `TestMemory_Compaction`**

Step 1 — Reset, bootstrap, create session, send several fact-containing messages, trigger
extraction (reuse steps 1–4 above or use a fixture set of 5+ memories).

Step 2 — Verify episodic memories exist before compaction:
```
GET /v1/memory/items?memory_type=episodic
→ body.data length >= 2
→ record episodic_ids (list of IDs to verify distillation)
```

Step 3 — Trigger compaction run:
```
TriggerCompactionRun(orgID: <org_id>)
→ 200 or 204
```

Step 4 — Verify episodic memories distilled (compaction produces semantic/procedural memories):
```
GET /v1/memory/items?memory_type=semantic
→ 200
→ body.data length >= 1  (at least one distilled memory created from episodic facts)

GET /v1/memory/compaction-runs
→ 200
→ body.data length >= 1
→ body.data[0].status == "completed"
→ body.data[0].memories_distilled >= 1
```

Step 5 — Verify original episodic memories are marked as archived or superseded after compaction:
```
GET /v1/memory/items/<episodic_id>
→ 200
→ body.data.superseded_by is non-null  (distilled into a semantic memory)
   OR body.data.is_active == false  (archived by compaction)
```

**Scenario: `TestMemory_ScopeFilter`**

Verifies that memory retrieval respects scope boundaries.

Step 1 — Create org-level memory (via extraction from org-scoped chat session).

Step 2 — Create project and project-level memory (via extraction from project-scoped session).

Step 3 — Query with org scope — should return org-level memory only:
```
POST /v1/memory/query
{ "query": "infrastructure", "scope": { "scope_type": "organization" } }
→ 200
→ results contain org-level memory
→ results do NOT contain memories from other orgs (verified by organization_id field)
```

Step 4 — Query with project scope — should return project-level and org-level memories:
```
POST /v1/memory/query
{ "query": "infrastructure", "scope": { "scope_type": "project", "project_id": "<project_id>" } }
→ 200
→ results include both org-level and project-level memories
```

### Must NOT build

- UI or TUI interactions
- Internal Go package calls
- Trust tier capping tests (tested in task 074)
- Dedup and contradiction detection tests (tested in task 074)
- JSONL import tests (tested in task 074)

## Acceptance Criteria

- [ ] `TestMemory_ExtractionAndRetrieval` passes: fact messages produce `memory` rows; retrieval API returns relevant memory
- [ ] `model_invocation.metadata` contains a non-zero token count for the memory injection layer after a subsequent message
- [ ] `TestMemory_Compaction` passes: compaction run completes; at least one semantic memory distilled; episodic memories superseded
- [ ] `TestMemory_ScopeFilter` passes: org-scoped query excludes project-scoped memories; project-scoped query includes both
- [ ] Full scenario completes in under 4 minutes

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:** None — this is an E2E test suite.

**E2E tests:**
- `TestMemory_ExtractionAndRetrieval` — messages → extraction → memory rows → retrieval API → injection in next turn
- `TestMemory_Compaction` — episodic memories → compaction run → distilled semantic memories
- `TestMemory_ScopeFilter` — org vs project scope boundaries in retrieval

## Implementer Notes

**ISSUE #27 (RESOLVED — path prefix):**
API routes use `/v1/*` except health (`/health*`) and test-mode reset (`/test/reset`).

**Deterministic model responses and memory extraction:**
In `OTTERCAMP_MODE=test`, the memory extraction pipeline uses a deterministic mode
where messages with the `[memory-fact]` prefix are guaranteed to produce memory rows.
This keyword is a test-mode contract between this E2E test and the extraction pipeline
implementation (task 039). The extraction pipeline must recognize this prefix in test
mode and bypass the LLM extraction stage, directly storing the message content as a
memory candidate.

**model_invocation.metadata access:**
The exact path to the memory injection token count in `model_invocation.metadata` is
implementation-defined. The test checks both `layer_token_counts.memory_injection` and
`memory_layer_tokens` as alternative field names. If neither is present, the test logs a
warning and skips the assertion with a comment: `TODO: verify metadata field name with
task 036 implementer`.

**ISSUE #9 (RESOLVED — memory_source.session_id soft reference):**
`memory_source.session_id` is a permanent soft reference (no SQL FK, by design). This
test does not directly verify `memory_source` rows — it verifies `memory` rows via the
API. The `memory_source` linkage is tested in task 074 integration tests.

**Compaction synchronous trigger:**
In `OTTERCAMP_MODE=test`, the `POST /v1/memory/consolidate` endpoint with
`type: "compaction"` must execute the compaction run synchronously (within the request,
not as a background job) so the test can poll for results immediately after. If the
endpoint dispatches a job, the test must poll the `GET /v1/memory/compaction-runs`
endpoint until `status == "completed"` with a 30-second timeout.
