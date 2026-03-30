## Acceptance Gates Throughout Project Execution

### Why This Matters

OtterCamp already has flow nodes and review steps, but that is not enough on its own.

Agents can produce a very large volume of work over many hours or days without actually delivering something that works. The system needs firmer acceptance checks throughout the project so work is validated against explicit expectations instead of only accumulating output and hoping review catches the problems late.

The goal is not more bureaucracy. The goal is to create clear, testable checkpoints that keep the project aligned with real outcomes.

### Core Principle

OtterCamp should define explicit acceptance gates before or alongside execution, then use those gates throughout the project lifecycle to determine whether work is actually ready to move forward.

A flow node should not only ask "was work performed?"

It should also be able to ask:

- did this pass the relevant acceptance gate?
- if not, what kind of failure is it?
- should we retry, replan, decompose differently, or escalate?

### What Acceptance Gates Are

Acceptance gates are concrete, testable statements of what "done" means at a given level of work.

They should not be vague preferences like:

- "looks good"
- "seems complete"
- "feels polished"

They should be framed as verifiable outcomes, such as:

- a user can complete a target workflow successfully
- a required artifact exists and includes specific expected content
- a system rejects invalid inputs correctly
- a page or feature behaves correctly under real end-to-end interaction
- a writing deliverable includes required sections and satisfies defined constraints
- a customer-support workflow produces the correct resolution and follow-up artifacts

### Where Gates Should Exist

Acceptance gates should exist at multiple levels, not just as one final project checklist.

Recommended levels:

- project-level acceptance gates
- milestone or phase-level acceptance gates
- task-level acceptance expectations
- review-node acceptance checks

This gives the system a way to fail earlier and more usefully, instead of letting weak work pile up for a long time.

### When Gates Should Be Created

Acceptance gates should be created early, near kickoff and planning time.

Frank / PM should derive them from:

- the project brief
- clarifying answers
- prior user context
- inferred risks and hidden failure modes

They can then be refined during decomposition so each major task or milestone inherits the right local definition of done.

The orchestrator should not invent success criteria only after work is already underway.

### What Kind Of Validation Should Count

Acceptance gates should not rely only on code-level checks.

They should include the forms of validation that actually match the product or deliverable:

- unit and integration tests when relevant
- artifact inspection
- scenario-based validation
- user-perspective end-to-end checks
- browser / UI interaction for interactive products
- content and structure checks for writing or research outputs
- operational workflow checks for support / process projects

The key idea is that validation should resemble the real way the work will be used.

### Relationship To Flow Nodes

Flow nodes remain useful, but acceptance gates should give them sharper meaning.

Examples:

- a work node should know the acceptance expectations it is trying to satisfy
- a review node should evaluate against explicit gate criteria, not just general quality
- a validation/review failure should say which gate failed and why

This makes review much less subjective and much more actionable.

### What Should Happen When A Gate Fails

A failed gate should not default to "retry the same thing again."

The system should determine whether the failure means:

- implementation bug
- poor decomposition
- missing prerequisite work
- bad assumptions
- missing context
- flawed or incomplete spec
- true need for escalation

Then it should respond accordingly:

- retry
- reject and rework
- create missing prerequisite tasks
- replan
- ask the user a targeted question
- escalate

Validation failures should be treated as planning signals, not just execution errors.

### Desired Outcome

OtterCamp should become much harder to fool with high-volume low-value output.

The process should reward:

- real progress
- passing concrete checks
- bounded, inspectable completion

And it should penalize:

- narrative-only progress
- repeated retries without passing criteria
- long-running work that has not been proven against real acceptance expectations

### Direction For Implementation

Likely next-stage process additions:

- Frank / PM generates initial acceptance gates during kickoff/spec
- decomposition carries those gates down into milestones and tasks
- review nodes reference explicit acceptance criteria
- test/validation sessions execute user-perspective checks where appropriate
- failures feed back into replanning rather than blind retry

### Summary

OtterCamp should not only move work through flows.

It should also carry explicit acceptance gates throughout the project so the system can continuously prove that the work is actually correct, useful, and ready to advance.

### Working Notes

- 2026-03-29 03:31 MDT - Triaged as a medium-term architectural extension. Current review nodes, checkpoint blockers, and validation-loop guards provide partial coverage, but acceptance gates are not yet first-class project/task state.
- Likely touchpoints: planning/decomposition metadata, review prompt assembly in `internal/turn/engine.go`, and flow state handling in `internal/flow/execution_service.go`.
- Integration plan: start by making task-level acceptance expectations explicit and reusable by review / validation lanes before widening to milestone- and project-level gates.
- Status: staged behind bounded task contract preflight and supervisory stop-condition cleanup.
- 2026-03-29 13:58 MDT - Picked up the first narrow implementation slice at the review-prompt layer. Many tasks already carry explicit `Acceptance criteria:` blocks in their description text, but review lanes were still operating mostly on generic quality language instead of surfacing those criteria directly.
- 2026-03-29 13:58 MDT - `internal/turn/engine.go` now extracts bounded task-level acceptance criteria from task descriptions and injects them into `buildTaskReviewActionPrompt(...)`. Review prompts now explicitly tell the reviewer to evaluate against those criteria and to reject while citing the failing criterion(s) when a criterion is not met. Focused turn coverage is green in `internal/turn/engine_test.go`.
- 2026-03-29 18:57 MDT - Picked up the next narrow reuse slice on the recovery side instead of introducing a new gate model. Recovery turns already reused structured acceptance criteria when a prior `flow.review_decision` had persisted them, but they still dropped explicit task acceptance criteria entirely when the lane had not reached a structured review yet.
- 2026-03-29 18:57 MDT - [`internal/turn/engine.go`](/Users/sam/dev/otter-camp/internal/turn/engine.go) now carries task-description acceptance criteria into `recoveryResumeState` and surfaces them in both `[Recovery resume state]` and the synthetic recovery action prompt when there is no prior structured review criteria set to reuse. That gives recovery lanes an explicit task-level gate even before the first durable review artifact exists.
- 2026-03-29 18:57 MDT - Focused turn coverage is green in [`internal/turn/engine_test.go`](/Users/sam/dev/otter-camp/internal/turn/engine_test.go):
  - `GOFLAGS='' go test ./internal/turn -run 'Test(BuildRecoveryResumeStateMessageIncludes(TaskAcceptanceCriteriaFallback|StructuredReviewDecisionContext)|BuildTaskReviewActionPromptIncludesPreferredDeliverableTarget)$' -count=1`
- 2026-03-29 18:57 MDT - This is still intentionally narrow: it does not create milestone/project gate state yet. It just makes task-level acceptance expectations reusable by both review and recovery lanes, which is the smallest useful bridge toward first-class acceptance gates.
