# Issue #7: CLI End-to-End Bugs & Missing Commands

## Context

Full E2E test of the Otter Camp CLI flow, run as an agent would. Every bug and gap documented below.

## Bugs

### 7.1 `otter issue close` fails from `queued` state
**Severity: High** — Most common operation fails on new issues.

`otter issue close` sends `work_status: "done"` but the state machine doesn't allow `queued → done`. Valid path is `queued → in_progress → review → done`.

**Fix:** `close` command should force-close regardless of current work_status. Either:
- Skip the state machine for close/cancel operations, OR
- Transition through intermediate states automatically, OR
- Add `queued → done` and `queued → cancelled` as valid transitions (simplest — closing a queued issue is normal)

**File:** `internal/store/project_issue_store.go` (`canTransitionIssueWorkStatus`)

### 7.2 `otter issue comment` requires `--author` even when authenticated
**Severity: High** — Breaks the basic flow.

```
$ otter issue comment --project "E2E Test Project" 1 "Comment text"
comment requires --author or OTTER_AGENT_ID
```

Should infer author from the authenticated session. The CLI knows the user (via `whoami`), so it should use that identity automatically.

**File:** `cmd/otter/main.go` (comment handler)

### 7.3 `otter issue comment` returns 500 even with `--author`
**Severity: High** — Comments completely broken.

```
$ otter issue comment --project "E2E Test Project" --author "Frank" 1 "text"
request failed (500): {"error":"failed to resolve issue chat target"}
```

The comment endpoint tries to resolve a "chat target" which likely depends on the project chat infrastructure being set up for the issue. Probably a missing DB row or nil pointer.

**File:** `internal/api/issues.go` or `internal/api/project_chat.go`

### 7.4 `otter issue view` shows owner UUID instead of agent name
**Severity: Medium** — Usability issue.

```
Owner: 354e85f6-0999-431c-8351-a0b4305fa24e
```

Should show: `Owner: Frank`

**File:** `cmd/otter/main.go` (view handler) — needs to resolve agent UUID to name

### 7.5 `otter issue view --json` doesn't output JSON
**Severity: Low** — The `--json` flag exists but `view` doesn't check it (or outputs non-JSON even when set).

**File:** `cmd/otter/main.go` (view handler)

### 7.6 `otter project create` parses flags wrong with `--description`
**Severity: Medium** — Description gets concatenated into the project name.

```
$ otter project create "Agent Avatars" --description "Description here"
Created project: Agent Avatars --description Description here
```

This is because `--description` must come BEFORE the positional project name (Go's `flag` package), but that's unintuitive. Either:
- Document that flags must come first, OR
- Switch to a CLI framework that handles this (cobra), OR
- Parse positional args more carefully

**File:** `cmd/otter/main.go` (project create handler)

## Missing Commands

### 7.7 `otter project list` doesn't exist
**Severity: High** — Can't list projects. Most basic operation after auth.

`otter project` only has `create`. Need: `list`, `view`, `archive`, `delete`.

### 7.8 `otter project` needs full CRUD
Missing subcommands:
- `otter project list` — list all projects (with status filter)
- `otter project view <name>` — show project details
- `otter project archive <name>` — archive a project
- `otter project delete <name>` — delete a project (with confirmation)

### 7.9 `otter clone` fails — projects have no repo_url
**Severity: High** — The core "clone a project and work in it" flow is broken.

```
$ otter clone e2e-test-project
project has no repo_url; set one first
```

`clone` requires `repo_url` to be set, but `project create` doesn't set one. The Otter Camp git server exists at `/git/` but isn't wired to project creation.

**Fix:** When a project is created, either:
- Auto-provision a git repo on the Otter Camp git server and set `repo_url` to `https://api.otter.camp/git/<org>/<project>.git`, OR
- `clone` should initialize a local repo and configure the otter remote automatically (similar to `git init` + `otter remote add`)

This is probably the single biggest blocker for the agent workflow.

## Current State Machine (for reference)

```
queued → in_progress, blocked, cancelled
in_progress → review, blocked, cancelled
blocked → in_progress, cancelled
review → in_progress, done, cancelled
done → queued (reopen)
cancelled → queued (reopen)
```

**Missing transitions:** `queued → done` (close without starting), `in_progress → done` (close without review)

## Test Results Summary

| Step | Command | Result |
|------|---------|--------|
| CLI available | `which otter` | ✅ `/usr/local/bin/otter` |
| Auth | `otter whoami` | ✅ Works |
| List projects | `otter project list` | ❌ Command doesn't exist |
| Create project | `otter project create "Name"` | ✅ Works (flag ordering fragile) |
| Clone project | `otter clone <name>` | ❌ No repo_url |
| Create issue | `otter issue create --project X "Title"` | ✅ Works |
| List issues | `otter issue list --project X` | ✅ Works |
| View issue | `otter issue view --project X 1` | ⚠️ Works but owner shows UUID |
| Comment on issue | `otter issue comment` | ❌ 500 error |
| Assign issue | `otter issue assign --project X 1 Agent` | ✅ Works |
| Close issue | `otter issue close --project X 1` | ❌ Invalid transition |
| Reopen issue | `otter issue reopen --project X 1` | ✅ Works |

## Priority

Fix in this order:
1. **7.9** — Clone flow (biggest blocker)
2. **7.7** — Project list
3. **7.1** — Close from any state
4. **7.3** — Comment 500 error
5. **7.2** — Comment auto-author
6. **7.4** — Owner name resolution
7. **7.8** — Full project CRUD
8. **7.6** — Flag parsing
9. **7.5** — JSON output

## Files to Modify

- `cmd/otter/main.go` — add project list/view/archive/delete, fix comment/close/view
- `internal/ottercli/client.go` — add ListProjects, DeleteProject, GetProject methods
- `internal/store/project_issue_store.go` — fix state machine transitions
- `internal/api/issues.go` — fix comment 500
- `internal/api/projects.go` — auto-provision git repo on create
