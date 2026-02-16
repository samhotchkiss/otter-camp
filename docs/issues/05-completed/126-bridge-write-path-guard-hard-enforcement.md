# 126 - Bridge Write Path Guard Hard Enforcement

## Problem
Bridge write restrictions are currently policy-level guidance. Path validation logic exists (`isPathWithinProjectRoot`), but mutation tools are not blocked at write-time in all cases. This leaves a gap where traversal or symlink-escape write attempts may reach execution paths before enforcement.

## Goals
- Enforce write-time guardrails for mutation tools (`write`, `edit`, `apply_patch`) using OpenClaw tool-event interception.
- Reject mutation attempts in conversation mode (no `project_id`).
- Reject mutation targets outside active project root, including symlink escape paths.
- Preserve current non-mutation tool behavior.

## Out of Scope
- New product UI behavior.
- Re-architecting OpenClaw session protocol beyond required capability/event wiring.
- Re-addressing prior race-condition fix tracked separately.

## Requirements
1. Tool-event enforcement path
- Bridge must subscribe to and process OpenClaw tool events needed to inspect mutation tool calls before writes execute.
- Mutation tool calls must be evaluated against mode and path rules.

2. Conversation mode mutation block
- If the resolved session mode is conversation/no project context, block mutation tool calls.
- Error message must clearly state writes require project mode.

3. Project-mode path + symlink enforcement
- Validate all target paths for `write`, `edit`, `apply_patch` against active project root.
- Canonicalize paths and reject traversal/escape attempts.
- Re-check symlink segments before allowing mutation.

4. Tests
- Add/extend bridge tests for:
  - conversation mode mutation rejection
  - in-root mutation allow
  - traversal escape rejection
  - symlink escape rejection
  - non-mutation tool pass-through

5. Documentation
- Update bridge implementation notes/comments to describe transition from policy-level restrictions to hard enforcement.

## Full Implementation Plan (Build Order)

### Phase 1 - Event capability + ingestion
- Wire required OpenClaw client capability for tool events in bridge handshake.
- Ensure bridge receives tool events at the verbosity required for enforcement.
- Add tests proving tool events are handled.

### Phase 2 - Mutation target extraction + guard checks
- Centralize extraction of candidate file targets from mutation tool arguments.
- Reuse/extend canonical path + symlink checks for all extracted targets.
- Add targeted unit tests for extraction/validation edge cases.

### Phase 3 - Enforcement action on invalid writes
- On invalid mutation attempt, terminate/abort the active run safely and emit clear user-visible failure reason.
- Keep non-mutation tool streams unchanged.
- Add regression tests around abort path and state cleanup.

### Phase 4 - Docs + hardening regression pass
- Update comments/docs in bridge code for enforcement behavior.
- Run focused bridge regression suite.

## Initial GitHub Micro-Issue Plan (to create before coding)
- Capability/event wiring slice
- Path target extraction + validation slice
- Enforcement/abort behavior slice
- Regression/docs slice

Each issue must include explicit test commands and acceptance criteria.

## Test Plan (spec-level)
- `npm run test:bridge`
- `npx tsx --test bridge/openclaw-bridge.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts`

## Acceptance Criteria
- Mutation tools are blocked outside active project root at execution time.
- Mutation tools are blocked in conversation mode.
- Symlink escape attempts are rejected.
- Bridge tests cover these scenarios and pass.

## Execution Log
- [2026-02-11 08:45 MST] Issue #550 | Commit n/a | queued | Created spec 126 in 01-ready from GitHub follow-up issue for bridge write-path hard enforcement | Tests: n/a
- [2026-02-11 08:46 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 126 from 01-ready to 02-in-progress as active execution target | Tests: n/a
- [2026-02-11 08:49 MST] Issue #n/a | Commit e4c4d01 | branch_created | Created and switched to branch `codex/spec-126-bridge-write-path-guard` from `origin/main` for isolated implementation | Tests: n/a
- [2026-02-11 08:52 MST] Issue #742 | Commit n/a | created | Created phase-1 micro-issue for tool-event capability + ingestion wiring with explicit tests | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "tool-event"
- [2026-02-11 08:52 MST] Issue #743 | Commit n/a | created | Created phase-2 micro-issue for mutation target extraction/path validation with explicit tests | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "path guard|traversal|symlink|mutation target"
- [2026-02-11 08:52 MST] Issue #744 | Commit n/a | created | Created phase-3 micro-issue for runtime deny + run-abort enforcement with explicit tests | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "conversation mode|write guard|abort|apply_patch|edit|write"
- [2026-02-11 08:52 MST] Issue #745 | Commit n/a | created | Created phase-4 micro-issue for docs/regression update with explicit tests | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts
- [2026-02-11 09:01 MST] Issue #742 | Commit b5dad12 | closed | Wired tool-events capability into OpenClaw connect handshake and added tool-stream event ingestion/test hooks with regression tests | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "tool-event"
- [2026-02-11 09:08 MST] Issue #743 | Commit 7b4efb3 | closed | Added mutation target extraction + project-root validation helpers for write/edit/apply_patch with traversal/symlink regression coverage | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "path guard|traversal|symlink|mutation target"
- [2026-02-11 09:17 MST] Issue #744 | Commit dde114d | closed | Added runtime mutation deny/abort enforcement for conversation mode and project-root escapes with explicit deny reasons + regression tests | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "conversation mode|write guard|abort|apply_patch|edit|write"
- [2026-02-11 09:22 MST] Issue #745 | Commit 117aafe | closed | Updated execution-policy/docs text from policy-level TODOs to active hard-enforcement language and refreshed related tests | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts
- [2026-02-11 09:23 MST] Issue #550 | Commit 117aafe | closed | Closed parent follow-up issue after completing all spec-126 micro-issues and validation runs | Tests: npm run test:bridge; npx tsx --test bridge/openclaw-bridge.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts; npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "conversation mode|write guard|abort|apply_patch|edit|write"
- [2026-02-11 09:25 MST] Issue #n/a | Commit 117aafe | pr_opened | Opened PR #746 for spec 126 branch with issue closure + test evidence | Tests: n/a
- [2026-02-11 09:25 MST] Issue #n/a | Commit 117aafe | moved_to_review | Implementation complete; moved spec 126 from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
- [2026-02-11 10:18 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 126 from 01-ready back to 02-in-progress to address reviewer-required changes from PR review | Tests: n/a
- [2026-02-11 10:18 MST] Issue #751/#752 | Commit n/a | verified | Verified reviewer follow-up micro-issues #751 and #752 exist with explicit required tests before coding | Tests: n/a
- [2026-02-11 10:18 MST] Issue #n/a | Commit 117aafe | branch_reused | Switched implementation workspace back to branch `codex/spec-126-bridge-write-path-guard` for isolated reviewer-fix execution | Tests: n/a
- [2026-02-11 10:20 MST] Issue #751/#752 | Commit c527d67 | closed | Implemented reviewer follow-up hardening: null-byte path sanitization, fail-closed enforcement catch/abort, toolCallId abort propagation, and expanded mutation enforcement edge-case coverage; pushed and closed both issues | Tests: npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "null byte|fail-closed|enforcement error"; npx tsx --test bridge/openclaw-bridge.test.ts --test-name-pattern "missing target|unified diff|toolCallID"; npm run test:bridge
- [2026-02-11 10:21 MST] Issue #n/a | Commit c527d67 | reviewer_changes_resolved | Removed top-level Reviewer Required Changes block after implementing all required P1/P2/P3 items; posted reviewer-fix summary on PR #746 | Tests: n/a
- [2026-02-11 10:21 MST] Issue #n/a | Commit c527d67 | moved_to_review | Reviewer follow-up implementation complete; moved spec 126 from 02-in-progress back to 03-needs-review pending external validation | Tests: n/a
