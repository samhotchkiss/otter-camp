# Issue 126: Memory entity synthesis not running

## Problem
`memory_entity` table always has 0 rows despite 76 memory items (all in candidate status).
Memory query results lack entity enrichment (composite_score based only on similarity + confidence).

## Root Cause
The memory entity synthesis pipeline (entity extraction from memory items) is not running.
`memory_entity_mention` table also has 0 rows.

`memory_compaction_run` (consolidation runs) would create entity mentions, but no compaction jobs have run.

## DB Evidence
```sql
SELECT COUNT(*) FROM memory_entity;          -- 0
SELECT COUNT(*) FROM memory_entity_mention;  -- 0
SELECT COUNT(*) FROM memory_compaction_run;  -- 0 or none
```

## Related
- Memory taxonomy is empty (0 nodes in `memory_taxonomy_node`) — Ellie/taxonomy bootstrapping not run
- Memory items are all in `candidate` status (7-day hold by design — correct)
- Memory query works but returns raw semantic matches, not entity-enriched results

## Root Cause Detail
`POST /v1/memory/consolidate` correctly enqueues a `memory_sleep_reflection` job.
But the worker (`internal/worker/worker.go`) never calls `sleepReflector.RegisterJobs(jqWorker)`,
so no handler is registered for `memory_sleep_reflection` jobs. Jobs stay in `pending` forever.

The `SleepReflector` requires a `CandidateDeduplicator` interface implementation
(an LLM-based agent that reviews memory batches) — not yet wired.

## Fix
1. Implement a `CandidateDeduplicator` using the model gateway (LLM-based review of candidate batches)
2. Register `SleepReflector.RegisterJobs(jqWorker)` in `internal/worker/worker.go`
3. Bootstrap taxonomy via a one-time seed (or Ellie initialization)

## Priority: MEDIUM
Entity synthesis enriches memory retrieval quality. Without it, memory query works but returns less structured results.
