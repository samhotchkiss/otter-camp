# 342: Clean bootstrap restarts must replay canonical input, not polluted project-session context

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M |
| Spec refs | docsv2/03-projects-and-task-flow.md, docsv2/16-agent-control-plane.md |
| Depends on | 340, 341 |

## Problem

When bootstrap fails before real execution begins, the correct behavior is a clean restart. That restart must not inherit:

- bloated kickoff/project-session transcripts
- stale partial task trees
- stale wakeup/runtime ownership
- partial planning chatter from the failed bootstrap

The restart should be rooted in canonical operator input plus persisted environment bindings, not dirty bootstrap context.

## Scope

### Must build
- Define and persist the canonical bootstrap input bundle:
  - originating operator brief
  - explicit operator constraints
  - environment/repo/credential bindings
- Auto-restarts must create a fresh project/session tree from that canonical bundle.
- Failed bootstrap transcripts and partial project-session context must not be reused as restart context.
- Update relevant `docsv2` specs in the same change.

### Must NOT build
- A restart that simply resumes the failed kickoff/project session.
- A restart that reuses partial task trees or stale wakeup/runtime ownership from the failed bootstrap.

## Acceptance Criteria

- [ ] Bootstrap auto-restart uses canonical operator input, not failed project-session chatter.
- [ ] Restart creates a fresh project/session tree.
- [ ] Failed bootstrap transcripts are retained for audit, but not reused as live restart context.
- [ ] Relevant `docsv2` specs are updated in the same change.

## Tests Required

**Integration/E2E tests:**
- failed bootstrap auto-restart creates a fresh project and session tree from canonical input
- stale partial project-session context is not injected into the restarted bootstrap
- environment/repo bindings persist across the clean restart

