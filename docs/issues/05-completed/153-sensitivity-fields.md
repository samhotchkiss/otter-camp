# Issue #153 — Add Sensitivity Fields to Conversations and Memories

> **Priority:** P1
> **Status:** Ready
> **Depends on:** #150 (Conversation Schema Redesign — must complete first)
> **Author:** Josh S

## Summary

Add a `sensitivity` column to the `memories` and `conversations` tables created by #150. This was identified after #150 moved to in-progress and couldn't be edited.

## Why

Multi-human support is on the near-term roadmap. Tagging sensitivity during Ellie's async extraction pass (#151) is cheap now but expensive to backfill later. Store the flag now, enforce it when multi-user ships.

## Changes

### Migration

```sql
-- Add sensitivity to memories
ALTER TABLE memories
    ADD COLUMN sensitivity TEXT NOT NULL DEFAULT 'normal'
    CHECK (sensitivity IN ('normal', 'sensitive'));

-- Add sensitivity to conversations
ALTER TABLE conversations
    ADD COLUMN sensitivity TEXT NOT NULL DEFAULT 'normal'
    CHECK (sensitivity IN ('normal', 'sensitive'));

-- Index for future filtered queries
CREATE INDEX memories_org_sensitivity_idx
    ON memories (org_id, sensitivity) WHERE sensitivity = 'sensitive';
```

### Sensitivity Values

| Value | Meaning |
|-------|---------|
| `normal` | Visible to all agents in the org |
| `sensitive` | Restricted visibility (future: room-scoped or allow-list only) |

### What Gets Flagged Sensitive (by Ellie, #151)

- Health/medical information
- Financial details (account numbers, balances, transactions)
- Personal relationships, family matters
- Credentials, passwords, API keys (conversation flagged; these shouldn't be stored as memories)
- Anything the user explicitly marks as private

### Current Behavior (Single-User)

Flag is stored but **not enforced**. All agents see everything. This is correct for single-user.

### Future Behavior (Multi-User)

Sensitive memories/conversations restricted to agents in the originating room or an explicit allow-list. Data and tags already in place; enforcement is a query filter addition.

## Acceptance Criteria

- [ ] `memories` table has `sensitivity` column with default `'normal'`
- [ ] `conversations` table has `sensitivity` column with default `'normal'`
- [ ] CHECK constraint enforces valid values
- [ ] Migration is backward-compatible (existing rows get `'normal'`)
- [ ] Store layer exposes sensitivity in read/write paths

## References

- #150 — Conversation Schema Redesign (creates the tables)
- #151 — Ellie Memory Infrastructure (tags sensitivity during extraction)

## Execution Log

- [2026-02-12 15:50 MST] Issue #859 | Commit n/a | in_progress | Moved spec 153 from 01-ready to 02-in-progress and created branch codex/spec-153-sensitivity-fields from origin/main | Tests: n/a
- [2026-02-12 15:50 MST] Issue #859 | Commit n/a | created | Created full pre-implementation micro-issue set #859-#861 with explicit tests before coding | Tests: n/a
- [2026-02-12 15:59 MST] Issue #859 | Commit 1e8670c | closed | Added migration 071 for `memories.sensitivity` with constraint/index and schema validation coverage for memories+conversations sensitivity | Tests: go test ./internal/store -run 'TestMigration071MemoriesSensitivityFilesExistAndContainConstraint|TestSchemaMemoriesAndConversationsSensitivityColumnsAndConstraints' -count=1; go test ./internal/store -count=1
- [2026-02-12 15:59 MST] Issue #860 | Commit f00cddc | closed | Extended conversation segmentation and Ellie ingestion write paths with sensitivity defaulting/validation and regression tests | Tests: go test ./internal/store -run 'TestConversationSegmentationStoreDefaultsSensitivityToNormal|TestConversationSegmentationStorePersistsSensitiveConversations|TestConversationSegmentationStoreRejectsInvalidSensitivity|TestEllieIngestionStoreDefaultsMemorySensitivityToNormal|TestEllieIngestionStorePersistsSensitiveMemory|TestEllieIngestionStoreRejectsInvalidMemorySensitivity' -count=1; go test ./internal/store -count=1
- [2026-02-12 15:59 MST] Issue #861 | Commit b719201 | closed | Added Ellie retrieval store read models/queries with sensitivity propagation for memories/conversations and org/project scope tests | Tests: go test ./internal/store -run 'TestEllieRetrievalStoreIncludesMemorySensitivity|TestEllieRetrievalStoreIncludesConversationSensitivityInRoomAndHistory|TestEllieRetrievalStoreProjectAndOrgScopes' -count=1; go test ./internal/store -count=1
- [2026-02-12 15:59 MST] Issue #153 | Commit b719201 | in_review | Opened PR #865 for spec 153 implementation visibility | Tests: go test ./... -count=1
- [2026-02-12 15:59 MST] Issue #153 | Commit b719201 | moved | Transitioned spec file from `02-in-progress` to `03-needs-review` after implementation completion | Tests: go test ./... -count=1
