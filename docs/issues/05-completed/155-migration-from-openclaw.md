# Issue #155 — Migration: Import OpenClaw Agents & Conversation History

> **Priority:** P0
> **Status:** Ready
> **Depends on:** #150 (Conversation Schema Redesign), #151 (Ellie Memory Infrastructure)
> **Author:** Josh S / Sam

## ⚠️ Non-Destructive Constraint

**This migration is READ-ONLY with respect to OpenClaw.** The migration process:
- ✅ READS OpenClaw config files, agent workspaces, and JSONL session logs
- ✅ WRITES only to the Otter Camp database
- ❌ NEVER modifies, deletes, moves, or writes to any OpenClaw file or directory
- ❌ NEVER touches OpenClaw's database, config, or running processes

OpenClaw continues running normally before, during, and after migration. The migration is a one-way copy: OpenClaw → Otter Camp.

## Summary

Import a user's entire OpenClaw agent roster and JSONL conversation history into Otter Camp's database. Agents become rows in the agents table. Conversations become rooms + chat_messages. Ellie's ingestion pipeline extracts memories from the backfilled history, giving her full institutional knowledge from day one.

## Phase 1: Agent Import

Read the user's OpenClaw configuration and import every agent.

### What Gets Imported

| Source | Destination |
|--------|------------|
| Agent slug | `agents.slug` |
| Display name | `agents.display_name` |
| Emoji | `agents.emoji` |
| Role | `agents.role` |
| SOUL.md file content | `agents.soul_md` |
| IDENTITY.md file content | `agents.identity_md` |
| Model config | `agents.instructions_md` (or metadata) |

### Status Assignment

| Agent | Status | Why |
|-------|--------|-----|
| Frank | `active` | Permanent — Chief of Staff |
| Ellie | `active` | Permanent — Chief Context & Compliance Officer |
| Lori | `active` | Permanent — Agent Resources Director |
| All others (Nova, Max, Derek, Josh S, Jeremy H, etc.) | `inactive` | Legacy agents — preserved for history, not running |

All imported agents: `is_ephemeral = false` (they were permanent in the old model).

### Implementation

Extend the existing `EnsureOpenClawRequiredAgents` flow in `internal/import/openclaw.go`:

```
1. Read ~/.openclaw/openclaw.json → agents.list[] for agent IDs
2. For each agent ID:
   a. Workspace dir: ~/.openclaw/workspace-{id}/
   b. Read SOUL.md from workspace (if exists)
   c. Read IDENTITY.md from workspace (if exists)
   d. Read TOOLS.md from workspace (if exists — store in instructions_md)
   e. Upsert into agents table (match on org_id + slug)
   f. Set status based on Three + Temps assignment
3. Log import summary: N agents imported, 3 active, N-3 inactive
```

**Config structure** (actual `~/.openclaw/openclaw.json`):
```json
{
  "agents": {
    "list": [
      {"id": "main", "heartbeat": {"every": "30s"}},
      {"id": "email-mgmt", "heartbeat": {"every": "30s"}},
      ...
    ]
  }
}
```

Agent config is minimal (just id + heartbeat). All personality data lives in workspace files.

### Idempotent

Running the import twice produces the same result. Upsert on `(org_id, slug)`. Existing agents are updated, not duplicated.

## Phase 2: JSONL Backfill

Crawl every JSONL session log file and insert into the new conversation schema (#150).

### Room Structure

**One room per agent.** All sessions with the same agent merge into a single room.

- "Sam & Frank" is one room with the full conversation history
- Conversation segmentation worker handles topic boundaries — session boundaries don't matter
- Far fewer rooms (13 vs 1,300+)
- Matches how rooms work going forward — one ongoing relationship, many conversations

```
Room:
  type: 'ad_hoc' (historical import — no project context available)
  name: "Sam & {agent_display_name}"
  context_id: NULL
```

### Room Participants

Each imported room gets two participants:
- The agent (mapped via slug → agents.id)
- The user (Sam)

Multiple JSONL files for the same agent all feed into the same room, ordered chronologically.

### JSONL Format (Actual)

OpenClaw JSONL files are **not** simple role/content lines. Each line is a typed event:

```jsonl
{"type":"session","version":3,"id":"<uuid>","timestamp":"...","cwd":"..."}
{"type":"model_change","id":"...","modelId":"claude-opus-4-6",...}
{"type":"thinking_level_change","id":"...","thinkingLevel":"low",...}
{"type":"message","id":"...","parentId":"...","timestamp":"...","message":{"role":"user","content":[{"type":"text","text":"..."}]}}
{"type":"message","id":"...","parentId":"...","timestamp":"...","message":{"role":"assistant","content":[{"type":"text","text":"..."},{"type":"thinking","thinking":"..."}]}}
{"type":"message","id":"...","parentId":"...","timestamp":"...","message":{"role":"toolResult","toolCallId":"...","toolName":"read","content":[...]}}
{"type":"custom",...}
```

Key observations:
- Role lives at `message.role`, not top-level
- Content is an array of typed blocks (`text`, `thinking`, `tool_use`, `tool_result`)
- `parentId` chains form a tree (branching conversations possible)
- `thinking` blocks contain reasoning — useful context but potentially large
- `toolResult` messages contain tool call outputs
- Sessions are per-agent: `~/.openclaw/agents/<slug>/sessions/<uuid>.jsonl`

### Message Parsing

| JSONL Event | Destination |
|-------------|------------|
| `type: "message"`, `message.role: "user"` | `sender_type: 'user'`, `sender_id: <user id>` |
| `type: "message"`, `message.role: "assistant"` | `sender_type: 'agent'`, `sender_id: <agent id>`. Extract text blocks only; skip thinking blocks. |
| `type: "message"`, `message.role: "toolResult"` | `type: 'system'` with tool name + summary in body. Skip raw output (too verbose). |
| `type: "session"` | Metadata only — don't create a message. Use timestamp as session start. |
| `type: "model_change"` / `type: "thinking_level_change"` / `type: "custom"` | Skip — operational metadata, not conversation content. |
| `timestamp` field | `created_at` |

**Thinking blocks:** Do NOT import thinking/reasoning content as messages. They're internal model reasoning, not conversation. However, they may contain useful context for memory extraction — Ellie can read them during Phase 3/4 but they shouldn't become chat_messages.

### Processing Order

Sessions are already grouped by agent in the filesystem: `~/.openclaw/agents/<slug>/sessions/*.jsonl`

```
1. Scan ~/.openclaw/agents/*/sessions/*.jsonl
2. Agent slug comes from the directory name (e.g., agents/main/sessions/ → slug "main")
3. For each file:
   a. Create room (if not already created for this session)
   b. Add participants
   c. Parse each line → insert chat_message
   d. Preserve chronological order via created_at
4. Log: N files processed, M messages imported, K rooms created
```

### Async Workers Pick Up From Here

After backfill, the existing async workers from #150 process the imported messages:

1. **Embedding worker** — finds all `chat_messages` where `embedding IS NULL`, generates vectors. This will be a large batch on first run.
2. **Conversation segmentation worker** — groups messages within each room by topic/time, assigns `conversation_id`.

### Volume Estimates

For a power user (Sam-scale, actual numbers from his install):
- 14 agent slugs, ~550 JSONL files total (515 in `main` alone)
- Agent distribution is very uneven: main (Frank) = 515 sessions, others = 2-9 each
- Estimated ~200K-400K messages (needs sampling to refine)
- Embedding at 1,000/min (local model) = ~3-7 hours for full backfill
- This runs in the background — Otter Camp is usable immediately, embeddings fill in over time

## Phase 3: Memory Extraction

After messages are imported and embedded, Ellie's ingestion pipeline (#151) runs over the entire history.

### How It Works

Ellie's normal 5-minute ingestion cycle processes new messages by cursor. For the initial backfill, we run the same pipeline but starting from the beginning:

```
1. Set Ellie's cursors to epoch (beginning of time)
2. Ingestion pipeline processes messages in chronological order
3. For each conversation window:
   a. LLM extraction — decisions, preferences, lessons, patterns
   b. Importance + confidence scoring
   c. Sensitivity tagging (#153)
   d. Dedup check
   e. Write to memories table
4. Advance cursors normally after backfill completes
```

### What Ellie Learns

From months of conversation history, Ellie extracts:
- Every technical decision ("we chose Postgres", "we use Drizzle")
- Every stated preference ("small commits", "no Prisma", "explicit migrations")
- Every lesson learned ("Prisma caused problems", "reply campaign got us blocked")
- Every anti-pattern ("tried X, it failed because Y")
- Every project's context and stack
- Process outcomes ("6-agent pipeline worked for social campaigns")

**Day one, Ellie knows everything.** Not because someone manually entered it, but because she read every conversation that ever happened.

### Cost Consideration

Memory extraction is LLM-powered. For 650K messages:
- Chunked into ~5,000 conversation windows
- Each window = one Sonnet call for extraction
- At ~$0.003/call = ~$15 total for full backfill
- Runs async, can be throttled to stay within rate limits

### Backfill Mode

The ingestion pipeline needs a "backfill" flag that:
- Processes from epoch instead of last cursor
- Runs at higher throughput (larger batches, shorter sleep)
- Logs progress: "Processing 2025-06... 2025-07... 2025-08..."
- Can be paused and resumed (cursor tracks progress)

## Phase 4: Project & Issue Discovery

After memory extraction, Ellie makes a second pass to identify projects and issues from the conversation history. The goal: when the user opens Otter Camp for the first time, the dashboard is fully populated — every project, every outstanding thread, every completed effort. Full context, ready to go.

### How It Works

Ellie processes conversations chronologically, building up a picture of work over time. This is a **synthesis** pass — she's not just extracting facts, she's identifying structure:

```
1. Read conversation windows chronologically (same chunks as memory extraction)
2. For each window, extract:
   a. Project references — named efforts with ongoing identity
   b. Issue/task references — specific work items, bugs, decisions
   c. Status signals — "shipped", "done", "blocked", "dropped", "we should..."
   d. Agent involvement — who worked on what
3. Accumulate into a running project/issue graph
4. After all conversations processed:
   a. Deduplicate — merge references to the same project/issue across conversations
   b. Assign statuses based on resolution signals and recency
   c. Assign issues to projects based on context
   d. Write to Otter Camp projects and issues tables
```

### Project Extraction

Projects are named, ongoing efforts that span multiple conversations:

| Extracted Field | Source |
|----------------|--------|
| `name` | Recurring project name across conversations ("ItsAlive", "Three Stones", "Pearl") |
| `description` | Synthesized from all conversations about the project — what it is, what it does, current state |
| `status` | `active` / `completed` / `archived` — based on resolution signals and last mention date |
| `agents` | Which agents were involved (project participants) |
| `last_discussed` | Timestamp of most recent conversation mentioning this project |

**Status heuristics:**
- Mentioned in last 30 days with no completion signal → `active`
- Explicit completion signal ("we shipped it", "that's done") → `completed`
- Not mentioned in 60+ days with no completion signal → `archived` (stale/dropped)

### Issue Extraction

Issues are specific tasks, bugs, decisions, or threads within (or outside) a project:

| Extracted Field | Source |
|----------------|--------|
| `title` | Short description of the task/thread |
| `body` | Context — what was discussed, what was decided, what's outstanding |
| `project_id` | Parent project (if identifiable) |
| `status` | `open` / `closed` — based on resolution signals |
| `assigned_agent` | Which agent was working on it (if clear) |
| `priority` | Inferred from urgency signals ("we need this ASAP" vs "someday we should") |
| `last_discussed` | Most recent mention |

**The scoping problem:** "Update the favicon" is an issue within ItsAlive. "Build ItsAlive" is the project. Ellie needs to understand hierarchy:

- If something has sub-tasks and spans weeks/months → project
- If something is a discrete task that could be "done" → issue
- If ambiguous, default to issue within an "Uncategorized" project

### Cross-Conversation Deduplication

The same issue might come up in conversations with different agents:
- Sam discusses favicon with Derek in March
- Sam discusses favicon with Jeff G in July
- That's **one issue**, not two

Dedup strategy:
1. During accumulation, Ellie maintains a running list of known projects/issues
2. Each new extraction is compared against existing items (semantic similarity on title + context)
3. If match confidence > 0.85 → merge (update status, append context, update last_discussed)
4. If match confidence < 0.85 → create new item
5. Final dedup pass after all conversations processed to catch anything missed

### What The User Sees

On first login after migration, the dashboard shows:

```
📋 Projects (7)
  ✅ Social Media Campaign          — Completed (Aug 2025)
  🟢 ItsAlive                       — Active, 3 open issues
  🟢 Three Stones                   — Active, 5 open issues  
  🟢 Pearl                          — Active, 2 open issues
  🟢 Technonymous                   — Active, 1 open issue
  📦 Eulogy Book                    — Archived (last discussed Apr 2025)
  📦 Landing Page Redesign          — Archived (last discussed Mar 2025)

Click any project to see its issues, full context, and conversation history.
```

Each project page shows:
- Description (synthesized from conversations)
- Open issues with full context
- Closed issues (historical record)
- Which agents were involved
- Link to relevant conversation history

### Cost Estimate

This pass reuses the same conversation chunks as memory extraction. Additional LLM calls:
- ~5,000 chunks × 1 Sonnet call each for project/issue extraction = ~$15
- Plus one final synthesis call per discovered project to generate description = negligible
- Total: ~$15 additional (on top of memory extraction's ~$15)

### Ordering

Phase 4 runs **after** Phase 3 (memory extraction), because Ellie's accumulated memories provide additional context for scoping projects vs issues. The memories table acts as a knowledge base during project discovery.

Alternatively, Phases 3 and 4 can run as a single pass — extract memories AND project/issue references from each conversation window simultaneously. This halves the LLM calls but makes the extraction prompt more complex. **Recommended: single pass with a combined prompt.**

## Migration CLI

```bash
# Full migration: agents + history + memories + projects
otter migrate from-openclaw --openclaw-dir ~/.openclaw

# Just agents (fast, no history)
otter migrate from-openclaw --openclaw-dir ~/.openclaw --agents-only

# Just backfill history (agents already imported)
otter migrate from-openclaw --openclaw-dir ~/.openclaw --history-only

# Check progress
otter migrate status

# Dry run — show what would be imported
otter migrate from-openclaw --openclaw-dir ~/.openclaw --dry-run
```

### Flags

| Flag | Purpose |
|------|---------|
| `--openclaw-dir` | Path to OpenClaw root directory (default: `~/.openclaw`). Reads config, workspaces, and sessions from here. |
| `--agents-only` | Import agents without conversation history |
| `--history-only` | Backfill conversations (agents must exist) |
| `--dry-run` | Show what would be imported without writing |
| `--since` | Only import sessions after this date |

## Progress Visibility

The migration can take hours (embedding 650K+ messages, extracting memories). Users need to see what's happening.

### Progress Table

```sql
CREATE TABLE migration_progress (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    migration_type TEXT NOT NULL,  -- 'agent_import', 'history_backfill', 'memory_extraction', 'project_discovery'
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending', 'running', 'paused', 'completed', 'failed'
    )),
    total_items INT,              -- total files/messages/chunks to process
    processed_items INT NOT NULL DEFAULT 0,
    failed_items INT NOT NULL DEFAULT 0,
    current_label TEXT,           -- human-readable: "Processing 2025-06..." or "Frank: 342/1200 messages"
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### CLI Progress

```bash
$ otter migrate status

Migration Status:
  Agent Import:       ✅ Complete (13 agents, 3 active, 10 inactive)
  History Backfill:   🔄 Running — 412,000 / 650,000 messages (63%)
                      Currently: Derek — 2025-08-14
  Embedding:          🔄 Running — 380,000 / 650,000 embedded (58%)
  Memory Extraction:  ⏳ Pending (starts after embedding)
  Project Discovery:  ⏳ Pending (runs with memory extraction)
```

### Web UI

The Otter Camp dashboard shows a migration banner when a migration is active:

```
📦 Importing conversation history... 63% complete
    412,000 / 650,000 messages imported. Embedding: 58%.
    Memory extraction and project discovery will begin automatically.
```

Banner dismisses when all phases complete.

### Pause/Resume

```bash
$ otter migrate pause     # Pauses at next checkpoint
$ otter migrate resume    # Resumes from cursor
```

Cursors track progress — pause just stops the loop, resume picks up exactly where it left off.

## Open Questions

1. ~~**Project attribution**~~ — Resolved: leave context_id null. Embeddings make everything searchable regardless.
2. **Tool calls** — Store tool call/result pairs as system messages? Or skip them? They contain useful context (what the agent tried, what worked) but add volume.
3. **Compacted sessions** — Some JSONL files may have been compacted (summary replacing original messages). Import the summary as-is? Flag it?
4. **Multiple OpenClaw instances** — If a user has run multiple OpenClaw setups over time, can they import from all of them? Dedup on timestamp + agent + content?
5. **Memory file import** — Current agents have `memory/YYYY-MM-DD.md` files. Import these as memories directly (faster, already curated) in addition to extracting from JSONL?

## Non-OpenClaw Users

This migration is OpenClaw-specific. Users coming from other setups (raw Claude, ChatGPT, Cursor, etc.) won't have JSONL session logs or an agent roster to import. For them, Ellie starts fresh — learning from conversations as they happen. No migration needed; the ingestion pipeline (#151) handles everything going forward.

A future import path could support other formats (ChatGPT export JSON, etc.) but that's out of scope here.

## References

- #150 — Conversation Schema Redesign (target tables)
- #151 — Ellie Memory Infrastructure (ingestion pipeline)
- #153 — Sensitivity Fields (tagged during extraction)
- Existing import: `internal/import/openclaw.go`
- OpenClaw root: `~/.openclaw/`
- OpenClaw config: `~/.openclaw/openclaw.json`
- Agent workspaces: `~/.openclaw/workspace-<slug>/` (SOUL.md, IDENTITY.md, TOOLS.md, memory/)
- Session logs: `~/.openclaw/agents/<slug>/sessions/<uuid>.jsonl`

## Execution Log
- [2026-02-12 17:05 MST] Issue #155 | Commit n/a | in_progress | Moved spec 155 from /Users/sam/Documents/Dev/otter-camp/issues/01-ready to /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress and created branch codex/spec-155-migration-from-openclaw from origin/main | Tests: n/a
- [2026-02-12 17:06 MST] Issue #895 | Commit n/a | created | Created micro-issue for migration_progress schema/store primitives with explicit migration + store tests | Tests: n/a
- [2026-02-12 17:06 MST] Issue #896 | Commit n/a | created | Created micro-issue for OpenClaw agent roster import with identity files, status mapping, and idempotent upsert tests | Tests: n/a
- [2026-02-12 17:06 MST] Issue #897 | Commit n/a | created | Created micro-issue for JSONL session parser covering typed event extraction and skip rules | Tests: n/a
- [2026-02-12 17:06 MST] Issue #898 | Commit n/a | created | Created micro-issue for room/participant/message backfill persistence and idempotent history import tests | Tests: n/a
- [2026-02-12 17:06 MST] Issue #899 | Commit n/a | created | Created micro-issue for otter migrate from-openclaw CLI modes, dry-run, and since filter tests | Tests: n/a
- [2026-02-12 17:06 MST] Issue #900 | Commit n/a | created | Created micro-issue for migration status + pause/resume orchestration and API status visibility tests | Tests: n/a
- [2026-02-12 17:06 MST] Issue #901 | Commit n/a | created | Created micro-issue for Ellie ingestion backfill mode and cursor resume behavior tests | Tests: n/a
- [2026-02-12 17:06 MST] Issue #902 | Commit n/a | created | Created micro-issue for post-import project/issue discovery pass and dedupe/status inference tests | Tests: n/a
- [2026-02-12 17:06 MST] Issue #903 | Commit n/a | created | Created micro-issue for read-only OpenClaw migration guardrails and summary reporting tests | Tests: n/a
- [2026-02-12 17:13 MST] Issue #895 | Commit e5b2909 | closed | Added migration 073 + MigrationProgressStore primitives with org-scoped create/advance/status/get flows and coverage for pause/resume/completed + org isolation | Tests: go test ./internal/store -run TestMigration073MigrationProgressFilesExistAndContainCoreDDL -count=1; go test ./internal/store -run "TestMigrationProgressStore(CreateAndGet|AdvanceAndFail|PauseResume|OrgScoping)" -count=1; go test ./internal/store -count=1; go test ./... -count=1
- [2026-02-12 17:17 MST] Issue #896 | Commit 245b58e | closed | Added OpenClaw agent roster importer with identity file upsert, Three+Temps status mapping, non-ephemeral enforcement, and idempotent `(org_id, slug)` updates plus fixture-driven importer tests | Tests: go test ./internal/import -run TestOpenClawAgentImportUpsertsRosterAndIdentityFiles -count=1; go test ./internal/import -run TestOpenClawAgentImportStatusMappingAndIdempotency -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 17:20 MST] Issue #897 | Commit 65b39c2 | closed | Added OpenClaw JSONL parser for `agents/*/sessions/*.jsonl` with deterministic ordering, message role normalization (user/assistant/toolResult), operational-event skips, and assistant thinking exclusion | Tests: go test ./internal/import -run TestOpenClawSessionParserExtractsConversationEvents -count=1; go test ./internal/import -run TestOpenClawSessionParserSkipsThinkingAndOperationalEvents -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 17:24 MST] Issue #898 | Commit d02199e | closed | Added OpenClaw history backfill service for one ad-hoc room per agent, user+agent participant mapping, deterministic chat message IDs, chronological insert ordering, and idempotent rerun behavior | Tests: go test ./internal/import -run TestOpenClawHistoryBackfillCreatesSingleRoomPerAgent -count=1; go test ./internal/import -run TestOpenClawHistoryBackfillAddsParticipantsAndMessagesChronologically -count=1; go test ./internal/import -run TestOpenClawHistoryBackfillIsIdempotent -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 17:27 MST] Issue #899 | Commit 639364f | closed | Added `otter migrate from-openclaw` CLI command with mode flags (`--agents-only`/`--history-only`), dry-run planning output, `--since` filtering, org/db/user resolution, and wiring to OpenClaw agent import + history backfill services | Tests: go test ./cmd/otter -run TestMigrateFromOpenClawCommandParsesModesAndFlags -count=1; go test ./cmd/otter -run TestMigrateFromOpenClawDryRunOutput -count=1; go test ./cmd/otter -run TestMigrateFromOpenClawSinceFilter -count=1; go test ./cmd/otter -count=1; go test ./... -count=1
- [2026-02-12 17:33 MST] Issue #900 | Commit c1644bb | closed | Added migration runner progress lifecycle orchestration with pause/resume checkpoints, CLI status/pause/resume controls, store list/bulk-status primitives, and API migration status endpoint for banner/phase visibility | Tests: go test ./cmd/otter -run TestMigrateStatusPauseResumeCommands -count=1; go test ./internal/api -run TestMigrationStatusEndpointReturnsPhaseProgress -count=1; go test ./internal/import -run TestMigrationRunnerPauseAndResumeFromCheckpoint -count=1; go test ./cmd/otter ./internal/api ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 17:39 MST] Issue #901 | Commit aa8d0a1 | closed | Added Ellie ingestion backfill worker mode with epoch start/cursor resume behavior and wired migration runner memory_extraction phase orchestration | Tests: go test ./internal/memory -run TestEllieIngestionWorkerBackfillModeStartsFromEpoch -count=1; go test ./internal/memory -run TestEllieIngestionWorkerBackfillModeResumesNormalCursoring -count=1; go test ./internal/import -run TestMigrationRunnerStartsEllieBackfillPhase -count=1; go test ./internal/memory ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 17:46 MST] Issue #902 | Commit 181d764 | closed | Added OpenClaw history project/issue discovery persistence with dedupe/status inference and wired migration runner project_discovery phase progress orchestration | Tests: go test ./internal/import -run TestOpenClawProjectDiscoveryBuildsProjectsAndIssuesFromHistory -count=1; go test ./internal/import -run TestOpenClawProjectDiscoveryDedupesCrossConversationReferences -count=1; go test ./internal/import -run TestOpenClawProjectDiscoveryStatusInference -count=1; go test ./internal/import -run TestMigrationRunnerStartsProjectDiscoveryPhase -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 17:50 MST] Issue #903 | Commit 857f07e | closed | Enforced read-only OpenClaw source guardrails with snapshot mutation detection plus migration summary audit reporting and docs | Tests: go test ./internal/import -run TestOpenClawMigrationDoesNotMutateSourceWorkspaceFiles -count=1; go test ./internal/import -run TestOpenClawMigrationRejectsUnsafeWriteOperations -count=1; go test ./internal/import -run TestOpenClawMigrationSummaryReport -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 17:50 MST] Issue #155 | Commit n/a | moved_to_03-needs-review | Completed all planned micro-issues (#895-#903) and moved spec file from /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress to /Users/sam/Documents/Dev/otter-camp/issues/03-needs-review pending external review sign-off | Tests: n/a
- [2026-02-12 18:09 MST] Issue #155 | Commit n/a | in_progress | Re-queued spec 155 reviewer remediation and started pre-implementation micro-issue planning for branch isolation + P0/P1/P2 findings. | Tests: n/a
- [2026-02-12 18:11 MST] Issue #912/#913/#914/#915/#916/#917 | Commit n/a | created | Created granular reviewer-remediation micro-issues with explicit tests for spec-155 P0/P1/P2 findings (branch isolation tracked separately in #909). | Tests: n/a
- [2026-02-12 18:11 MST] Issue #910/#911 | Commit n/a | closed | Closed superseded aggregate remediation issues after splitting into explicit micro-issues #912-#917. | Tests: n/a
- [2026-02-12 18:12 MST] Issue #909 | Commit n/a | closed | Cleared branch-isolation blocker by verifying spec-155 branch now contains only spec-specific commits relative to origin/main and passes full Go validation gate. | Tests: git log origin/main..HEAD --oneline; go vet ./...; go build ./...; go test ./... -count=1
- [2026-02-12 18:13 MST] Issue #912 | Commit 5869d89 | closed | Replaced hardcoded "Sam &" history room naming with DB-backed user display-name formatting and added non-Sam regression coverage. | Tests: go test ./internal/import -run TestOpenClawHistoryBackfillUsesUserDisplayNameInRoomName -count=1; go test ./internal/import -run TestOpenClawHistoryBackfillCreatesSingleRoomPerAgent -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 18:15 MST] Issue #913 | Commit e8c7e1d | closed | Replaced permissive import UUID regex with strict canonical UUID matching and added malformed UUID rejection tests. | Tests: go test ./internal/import -run TestOpenClawImportRejectsMalformedUUIDStrings -count=1; go test ./internal/import -run TestOpenClawAgentImportStatusMappingAndIdempotency -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 18:17 MST] Issue #914 | Commit ab76004 | closed | Fixed symlink traversal bypass in source guard via resolved-path checks and made parser reject symlinked session files with explicit regression tests. | Tests: go test ./internal/import -run TestOpenClawSourceGuardRejectsSymlinkEscape -count=1; go test ./internal/import -run TestOpenClawSessionParserRejectsSymlinkedSessionFile -count=1; go test ./internal/import -run TestOpenClawMigrationDoesNotMutateSourceWorkspaceFiles -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 18:18 MST] Issue #915 | Commit f5a6ed3 | closed | Added lenient-default session parsing with strict mode support and expanded parser edge-case coverage (empty, blank-only, invalid-only, near-buffer-limit). | Tests: go test ./internal/import -run TestOpenClawSessionParserLenientModeSkipsMalformedLines -count=1; go test ./internal/import -run TestOpenClawSessionParserStrictModeRejectsMalformedLines -count=1; go test ./internal/import -run "TestOpenClawSessionParser(EmptyFile|BlankLinesOnly|InvalidJSONOnly|NearBufferLimitLine)" -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 18:20 MST] Issue #916 | Commit c31aa14 | closed | Fixed migration-runner HistoryOnly semantics + resume safety (skip completed, block failed until reset) and removed custom max shadow helper. | Tests: go test ./internal/import -run TestMigrationRunnerHistoryOnlySkipsEllieAndProjectDiscovery -count=1; go test ./internal/import -run TestMigrationRunnerResumeSkipsCompletedPhase -count=1; go test ./internal/import -run TestMigrationRunnerResumeDoesNotAutoResetFailedPhase -count=1; go test ./internal/import -run "TestMigrationRunner(StartsEllieBackfillPhase|StartsProjectDiscoveryPhase|PauseAndResumeFromCheckpoint)" -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 18:23 MST] Issue #917 | Commit 4aae696 | closed | Tightened project discovery org_id UUID validation and added per-project locking for issue-number allocation to prevent race collisions; added malformed-org and uniqueness regression coverage. | Tests: go test ./internal/import -run TestOpenClawProjectDiscoveryRejectsMalformedOrgID -count=1; go test ./internal/import -run TestOpenClawProjectDiscoveryIssueNumberUniqueness -count=1; go test ./internal/import -run TestOpenClawProjectDiscoveryBuildsProjectsAndIssuesFromHistory -count=1; go test ./internal/import -count=1; go test ./... -count=1
- [2026-02-12 18:23 MST] Issue #909/#912/#913/#914/#915/#916/#917 | Commit n/a/5869d89/e8c7e1d/ab76004/f5a6ed3/c31aa14/4aae696 | resolved | Completed all top-level reviewer-required remediation items for spec 155 and prepared queue transition back to needs-review. | Tests: see issue-level entries above
- [2026-02-12 19:05 MST] Issue #918/#919/#920 | Commit n/a | queue-transition | Moved 155-migration-from-openclaw.md from /Users/sam/Documents/Dev/otter-camp/issues/01-ready to /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress to address top-level Reviewer Required Changes in priority order | Tests: n/a
- [2026-02-12 19:12 MST] Issue #918 | Commit 1d83f3e/dc75ed2 | closed | Rebased spec-155 onto origin/main, resolved ellie ingestion worker conflicts preserving production + backfill behavior, and passed required gate checks | Tests: git merge --no-commit --no-ff origin/main; go vet ./...; go build ./...; go test ./... -count=1
- [2026-02-12 19:15 MST] Issue #919 | Commit 1d83f3e | closed | Confirmed out-of-scope deletions are removed by rebase onto current main and validated migrations 066-068 + Ellie/server/config regression coverage | Tests: go test ./internal/store -run 'TestMigration066EllieIngestionCursorsDownIncludesPolicyDrop|TestMigration067EllieRetrievalStrategiesFilesExistAndContainCoreDDL|TestMigration068EllieRetrievalQualityEventsFilesExistAndContainCoreDDL' -count=1; go test ./cmd/server -run 'TestMainStartsEllieIngestionWorkerWhenConfigured|TestMainConstructsSingleSharedEmbedderForEmbeddingWorkers' -count=1; go test ./internal/config -run 'TestLoadRejectsNaNContextInjectionThreshold|TestLoadRejectsInvalidEmbedderConfigWhenInjectionEnabled' -count=1; go test ./... -count=1
- [2026-02-12 19:16 MST] Issue #920 | Commit e78a231 | closed | Switched migration status route to RequireWorkspace, added migrate CLI timeout contexts, and replaced deterministic message IDs with UUIDv5 semantics plus new regressions | Tests: go test ./internal/api -run 'TestMigrationStatusEndpointReturnsPhaseProgress|TestMigrationStatusEndpointReturnsBadRequestWithoutWorkspaceContext|TestMigrationStatusRouteUsesRequireWorkspaceMiddleware' -count=1; go test ./cmd/otter -run 'TestMigrateStatusPauseResumeCommands|TestMigrateFromOpenClawRunUsesExecutionTimeoutContext' -count=1; go test ./internal/import -run 'TestOpenClawHistoryBackfillIsIdempotent|TestStableOpenClawBackfillMessageIDUsesUUIDv5' -count=1; go test ./... -count=1
- [2026-02-12 19:17 MST] Issue #155 | Commit n/a | moved_to_03-needs-review | Removed resolved top-level Reviewer Required Changes block, opened PR #929 from branch codex/spec-155-migration-from-openclaw-r4, and moved spec back to needs-review | Tests: n/a
