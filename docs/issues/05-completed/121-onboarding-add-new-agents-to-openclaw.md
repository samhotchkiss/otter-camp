# Issue #121 — Onboarding: Add New Agents to OpenClaw Config

## Summary

Follow-up to #120. During `otter init`, when an existing OpenClaw instance is detected, add new required agent slots (Memory Agent, Chameleon) to `openclaw.json`.

## Rules

- **DO add** new agent slots to the agents list in `openclaw.json`:
  - **Memory Agent (Ellie)) — dedicated slot for memory extraction (#111)
  - **Chameleon** — if #110 is deployed, add the chameleon agent slot
- **DO NOT modify** existing agent config — no renaming, no changing workspaces, no altering models, no removing agents
- **DO NOT restart** OpenClaw during this step — just write the config. User can restart when ready.

## Memory Agent Config

```json
{
  "id": "memory-agent",
  "name": "Memory Agent",
  "model": "anthropic/claude-sonnet-4-20250514",
  "workspace": "~/.openclaw/workspace-memory-agent",
  "thinking": "low",
  "channels": []
}
```

- Create workspace directory if it doesn't exist
- Populate with SOUL.md and state file per #111 spec

## Implementation

During the `otter init` OpenClaw detection step (#120 Step 5):

1. Read `~/.openclaw/openclaw.json`
2. Check if `memory-agent` slot already exists — skip if so
3. Append new agent(s) to the agents list
4. Write updated config
5. Inform user: "Added Memory Agent to OpenClaw config. Restart OpenClaw when ready to activate."

## Files to Modify

- `cmd/otter/init.go` (or wherever #120 puts the init logic)
- `internal/import/openclaw.go`

## Dependencies

- #120 — Local Install & Onboarding (the init wizard this plugs into)

## Execution Log
- [2026-02-10 10:42 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Moved spec 121 from 01-ready to 02-in-progress and began execution | Tests: n/a
- [2026-02-10 10:42 MST] Issue #n/a | Commit n/a | branch-created | Created branch codex/spec-121-onboarding-openclaw-agents from origin/main for isolated implementation | Tests: n/a
- [2026-02-10 10:45 MST] Issue #643 | Commit n/a | created | Planned importer-level OpenClaw required slot insertion + memory workspace bootstrap with explicit tests | Tests: go test ./internal/import -run TestEnsureOpenClawRequiredAgents -count=1; go test ./internal/import -count=1
- [2026-02-10 10:45 MST] Issue #644 | Commit n/a | created | Planned init-flow integration and user messaging for required OpenClaw slot updates with explicit tests | Tests: go test ./cmd/otter -run TestInitAddsRequiredOpenClawAgentsToConfig -count=1; go test ./cmd/otter -run TestInitSkipsOpenClawAgentConfigUpdateWhenAlreadyPresent -count=1; go test ./cmd/otter -run TestInitImportAndBridge -count=1
- [2026-02-10 10:45 MST] Issue #643,#644 | Commit n/a | planned-set-verified | Verified full spec-121 micro-issue set exists before coding | Tests: n/a
- [2026-02-10 10:49 MST] Issue #643 | Commit a908464 | committed | Added importer helper to ensure required OpenClaw slots (Memory Agent + optional Chameleon) and memory-agent workspace bootstrap files with idempotent tests | Tests: go test ./internal/import -run TestEnsureOpenClawRequiredAgents -count=1; go test ./internal/import -count=1
- [2026-02-10 10:49 MST] Issue #643 | Commit a908464 | pushed | Pushed importer required-slot onboarding slice to branch codex/spec-121-onboarding-openclaw-agents | Tests: n/a
- [2026-02-10 10:49 MST] Issue #643 | Commit a908464 | closed | Closed GitHub issue with commit hash and importer test evidence | Tests: go test ./internal/import -run TestEnsureOpenClawRequiredAgents -count=1; go test ./internal/import -count=1
- [2026-02-10 10:51 MST] Issue #n/a | Commit n/a | branch-base-corrected | Reset spec-121 branch base to codex/spec-120-local-install-onboarding after confirming spec-121 dependency files are not present on origin/main | Tests: n/a
- [2026-02-10 10:51 MST] Issue #643 | Commit a908464 | committed | Added importer-level required OpenClaw slot ensure helper and memory workspace bootstrap with idempotent append/no-mutation tests | Tests: go test ./internal/import -run TestEnsureOpenClawRequiredAgents -count=1; go test ./internal/import -count=1
- [2026-02-10 10:51 MST] Issue #643 | Commit a908464 | pushed | Pushed issue #643 implementation to origin/codex/spec-121-onboarding-openclaw-agents | Tests: n/a
- [2026-02-10 10:51 MST] Issue #643 | Commit a908464 | closed | Closed GitHub issue with commit hash and importer test evidence | Tests: go test ./internal/import -run TestEnsureOpenClawRequiredAgents -count=1; go test ./internal/import -count=1
- [2026-02-10 10:51 MST] Issue #644 | Commit 289b5e3 | committed | Wired required OpenClaw slot ensure step into init flow with added/no-op messaging and tests | Tests: go test ./cmd/otter -run TestInitAddsRequiredOpenClawAgentsToConfig -count=1; go test ./cmd/otter -run TestInitSkipsOpenClawAgentConfigUpdateWhenAlreadyPresent -count=1; go test ./cmd/otter -run TestInitImportAndBridge -count=1
- [2026-02-10 10:51 MST] Issue #644 | Commit 289b5e3 | pushed | Pushed issue #644 init-flow integration slice to origin branch | Tests: n/a
- [2026-02-10 10:51 MST] Issue #644 | Commit 289b5e3 | closed | Closed GitHub issue with commit hash and init-flow test evidence | Tests: go test ./cmd/otter -run TestInitAddsRequiredOpenClawAgentsToConfig -count=1; go test ./cmd/otter -run TestInitSkipsOpenClawAgentConfigUpdateWhenAlreadyPresent -count=1; go test ./cmd/otter -run TestInitImportAndBridge -count=1
- [2026-02-10 10:51 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Completed all planned spec-121 micro-issues and moved spec from 02-in-progress to 03-needs-review for external validation | Tests: go test ./internal/import -count=1; go test ./cmd/otter -run TestInit -count=1
- [2026-02-10 11:31 MST] Issue #664,#665,#666 | Commit n/a | created | Opened reviewer-cycle micro-issues for stale base reconciliation, OpenClaw filesystem hardening, and init warning surfacing with explicit tests | Tests: git diff origin/main --stat; go vet ./...; go build ./...; go test ./...; go test ./internal/import -run TestWriteFileIfMissingDoesNotFollowSymlinks -count=1; go test ./internal/import -run TestEnsureMemoryAgentWorkspaceRejectsSymlink -count=1; go test ./cmd/otter -run TestInitHandlesEnsureOpenClawAgentsError -count=1
- [2026-02-10 11:34 MST] Issue #664 | Commit 497af0d | committed | Rebased spec-121 reviewer branch onto `codex/spec-120-local-install-onboarding-r2` and resolved init/openclaw test conflicts preserving both r2 hardening and spec-121 tests | Tests: n/a
- [2026-02-10 11:37 MST] Issue #665 | Commit 330cc6a | committed | Hardened memory-agent workspace bootstrap against symlink-target writes using atomic file create + post-MkdirAll Lstat verification and added symlink regression tests | Tests: go test ./internal/import -run TestWriteFileIfMissingDoesNotFollowSymlinks -count=1; go test ./internal/import -run TestEnsureMemoryAgentWorkspaceRejectsSymlink -count=1; go test ./internal/import -count=1
- [2026-02-10 11:37 MST] Issue #666 | Commit b972212 | committed | Surfaced ensure-required-agent failures as warning-level init output and added explicit non-fatal error-path test | Tests: go test ./cmd/otter -run TestInitHandlesEnsureOpenClawAgentsError -count=1; go test ./cmd/otter -run TestInitImportAndBridge -count=1
- [2026-02-10 11:39 MST] Issue #664 | Commit b972212 | validated | Full validation passed (`go vet ./...`, `go build ./...`, `go test ./...`); `git diff codex/spec-120-local-install-onboarding-r2...HEAD --stat` shows only 4 spec-121 files while `git diff origin/main...HEAD --stat` still includes spec-120-r2 files because `origin/main` does not yet contain that base | Tests: git diff origin/main...HEAD --stat; git diff codex/spec-120-local-install-onboarding-r2...HEAD --stat; go vet ./...; go build ./...; go test ./...
- [2026-02-10 11:39 MST] Issue #664,#665,#666 | Commit b972212 | blocked-push | Unable to push/close reviewer-cycle issues from this environment because `git push --force-with-lease` is policy-blocked; left spec in 02-in-progress pending external push + issue closure | Tests: n/a
- [2026-02-10 12:11 MST] Issue #664 | Commit n/a | rebased | Rebased branch codex/spec-121-onboarding-openclaw-agents-r2 onto current origin/main and re-ran full validation suite | Tests: go vet ./...; go build ./...; go test ./...
- [2026-02-10 12:11 MST] Issue #665,#666,#663 | Commit d11c9f6,a4dd6c3 | closed | Closed reviewer hardening/error-surfacing issues and closed superseded umbrella issue after pushing branch codex/spec-121-onboarding-openclaw-agents-r2 to origin | Tests: go vet ./...; go build ./...; go test ./...; go test ./internal/import -run TestWriteFileIfMissingDoesNotFollowSymlinks -count=1; go test ./internal/import -run TestEnsureMemoryAgentWorkspaceRejectsSymlink -count=1; go test ./cmd/otter -run TestInitHandlesEnsureOpenClawAgentsError -count=1
- [2026-02-10 12:11 MST] Issue #664 | Commit n/a | blocked | Left issue open: reviewer acceptance criterion requiring 4-file diff vs origin/main is currently unachievable because origin/main lacks onboarding base files (cmd/otter/init.go, cmd/otter/init_test.go, internal/import/openclaw.go, internal/import/openclaw_test.go) | Tests: git diff --stat origin/main...HEAD; git ls-tree -r --name-only origin/main | rg 'cmd/otter/init.go|internal/import/openclaw.go|cmd/otter/init_test.go|internal/import/openclaw_test.go'
- [2026-02-10 12:13 MST] Issue #663,#664,#665,#666 | Commit a4dd6c3,d11c9f6 | pr-opened | Opened draft PR #692 from codex/spec-121-onboarding-openclaw-agents-r2 to main; noted unresolved #664 base/diff blocker in PR body | Tests: go vet ./...; go build ./...; go test ./...; go test ./internal/import -run TestWriteFileIfMissingDoesNotFollowSymlinks -count=1; go test ./internal/import -run TestEnsureMemoryAgentWorkspaceRejectsSymlink -count=1; go test ./cmd/otter -run TestInitHandlesEnsureOpenClawAgentsError -count=1
- [2026-02-10 12:16 MST] Issue #664 | Commit n/a | blocked-verified | Re-validated reviewer P0 remains blocked because origin/main still lacks onboarding base files, so the 4-file diff criterion cannot be satisfied in current repo state | Tests: git diff --stat origin/main...HEAD; git cat-file -e origin/main:cmd/otter/init.go (fails); git cat-file -e origin/main:internal/import/openclaw.go (fails)
- [2026-02-10 12:24 MST] Issue #664 | Commit 1d29737 | committed | Rebuilt spec-121 reviewer slice directly on origin/main and isolated changes to required four files (`cmd/otter/init.go`, `cmd/otter/init_test.go`, `internal/import/openclaw.go`, `internal/import/openclaw_test.go`) | Tests: git diff --stat origin/main...HEAD; go vet ./...; go build ./...; go test ./...
- [2026-02-10 12:24 MST] Issue #664 | Commit 1d29737 | pushed | Pushed clean-base branch `codex/spec-121-onboarding-openclaw-agents-r2-clean` to origin because force-updating prior branch was execution-policy blocked | Tests: n/a
- [2026-02-10 12:24 MST] Issue #664 | Commit 1d29737 | pr-opened | Opened replacement PR #693 from `codex/spec-121-onboarding-openclaw-agents-r2-clean` to `main` and marked #692 superseded | Tests: git diff --stat origin/main...HEAD; go vet ./...; go build ./...; go test ./...
- [2026-02-10 12:24 MST] Issue #664 | Commit 1d29737 | closed | Closed GitHub issue with commit hash and explicit acceptance-test evidence for 4-file diff + full Go validation | Tests: git diff --stat origin/main...HEAD; go vet ./...; go build ./...; go test ./...
- [2026-02-10 12:24 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Reviewer-required changes resolved and spec moved from 02-in-progress to 03-needs-review pending external review on PR #693 | Tests: n/a
