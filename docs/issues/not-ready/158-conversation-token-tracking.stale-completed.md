# 158 — Conversation Token Tracking

> **Priority:** P2
> **Status:** Ready
> **Depends on:** #150 (Conversation Schema Redesign)
> **Author:** Josh S

## Summary

Track token counts per conversation so users and agents can see how much context each conversation has consumed. Enables cost visibility, context window awareness, and future budgeting.

## Schema Changes

```sql
-- Add token tracking columns to chat_messages
ALTER TABLE chat_messages ADD COLUMN token_count INT;

-- Add rolling totals to conversations
ALTER TABLE conversations ADD COLUMN total_tokens INT NOT NULL DEFAULT 0;

-- Add rolling totals to rooms
ALTER TABLE rooms ADD COLUMN total_tokens BIGINT NOT NULL DEFAULT 0;

-- Index for cost reporting queries
CREATE INDEX idx_chat_messages_tokens ON chat_messages (room_id, created_at) WHERE token_count IS NOT NULL;
```

## How It Works

### Token Counting

When a message is inserted into `chat_messages`:
1. Count tokens in `body` using a tokenizer (tiktoken or equivalent)
2. Store in `chat_messages.token_count`
3. Increment `conversations.total_tokens` (if conversation_id is set)
4. Increment `rooms.total_tokens`

### Tokenizer

Use a fast, local tokenizer — no API calls:
- **Option A:** `tiktoken-go` — Go port of OpenAI's tiktoken. cl100k_base encoding covers Claude/GPT-4 reasonably well.
- **Option B:** Simple word-count heuristic (words × 1.3). Less accurate but zero dependencies.

**Recommendation:** tiktoken-go for accuracy. Token counts are used for cost estimation — rough is okay, wildly wrong is not.

### Backfill

For existing messages (imported via #155 or already in the system):
- Async worker scans `chat_messages WHERE token_count IS NULL`
- Counts tokens, updates row
- Rolls up totals to conversations and rooms
- Same pattern as the embedding worker — background, resumable

## API

```
GET /api/v1/rooms/:id              — includes total_tokens
GET /api/v1/conversations/:id      — includes total_tokens
GET /api/v1/rooms/:id/stats        — token breakdown by time period, by agent
```

## CLI

```bash
otter room stats <room-name>
# Output:
# Room: Sam & Frank
# Total tokens: 1,245,000
# Conversations: 87
# Avg tokens/conversation: 14,310
# Last 7 days: 42,000 tokens
```

## Frontend

Show token count in conversation/room headers:
- Room list: subtle token count badge
- Conversation detail: "14.3K tokens" in the header
- Optional: cost estimate based on configurable $/token rate

## Acceptance Criteria

- [ ] `token_count` column on `chat_messages`
- [ ] `total_tokens` column on `conversations` and `rooms`
- [ ] Tokens counted on message insert (sync or near-sync)
- [ ] Backfill worker for existing messages
- [ ] Room stats API endpoint
- [ ] `go test ./... -count=1` passes

## References

- #150 — Conversation Schema Redesign (chat_messages, conversations, rooms tables)
- #155 — Migration from OpenClaw (imported messages need backfill)

## Execution Log
- [2026-02-12 19:19 MST] Issue #158 | Commit n/a | in_progress | Moved spec 158 from /Users/sam/Documents/Dev/otter-camp/issues/01-ready to /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress and created branch codex/spec-158-conversation-token-tracking from origin/main | Tests: n/a
- [2026-02-12 19:20 MST] Issue #930/#931/#932/#933/#934 | Commit n/a | created | Created full pre-implementation micro-issue set for schema/worker/API/CLI/rollup-regression phases with explicit command-level tests before code changes | Tests: n/a
- [2026-02-12 19:27 MST] Issue #930 | Commit ff9b1ec | closed | Added migration 074 token-tracking schema + SQL rollup trigger/functions and schema migration file coverage tests | Tests: go test ./internal/store -run TestMigration074ConversationTokenTrackingFilesExistAndContainCoreDDL -count=1; go test ./internal/store -run TestSchemaMigrationsUpDown -count=1
- [2026-02-12 19:28 MST] Issue #934 | Commit f42d721 | closed | Added DB-backed rollup invariant regression tests covering insert, reassignment, delete rollback, and idempotent backfill updates | Tests: go test ./internal/store -run TestConversationTokenRollups -count=1
- [2026-02-12 19:29 MST] Issue #931 | Commit c7fb46f | closed | Added conversation token backfill store API, worker loop, config/env toggles, and server startup wiring with tests | Tests: go test ./internal/store -run TestConversationTokenBackfillStore -count=1; go test ./internal/memory -run TestConversationTokenBackfillWorker -count=1; go test ./internal/config -run TestLoadConversationTokenBackfill -count=1; go test ./cmd/server -run TestMainStartsConversationTokenBackfillWorkerWhenConfigured -count=1
- [2026-02-12 19:31 MST] Issue #932 | Commit f6b1ebf | closed | Added room/conversation token summary endpoints and room stats API route/middleware coverage | Tests: go test ./internal/api -run TestRoomTokenEndpoints -count=1; go test ./internal/api -run TestConversationTokenEndpoint -count=1; go test ./internal/api -run TestRoomStatsEndpoint -count=1; go test ./internal/api -run TestConversationTokenRoutesUseRequireWorkspaceMiddleware -count=1
- [2026-02-12 19:36 MST] Issue #933 | Commit f7e9914 | closed | Added otter room stats CLI command + ottercli room stats client binding and exposed room_name in room stats API payload for human-readable output | Tests: go test ./cmd/otter -run TestHandleRoomStats -count=1; go test ./internal/ottercli -run TestClientGetRoomStats -count=1; go test ./cmd/otter ./internal/ottercli -count=1; go test ./internal/api ./internal/store -count=1
- [2026-02-12 19:36 MST] Issue #158 | Commit n/a | pr_opened | Opened reviewer visibility PR #935 for branch codex/spec-158-conversation-token-tracking | Tests: n/a
- [2026-02-12 19:36 MST] Issue #158 | Commit n/a | validation | Ran full regression gate for spec acceptance | Tests: go test ./... -count=1
- [2026-02-12 19:36 MST] Issue #158 | Commit n/a | moved_to_needs_review | Moved spec 158 from /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress to /Users/sam/Documents/Dev/otter-camp/issues/03-needs-review after closing all planned micro-issues | Tests: go test ./... -count=1
- [2026-02-12 19:46 MST] Issue #158 | Commit n/a | moved_to_in_progress | Moved spec 158 from /Users/sam/Documents/Dev/otter-camp/issues/01-ready to /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress for reviewer-required remediation work | Tests: n/a
- [2026-02-12 19:46 MST] Issue #936/#937 | Commit n/a | created | Created/remediated full micro-issue set with explicit command-level tests before implementation (branch isolation rebase + org-partition fairness) | Tests: n/a
- [2026-02-12 19:51 MST] Issue #936 | Commit bed653fa | closed | Merged origin/main into spec-158 branch to resolve pre-merge conflict and preserve branch-isolated additive token-tracking diff | Tests: git merge origin/main --no-commit --no-ff; go vet ./...; go build ./...; go test ./... -count=1
- [2026-02-12 19:51 MST] Issue #937 | Commit 789e3d03 | closed | Added org-partitioned token backfill candidate selection and multi-org fairness regression test | Tests: go test ./internal/store -run TestConversationTokenBackfillStoreOrgPartitionFairness -count=1; go test ./internal/store -run TestConversationTokenBackfillStore -count=1; go test ./... -count=1
- [2026-02-12 19:51 MST] Issue #158 | Commit n/a | reviewer_block_resolved | Removed resolved top-level Reviewer Required Changes block after closing issues #936 and #937 | Tests: n/a
- [2026-02-12 19:51 MST] Issue #158 | Commit n/a | moved_to_needs_review | Moved spec 158 from /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress to /Users/sam/Documents/Dev/otter-camp/issues/03-needs-review after completing reviewer-required remediation and pushing branch updates to PR #935 | Tests: go test ./... -count=1
- [2026-02-12 19:52 MST] Issue #158 | Commit n/a | moved_to_in_progress | Re-queued spec 158 from /Users/sam/Documents/Dev/otter-camp/issues/03-needs-review to /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress after fresh origin/main merge-check surfaced new conflicts in internal/config/config{,_test}.go | Tests: git merge origin/main --no-commit --no-ff
- [2026-02-12 19:52 MST] Issue #938 | Commit n/a | created | Created micro-issue for fresh origin/main config merge-conflict remediation with explicit gate tests before implementation | Tests: n/a
- [2026-02-12 19:54 MST] Issue #938 | Commit f7741088 | closed | Merged latest origin/main into spec-158 branch and resolved config conflicts while preserving both JobScheduler and ConversationTokenBackfill config paths | Tests: git merge origin/main --no-commit --no-ff; go test ./internal/config -count=1; go vet ./...; go build ./...; go test ./... -count=1
- [2026-02-12 19:54 MST] Issue #158 | Commit n/a | moved_to_needs_review | Moved spec 158 from /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress to /Users/sam/Documents/Dev/otter-camp/issues/03-needs-review after completing issue #938 and pushing branch updates to PR #935 | Tests: go test ./... -count=1
