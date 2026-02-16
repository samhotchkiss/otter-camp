# Issue #152 — Ellie: Proactive Context Injection

> **Priority:** P1
> **Status:** Ready
> **Depends on:** #150 (Conversation Schema Redesign), #151 (Ellie Memory Infrastructure)
> **Author:** Josh S / Sam

## The Problem

In the reactive model (#151's "Ask Ellie First" rule), agents must know they don't know something and explicitly ask Ellie. But agents don't know what they don't know. If Sam tells Frank "let's add a database," Frank might not realize there are 6 stored decisions about database preferences, ORM choices, and migration patterns. He'd just wing it — or worse, ask Sam questions that have already been answered.

## The Vision

Ellie watches every room in real-time. When a message triggers relevant memories, she whispers context to agent participants proactively — before they ask, before they make a mistake.

This complements the Ask Ellie First rule (#151): proactive injection handles "you don't know what you don't know," while Ask Ellie First handles "you know you don't know."

## How It Works

### Message Flow

```
Message lands in room (chat_messages table, #150)
  → Near-real-time embedding generated (same async worker as #150, but fast-tracked)
  → Ellie's observer searches memories (Tier 2 retrieval from #151)
  → Relevance score > threshold AND not already injected in this room?
    → Yes: inject context as a whisper to agent participants
    → No: skip
```

### Room Observer

Ellie subscribes to the message stream for rooms with agent participants. For each incoming message:

1. **Embeds the message** — local model (#150's embedding provider), near-real-time
2. **Searches memories** — pgvector semantic similarity against `memories` table (#150 schema), using #151's retrieval logic
3. **Checks injection ledger** — has this memory already been surfaced in this room since last compaction?
4. **Checks relevance threshold** — multi-factor score (see below)
5. **Injects as whisper** — `context_injection` message type in `chat_messages`

### Injection Ledger

Tracks what context has been surfaced in each room. Append-only.

```sql
CREATE TABLE context_injections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    memory_id UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    injected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(room_id, memory_id)
);

CREATE INDEX idx_context_injections_room
    ON context_injections (room_id, injected_at DESC);
```

### Compaction Reset

When a session is compacted, the agent loses whispered context from working memory. The `last_compacted_at` watermark on `rooms` (#150 schema) handles this:

```sql
-- Injection check includes compaction watermark
SELECT 1 FROM context_injections
WHERE room_id = $1
  AND memory_id = $2
  AND injected_at > COALESCE(
    (SELECT last_compacted_at FROM rooms WHERE id = $1),
    '1970-01-01'
  )
```

- Injection before compaction → stale, Ellie can re-inject on next relevant trigger
- Injection after compaction → still valid, skip
- No deletes, no cleanup — compaction moves the watermark, ledger stays append-only

### Supersession Handling

When a memory is deprecated and replaced (e.g., "prefers Postgres" → "prefers MySQL"), Ellie checks if the old memory was injected in this room. If so, she injects the new memory with an explicit note:

```
📎 Updated context:
Previous decision (Postgres) has been superseded.
Current preference: MySQL
Source: [link to source conversation]
```

### Message Types

Uses the `type` field on `chat_messages` (#150 schema):

| Type | Visibility | Purpose |
|------|-----------|---------|
| `message` | All participants | Normal conversation |
| `whisper` | Targeted participants | Agent-to-agent private |
| `system` | All participants | Room events |
| `context_injection` | Agents + optionally Sam | Ellie's proactive context |

Sam can toggle visibility of `context_injection` messages per room via room settings.

## Relevance Scoring

Five factors determine whether a memory match triggers an injection:

| Factor | Weight | Logic |
|--------|--------|-------|
| **Semantic similarity** | High | Cosine distance between message embedding and memory embedding |
| **Recency** | Medium | Recent memories weighted higher (unless old one is still `active`) |
| **Importance** | Medium | Memory importance (1-5) raises/lowers the injection bar |
| **Topic novelty** | High | First mention of "database" in this room = high signal; fifth = low |
| **Conversation stage** | Low | Early (planning) = more injection; deep in execution = less noise |

Threshold is tunable per org: `ellie.injection_threshold` in config. Starts conservative.

## Latency

Proactive injection is only useful if it arrives before the conversation moves on.

| Step | Target |
|------|--------|
| Embedding generation | < 500ms (local model) |
| Memory search (pgvector) | < 200ms |
| Ledger check | < 50ms |
| **Total pipeline** | **< 2 seconds typical, < 5 seconds max** |

Achievable with local embeddings and well-indexed Postgres.

## What Ellie Whispers

```
📎 Context (from [conversation date]):
[Memory title]: [Memory content]
Source: [link to source conversation]
Confidence: [score] | Last validated: [date]
```

Multiple relevant memories are bundled into a single whisper (max 3-5 items per injection).

## Noise Control

| Mitigation | How |
|-----------|-----|
| **Injection ledger** | Never repeat the same memory in the same room (post-compaction) |
| **Relevance threshold** | Tunable, starts conservative |
| **Cooldown** | After injecting, wait N messages before injecting again in the same room |
| **Agent feedback** | Agents react to injections (useful/not useful) — tunes future relevance |
| **Sam override** | Per-room: tell Ellie to be more/less proactive |

## Open Questions

1. **Batch vs. streaming observer?** Polling (check every N seconds) vs. Postgres LISTEN/NOTIFY. Polling is simpler to start; NOTIFY is faster. Recommend polling first, upgrade if latency matters.
2. **Observe all rooms or agent-only?** Recommend agent-only — no value in watching human-to-human rooms with no agents.
3. **Feedback mechanism format?** Emoji reactions on whisper messages? Explicit "useful/not useful" buttons? Implicit (did the agent reference the injected context)?

## References

- #150 — Conversation Schema Redesign (`rooms`, `chat_messages`, `memories`, `last_compacted_at`)
- #151 — Ellie Memory Infrastructure (retrieval cascade, Ask Ellie First, memory taxonomy)
- #125 — Three + Temps Architecture (vision doc)

## Execution Log

- [2026-02-12 15:28 MST] Issue #845 | Commit fc51cca | context sync | Merged `origin/main` into `codex/spec-152-ellie-proactive-context-injection` to pull in merged spec-150 remediation baseline before spec-152 implementation | Tests: go test ./cmd/server ./internal/memory -count=1; go test ./internal/store -run 'TestConversationEmbeddingQueueListPendingBalancesAcrossOrgs|TestConversationSegmentationQueueListPendingBalancesAcrossOrgs|TestMigration069ConversationSensitivityFilesExistAndContainConstraint|TestSchemaConversationsSensitivityColumnAndConstraint' -count=1
- [2026-02-12 15:28 MST] Issue #852 | Commit n/a | created | Created full spec-152 micro-issue set #852-#856 with explicit test plans before implementation | Tests: n/a
- [2026-02-12 15:30 MST] Issue #852 | Commit 0a0a2b1 | closed | Added migration 070 `context_injections` ledger with RLS/index and schema validation tests | Tests: go test ./internal/store -run TestMigration070ContextInjectionsFilesExistAndContainDDL -count=1; go test ./internal/store -run TestSchemaContextInjectionsTableAndConstraint -count=1; go test ./internal/store -count=1
- [2026-02-12 15:33 MST] Issue #853 | Commit 37cb98c | closed | Added `EllieContextInjectionStore` primitives (pending queue, semantic memory lookup, compaction-aware dedupe/ledger, injection message writer) plus targeted store tests | Tests: go test ./internal/store -run TestEllieContextInjectionStoreListPendingMessages -count=1; go test ./internal/store -run TestEllieContextInjectionStoreSearchMemoryCandidatesByEmbedding -count=1; go test ./internal/store -run TestEllieContextInjectionStoreCompactionAwareDedupe -count=1; go test ./internal/store -run TestEllieContextInjectionStoreCreateInjectionMessage -count=1; go test ./internal/store -count=1
- [2026-02-12 15:35 MST] Issue #854 | Commit f7f06b9 | closed | Added `EllieProactiveInjectionService` with weighted scoring, threshold/top-N bundling, and supersession note formatting plus unit tests | Tests: go test ./internal/memory -run TestEllieProactiveInjectionScoresAndThresholds -count=1; go test ./internal/memory -run TestEllieProactiveInjectionBundlesTopMatches -count=1; go test ./internal/memory -run TestEllieProactiveInjectionIncludesSupersessionNote -count=1; go test ./internal/memory -count=1
- [2026-02-12 15:42 MST] Issue #855 | Commit d96f2a0 | closed | Added Ellie context injection worker + server/config wiring (fast-path embedding, cooldown gating, bundle injection path, startup lifecycle integration) with new worker/config/main tests | Tests: go test ./internal/memory -run TestEllieContextInjectionWorkerFastTracksMessageEmbedding -count=1; go test ./internal/memory -run TestEllieContextInjectionWorkerSkipsSystemAndContextInjectionTypes -count=1; go test ./internal/config -run TestLoadIncludesEllieContextInjectionDefaults -count=1; go test ./cmd/server -run TestMainStartsEllieContextInjectionWorkerWhenConfigured -count=1; go test ./internal/memory ./internal/config ./cmd/server ./internal/store -count=1
- [2026-02-12 15:42 MST] Issue #856 | Commit 355ba26 | closed | Added regression coverage for compaction reinjection, cooldown suppression, multi-org processing, and ledger idempotency | Tests: go test ./internal/memory -run TestEllieContextInjectionReinjectsAfterCompaction -count=1; go test ./internal/memory -run TestEllieContextInjectionCooldownSuppressesRepeat -count=1; go test ./internal/memory -run TestEllieContextInjectionWorkerHandlesMultipleOrgs -count=1; go test ./internal/store -run TestEllieContextInjectionStoreRecordInjectionIdempotent -count=1; go test ./internal/memory ./internal/store ./internal/config ./cmd/server -count=1
- [2026-02-12 15:45 MST] Issue #856 | Commit 10499ed | branch-isolation sync | Rebased/cherry-picked spec-152 onto clean `origin/main` ancestry (single-spec branch `codex/spec-152-ellie-proactive-context-injection-clean`) with commits `0b9f6e9`, `fb1a7c8`, `079c230`, `cacfde4`, `10499ed`; full `go test ./... -count=1` passed | Tests: go test ./... -count=1
- [2026-02-12 15:45 MST] Issue #856 | Commit 10499ed | review-ready | Opened PR #857 from `codex/spec-152-ellie-proactive-context-injection-clean` to `main` and moved spec 152 from `02-in-progress` to `03-needs-review` | Tests: n/a
- [2026-02-12 16:08 MST] Issue #862 | Commit n/a | in_progress | Re-queued spec 152 reviewer-required changes by moving spec from `01-ready` to `02-in-progress` | Tests: n/a
- [2026-02-12 16:09 MST] Issue #152 | Commit n/a | branch | Switched to remediation branch `codex/spec-152-ellie-proactive-context-injection-r2` from `origin/codex/spec-152-ellie-proactive-context-injection-clean` for isolated reviewer fixes | Tests: n/a
- [2026-02-12 16:09 MST] Issue #871 | Commit n/a | created | Added micro-issue for deterministic sender ID intent documentation + regression test to complete reviewer-required issue set alongside #862/#863 | Tests: n/a
- [2026-02-12 16:15 MST] Issue #862 | Commit ead8068 | closed | Wired supersession mapping from store candidates into proactive scoring candidates and added supersession-note worker/integration tests | Tests: go test ./internal/memory -run 'TestEllieContextInjectionWorkerIncludesSupersessionNoteWhenCandidateSupersedesPriorMemory|TestEllieContextInjectionIncludesSupersessionNote|TestEllieContextInjectionWorkerFastTracksMessageEmbedding' -count=1
- [2026-02-12 16:15 MST] Issue #863 | Commit bc01f30 | closed | Replaced hardcoded room/injection scoring inputs with real counts via new store queries and added suppression regression coverage for high-message/high-injection rooms | Tests: go test ./internal/memory -run 'TestEllieContextInjectionWorkerUsesRoomAndInjectionCountsForScoring|TestEllieContextInjectionWorkerIncludesSupersessionNoteWhenCandidateSupersedesPriorMemory' -count=1; go test ./internal/store -run TestEllieContextInjectionStoreCountsRoomMessagesAndPriorInjections -count=1
- [2026-02-12 16:15 MST] Issue #871 | Commit 435ea68 | closed | Documented deterministic Ellie sender IDs as synthetic org-derived IDs and added source-level regression test | Tests: go test ./internal/memory -run 'TestDeterministicEllieSenderIDIsDocumentedAsSynthetic|TestEllieContextInjectionWorkerUsesRoomAndInjectionCountsForScoring' -count=1
- [2026-02-12 16:15 MST] Issue #152 | Commit 435ea68 | in_review | Opened PR #876, removed resolved top-level reviewer-required block, and validated full regression on remediation branch | Tests: go test ./... -count=1
- [2026-02-12 16:15 MST] Issue #152 | Commit 435ea68 | moved | Transitioned spec file from `02-in-progress` to `03-needs-review` after reviewer remediation completion | Tests: go test ./... -count=1
