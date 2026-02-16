# Issue #114 — Recursive Self-Review

> STATUS: NOT READY

## Problem

Right now the review flow is linear: agent produces work → human (or reviewer agent) reviews → approve/reject/revise. The original agent ships its first draft to review without ever questioning its own output.

Eric Osiu's "recursive prompting" concept adds self-critique before anything leaves the agent. But we can go further: make the loop recursive between the agent AND the review pipeline.

## Concept

Two layers of recursive quality improvement:

### Layer 1: Agent Self-Review (Pre-Submit)

Before an agent submits work for review, it runs its own critique pass:

1. **Draft** — agent produces the work
2. **Self-Critique** — agent reviews its own output: "What's wrong with this? What would Sam reject? What's missing?"
3. **Refine** — agent improves based on self-critique
4. **Submit** — only the refined version enters the review pipeline

This is the Eric Osiu pattern. The human never sees first drafts — they see polished output that's already survived self-scrutiny.

### Layer 2: Recursive Pipeline Review (Post-Submit)

When the reviewer (Josh S, Jeremy H, or another agent) sends feedback:

1. **Feedback arrives** — reviewer says "the error handling is too generic, needs specific error types"
2. **Original agent receives feedback** — not just as a comment, but as a structured revision request
3. **Agent revises** — applies the feedback
4. **Agent self-reviews the revision** — runs Layer 1 again on the updated work
5. **Resubmit** — revised + self-reviewed work goes back to reviewer
6. **Loop until approved** — or until max iterations hit (prevent infinite loops)

The key insight: the original agent should self-review its revisions too, not just blast back a quick fix. The self-review step catches "I fixed the thing they asked about but broke something else."

### Integration with Memory (#111)

Every rejection reason feeds into the memory system:
- **Agent-level**: "Josh rejected my error handling — he wants typed errors, not string errors"
- **Team-level**: "Engineering standard: use typed errors, not string errors" (via Memory Agent scoping)
- **Self-correction loop**: Next time this agent writes error handling, the recall system injects the lesson automatically

Over time, agents need fewer review cycles because they've internalized the feedback patterns.

### Integration with Pipeline (#105)

The pipeline spec (#105) already defines plan → build → review → ship stages. Recursive self-review adds:
- A `self-review` substage before entering `review`
- A `revise` substage that loops back through `self-review` before resubmitting
- Max iteration count per issue (default: 3) to prevent infinite loops
- Iteration metadata on the issue: "Revision 2/3, addressing: error handling specificity"

## Implementation Options

### Option A: System Prompt Pattern (Simple)

Add self-review instructions to agent SOUL.md / AGENTS.md:

```markdown
## Before Submitting Work

Before you submit anything for review:
1. Re-read your output as if you're the reviewer
2. Ask yourself: "What would Sam reject? What's missing? What's sloppy?"
3. Fix those things
4. Only then submit
```

Pros: Zero infrastructure. Works today.
Cons: Agents may skip it under pressure. No enforcement. No tracking.

### Option B: Pipeline-Enforced (Robust)

OtterCamp pipeline requires a self-review step:
- Issue moves to `self-review` stage before `review`
- Agent must produce a self-critique document alongside the work
- Pipeline won't advance to `review` without both artifacts
- Self-critique is visible to the reviewer (they can see what the agent caught)

Pros: Enforced, tracked, visible.
Cons: Adds latency, more pipeline complexity.

### Option C: Hybrid (Recommended)

- Self-review is a system prompt pattern (always on, no infrastructure)
- Pipeline tracks iteration count and feedback history
- Memory system captures rejection patterns for continuous improvement
- No hard enforcement of self-review stage, but agents that skip it get caught in review more often — the memory system learns this and starts reminding them

## Open Questions

1. Should self-critique be a visible artifact (reviewer sees what the agent caught) or invisible (agent just ships better work)?
2. Max iterations before escalating to human? (Recommend: 3)
3. Should the self-review prompt be generic or customizable per agent/project?
4. How does this interact with Codex sub-agents? (They already iterate internally — do we add another layer?)
