# 371: Task recovery must resume from structured checkpoints, not draft heuristics

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | L |
| Spec refs | docsv2/03-projects-and-task-flow.md, docsv2/16-agent-control-plane.md |
| Depends on | 369, 370 |

## Problem

The current recovery path often rebuilds or guesses resumable state from:

- current assistant content
- prior assistant drafts
- chat summaries
- recovery artifacts on disk
- task metadata
- historical session state

That heuristic approach kept the product moving during hard validation, but it is also why repeated placeholder-body, wrong-target-path, and intent-only recovery loops kept reappearing in slightly different forms.

Recovery should resume from a small structured checkpoint tied to the active node execution. If that checkpoint is missing, the system should fail closed and surface a product bug instead of inventing the next write target from broad historical context.

## Scope

### Must build

- Define a canonical structured recovery checkpoint for task-lane execution.
- Bind that checkpoint to the active `flow_node_execution`.
- Require recovery resume to use that checkpoint as the authoritative source for:
  - blocker class
  - target path
  - resumable action
  - durable draft/artifact reference
- Fail closed when a resumable checkpoint is missing or malformed.

### Must NOT build

- More placeholder-detection heuristics as the primary recovery strategy.
- New fallback search order rules across prior messages/drafts/summaries.
- Recovery paths that keep guessing the target file or body from historical context after the failing turn already had enough information to persist a checkpoint.

## Acceptance Criteria

- [ ] Recovery resume for a blocked task lane uses a structured checkpoint tied to the active node execution.
- [ ] Empty or malformed task writes can be repaired only from that persisted checkpoint, not from broad historical draft inference.
- [ ] Missing checkpoint state causes a deterministic product failure rather than another generic recovery loop.
- [ ] Recovery artifacts remain operator-visible, but they are no longer the canonical source of truth.

## Tests Required

**Integration tests:**
- a blocked task with a valid checkpoint resumes from that checkpoint and writes the declared target artifact
- a blocked task with missing checkpoint state fails closed with a structured product/runtime error
- stale historical drafts cannot override the checkpoint target path or draft body on later retries

## Implementer Notes

- This is the main architectural follow-on from the many live recovery fixes landed during the SAM.blog and Speaker Pipeline validation runs.
- The goal is to delete draft-shape recovery heuristics over time, not just add a stricter heuristic layer.
