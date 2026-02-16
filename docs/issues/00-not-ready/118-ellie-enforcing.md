# Issue #118 — Ellie: Enforcing (Commitment Tracking)

> ⚠️ **NOT READY** — Depends on #111 (Memory Infrastructure)

## Summary

Ellie's third job: making sure agents follow through on what they say they're going to do. When a commitment is made, it's captured and tracked to completion.

## Problem

Agents say "I'll do X" all the time — in conversations, issue comments, reviews. Today those commitments evaporate. Nobody checks whether the thing actually happened. The Memory Agent (#111) stores action items, but storing ≠ enforcing.

## Concept

### 1. Commitment Detection

During memory extraction, Ellie identifies commitments:
- "I'll update the spec after this"
- "Next step is to run the migration"
- "I'll send Sam the summary tonight"
- Agent accepting an assigned issue or task

Each commitment gets structured metadata: who committed, what they committed to, when (deadline if detectable), and context (source session, related issue).

### 2. Tracking

Commitments stored as a distinct memory kind with lifecycle:

| Status | Meaning |
|--------|---------|
| `open` | Commitment made, not yet fulfilled |
| `completed` | Verified done |
| `overdue` | Past expected completion, not done |
| `dropped` | Explicitly cancelled or superseded |

### 3. Verification

Ellie periodically checks open commitments against observable signals:
- Issue closed/completed that matches the commitment
- Commit message referencing the work
- Agent message confirming completion
- File changes consistent with the promise

Verification can be heuristic (LLM judges whether signals indicate completion) or explicit (agent marks it done).

### 4. Nudging & Escalation

- **Agent nudge**: Surface stale commitments in recall injection — "You said you'd do X 2 days ago"
- **Escalation**: If a commitment is overdue with no progress signals, flag it in the daily summary or notify the operator
- **Dashboard**: Commitments view showing open/overdue per agent

## Open Questions

1. How aggressive should nudging be? Every recall? Daily digest? Only when overdue?
2. Should agents be able to explicitly close/drop commitments, or only Ellie?
3. How to handle vague commitments ("I'll look into that") vs concrete ones ("I'll fix bug #42 today")?
4. Should commitment tracking feed into agent performance reviews (#104)?
5. What's the right granularity — track every "I'll" or only substantive promises?

## Dependencies

- [ ] #111 — Memory Infrastructure (extraction pipeline, memory storage, recall injection)
