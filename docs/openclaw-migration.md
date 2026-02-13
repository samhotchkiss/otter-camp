# OpenClaw Migration Safety and Audit

The `otter migrate from-openclaw` flow is a one-way import from OpenClaw into Otter Camp.

## Safety Guarantees

- OpenClaw source directories are treated as read-only.
- Migration code validates source paths stay inside the detected OpenClaw root.
- Any attempted write path targeting OpenClaw source is rejected by guard helpers.
- The migration runner snapshots source file hashes before and after execution and fails if any mutation is detected.

## Audit Summary Fields

Every migration run produces an execution summary with explicit counters:

- `agent_import.processed`
- `history_backfill.events_processed`
- `history_backfill.messages_inserted`
- `memory_extraction.processed`
- `project_discovery.processed`
- `failed_items`
- `warnings[]`

Warnings are used for non-fatal operator-visible states (for example: paused runs).
