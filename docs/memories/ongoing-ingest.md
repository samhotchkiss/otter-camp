# Memories: Ongoing Ingest

> Summary: Continuous ingestion behavior, known gaps, and the bridge reliability problem.
> Last updated: 2026-02-16
> Audience: Agents debugging why memories are or are not being extracted.

## Worker

Main worker: `internal/memory/ellie_ingestion_worker.go`

Core behavior:
- Polls candidate rooms for new messages
- Uses per-room cursor (`ellie_ingestion_cursors`)
- Groups adjacent messages by time window
- Runs LLM extraction when configured
- Falls back to deterministic heuristic extraction when needed

## Modes

- `normal`: incremental ingest from cursor
- `backfill`: broader history extraction path

## Ingest Correctness Controls

- Cursor updates after successful processing
- Dedupe via memory hash conflict path
- Limits for room batch sizes and extraction windows

## ⚠️ Known Gap: Bridge Reliability (Feb 11-15 2026)

`chat_messages` ingestion stopped after Feb 10. Investigation found:
- Feb 11: 151 messages (all cron/heartbeats, no real conversation)
- Feb 12-14: 0 messages
- Feb 15: 13 messages (all Ellie context injections)

**All Slack conversations from Feb 11-15 exist in OpenClaw JSONL session logs but were never bridged to `chat_messages`.** This means 5 days of conversations — including major work like 230+ agent profile generation — are invisible to the memory system.

### Impact
- Significant work product missing from memory
- Current-state queries return stale answers (e.g., "20 profiles" when actual is 230+)
- Entity synthesis can't consolidate facts that were never ingested

### Root Cause
Both `pipeline.mjs` and `ingest.mjs` read exclusively from `chat_messages`. There is no direct JSONL ingestion path.

### Fix (Not Yet Built)
- Build JSONL direct ingestion path that reads OpenClaw session logs
- Backfill the Feb 11-15 gap
- Add monitoring/alerting for bridge ingestion health

## Operational Dependencies

- Conversation embedding/segmentation quality improves downstream retrieval quality
- DB availability required for ingest loop
- Bridge connectivity required for real-time ingestion — **bridge outages create data gaps**

## Extraction Pipeline (from extraction repo)

The extraction pipeline has been extensively tested. Key stages:

1. **Stage 1** (`pipeline.mjs`): LLM extraction from message windows. Prompt tuned for file path extraction, queued-wrapper awareness, multi-human awareness.
2. **Stage 2** (`stage2v2.mjs`): Scoring and filtering. Threshold 40 is appropriate (bimodal distribution peaks at 55-59 and 65-69).
3. **Stage 2.5** (`stage25-projects.mjs`): LLM normalization with cross-day state.
4. **DB insertion**: Embed with 1536d, dedupe, store.

### Extraction Quality Findings
- Cross-validation: 42% strong match, 55% weak match, 3% missed
- Extraction misses specific file paths/artifact locations
- 0.75 similarity threshold for "captured" may be too strict
- Window size optimization (E05) still queued — testing 10/20/30/50 message windows

## Related Docs

- `docs/memories/initial-ingest.md`
- `docs/memories/file-backed-memories.md`
- `docs/memories/experiment-log.md`

## Change Log

- 2026-02-16: Added bridge gap investigation findings, extraction pipeline details, and quality findings from E03/E04.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
