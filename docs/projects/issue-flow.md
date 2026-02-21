# Projects: Task Flow

> Summary: End-to-end task lifecycle from creation through completion, review, and optional external sync.
> Last updated: 2026-02-21
> Audience: Agents orchestrating task progression.

## Work Status Lifecycle

Valid work transitions are enforced in store logic.

Typical path:
1. `queued`
2. `in_progress`
3. `review`
4. `done`

Side paths:
- `blocked` -> `in_progress`
- `on_hold` -> `in_progress` (after human response)
- `cancelled` -> `queued` (re-open flow)

## Approval Lifecycle

Typical path:
1. `draft`
2. `ready_for_review`
3. `needs_changes` or `approved_by_reviewer`
4. `approved`

## Collaboration Flow

- Participants can be added/removed.
- Comments and questionnaires support review and decisions.
- Review save/address flows create explicit checkpoints and notifications.

## Flow Templates (Enforced Handoffs)

- Tasks can be assigned a flow template and step.
- Advancing the flow follows node transitions (`next_step_key` / `reject_step_key`).
- Review nodes explicitly reject back to a prior node.
- Node actor assignment supports project manager and explicit human gates.
- See `docs/projects/flow-templates.md` for canonical behavior and suggested templates.

## Blockers and Escalation

- Sub-agent blockers are raised to project manager first.
- If unresolved, blockers escalate to human.
- Human escalation puts task `on_hold` and surfaces it in inbox.
## External Flow (When GitHub Connected)

- Manual/poll/webhook sync updates task/repo mirrors.
- Publish/sync actions are capability gated.
- Conflict resolution requires explicit user action.

## Change Log

- 2026-02-21: Added node-based transitions and blocker escalation notes.
- 2026-02-21: Renamed user-facing lifecycle terminology from issue to task.
- 2026-02-21: Added flow template handoff enforcement notes and references.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
