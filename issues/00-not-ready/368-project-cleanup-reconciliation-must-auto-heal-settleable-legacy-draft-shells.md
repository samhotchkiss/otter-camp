# 368: Project cleanup reconciliation must auto-heal settleable legacy draft shells

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M |
| Spec refs | docsv2/03-projects-and-task-flow.md, docsv2/16-agent-control-plane.md |
| Depends on | 339 |

## Problem

The 2026-03-22 SAM.blog clean run proved that the new auto-complete logic for:

- orchestration-only parent tasks
- bootstrap planning shells

works for fresh transitions, but it did not retroactively heal already-finished projects. Older projects with completed child work or recorded planning artifacts still required a one-time operator sweep because the historical child `done` events had already been consumed before the new cleanup code shipped.

That is still too manual. If OtterCamp is upgraded while projects already contain settleable `draft` shells, the system should reconcile them automatically instead of depending on ad hoc internal runners or operator DB-level repair.

## Scope

### Must build

- Add a canonical reconciliation path that scans existing projects for:
  - orchestration-only parent tasks that now satisfy child/integration completion requirements
  - `Bootstrap:` planning tasks with enforceable playbook artifacts that can now satisfy completion requirements
- Run that reconciliation through the canonical task transition service, not direct task-row writes.
- Make the reconciliation safe to run repeatedly without changing already-settled tasks.
- Expose enough audit/event history that operators can tell which tasks were auto-healed by reconciliation.

### Must NOT build

- A direct SQL patch job that bypasses task-service validation.
- A one-off script that only fixes SAM.blog but leaves the product without a repeatable reconciliation path.
- A reconciliation pass that marks arbitrary planning tasks `done` without the same validator-backed proof required in normal runtime.

## Acceptance Criteria

- [ ] Existing projects with settleable orchestration-parent shells can auto-heal to `done` after the feature is deployed, even if their child completion events happened before the deploy.
- [ ] Existing projects with settleable bootstrap planning shells can auto-heal to `done` after deploy without manual operator runners.
- [ ] Reconciliation is idempotent and safe to rerun.
- [ ] Task/event history makes the reconciliation action explicit.

## Tests Required

**Integration tests:**
- a project with pre-existing completed child tasks and a draft orchestration parent is reconciled to `done`
- a project with pre-existing bootstrap planning artifacts and a draft bootstrap shell is reconciled to `done`
- rerunning reconciliation does not mutate already-complete tasks

## Implementer Notes

- The product learned this from the live SAM.blog run on 2026-03-22. Fresh runtime cleanup was fixed by:
  - `dede48bd`
  - `1d20cda2`
  - `952fc150`
- This issue is about upgrade-time and historical-state healing, not the fresh-transition path.
