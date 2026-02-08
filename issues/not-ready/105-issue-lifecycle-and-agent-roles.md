# Issue #105: Issue Lifecycle & Agent Work Roles

> ⚠️ **NOT READY FOR WORK** — This issue is still being specced. Do not begin implementation until this banner is removed.

## Summary

Formalize the issue lifecycle into a pipeline with three distinct agent roles — **Planner**, **Worker**, **Reviewer** — each picking up work automatically when it reaches their stage. Issues flow through the pipeline from human intent to deployed result with minimal human intervention.

## The Cycle

```
                          ┌─────────────────────────────┐
                          │                             │
  ┌─────────┐    ┌───────▼──────┐    ┌──────────────────────────┐
  │ CREATED  │───▶│ MARKED READY │───▶│ SPLIT INTO "SHOVEL READY" │
  └─────────┘    └──────────────┘    │ SUB-ISSUES               │
                   picked up by      └────────────┬─────────────┘
                   "Planner"                      │
                   (may call in others             │ picked up by
                    or kick back questions          │ "Worker"
                    to human)                      │
                                                   ▼
  ┌──────────────────────────┐    ┌──────────────────────────┐
  │ REVIEWED & EITHER:       │◀───│ WORK GETS DONE           │
  │ • Deployed               │    │ IN BRANCH                │
  │ • New sub-issues created │    └──────────────────────────┘
  │ • Flagged for human      │      picked up by
  │   review                 │      "Reviewer"
  └──────────────────────────┘
```

### Stage 1: Created
- Human (or agent) creates an issue with a title and description
- Issue is in `draft` state — not yet ready for work
- This is where ideas, bug reports, and feature requests land

### Stage 2: Marked Ready
- Human marks the issue as `ready` — signals it's worth working on
- This is the **intent boundary**: human has decided this should happen
- Issue now enters the Planner's queue

### Stage 3: Planner Picks Up
The **Planner** agent (a role, not a specific agent) picks up `ready` issues and:
1. **Reads the issue** — understands what's being asked
2. **Researches the codebase** — finds relevant files, existing patterns, dependencies
3. **Splits into sub-issues** — creates "shovel ready" sub-issues that are small enough for a single Worker session to complete
4. **Writes specs** — each sub-issue has clear acceptance criteria, files to modify, and test expectations
5. **Sequences work** — marks dependencies between sub-issues (e.g., "schema migration before API handler")

The Planner may:
- **Call in other agents** for input (e.g., ask the design agent about UI, ask the architect about schema)
- **Kick back to the human** with questions if the issue is ambiguous or needs decisions
- **Link related issues** if the work overlaps with something else in progress

Output: Parent issue updated with plan, child sub-issues created in `ready_for_work` state.

### Stage 4: Worker Picks Up
**Worker** agents (could be Codex, could be OpenClaw agents) pick up `ready_for_work` sub-issues:
1. **Create a branch** from main (named `issue-<number>-<slug>`)
2. **Do the work** — implement the change, write tests
3. **Commit to the branch** with issue references
4. **Mark sub-issue as `review`** — signals work is done and ready for review

Workers are ephemeral — they spin up, do one sub-issue, and move on. No long-lived state beyond the branch and commits.

### Stage 5: Reviewer Picks Up
The **Reviewer** agent picks up sub-issues in `review` state:
1. **Reads the diff** — compares branch to main
2. **Runs tests** — verifies nothing is broken
3. **Checks against the spec** — does the implementation match what the Planner wrote?
4. **Makes a decision:**
   - ✅ **Approve & merge** — merges branch to main, closes sub-issue
   - 🔄 **Request changes** — adds comments, moves back to `in_progress` for Worker to fix
   - 🆕 **Create new sub-issues** — if the review reveals additional work needed
   - 🚩 **Flag for human review** — if the change is significant/risky enough to need human eyes

When all sub-issues of a parent are closed, the parent issue is automatically closed (or moved to a "verify" state for the human).

## Roles

### Planner
- **What they do:** Decompose issues into shovel-ready sub-issues
- **Skills needed:** Codebase knowledge, architecture understanding, clear writing
- **Analogy:** Tech lead / staff engineer doing sprint planning
- **Gas Town equivalent:** Mayor + design crew
- **In our system:** Could be Frank, Josh S, or a specialized planning agent
- **Trigger:** Issue moves to `ready` state

### Worker  
- **What they do:** Implement one sub-issue at a time in a branch
- **Skills needed:** Coding, following specs precisely
- **Analogy:** Engineer heads-down on a ticket
- **Gas Town equivalent:** Polecats (ephemeral workers)
- **In our system:** Codex sessions, Derek's sub-agents, or any coding agent
- **Trigger:** Sub-issue in `ready_for_work` state with no unresolved dependencies

### Reviewer
- **What they do:** Review completed work, merge or kick back
- **Skills needed:** Code review, testing, quality judgment
- **Analogy:** Senior engineer doing PR review
- **Gas Town equivalent:** Refinery (merge queue manager)
- **In our system:** Jeremy H, or a specialized review agent
- **Trigger:** Sub-issue moves to `review` state

### Human (Overseer)
- **What they do:** Create issues, mark ready, review flagged items, make decisions
- **Not automated:** The human is the intent source and final authority
- **Notifications:** Gets pinged when Planner has questions, when Reviewer flags something

## Data Model Changes

### Sub-Issues (Parent/Child)
```sql
ALTER TABLE project_issues
  ADD COLUMN parent_issue_id UUID REFERENCES project_issues(id),
  ADD COLUMN issue_role TEXT;  -- 'planner', 'worker', 'reviewer', or NULL
```

- `parent_issue_id` — links sub-issues to their parent
- `issue_role` — which role is currently responsible for this issue

### Work Status Updates
Current states: `queued`, `in_progress`, `blocked`, `review`, `done`, `cancelled`

New states needed:
- `ready` — human has marked it ready for planning (replaces current `queued` for parent issues)
- `planning` — Planner is actively decomposing it
- `ready_for_work` — sub-issue is fully specced and ready for a Worker
- Keep `in_progress`, `review`, `done`, `cancelled` as-is
- Add `flagged` — Reviewer has flagged for human review

### Branch Tracking
```sql
ALTER TABLE project_issues
  ADD COLUMN branch_name TEXT,
  ADD COLUMN branch_merged_at TIMESTAMPTZ;
```

### Agent Role Assignment
```sql
CREATE TABLE issue_role_assignments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id UUID NOT NULL,
  project_id UUID NOT NULL,
  role TEXT NOT NULL,  -- 'planner', 'worker', 'reviewer'
  agent_id UUID,       -- specific agent, or NULL for "any available"
  priority INTEGER DEFAULT 0,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
```

## Automatic Pickup (GUPP-inspired)

The key insight from Gas Town: **if there's work on your hook, you must run it.**

For Otter Camp, this means:
1. **Polling or webhook**: Agents periodically check for issues in their role's queue (or get notified via bridge)
2. **Claim mechanism**: Agent claims an issue (sets `owner_agent_id`), preventing double-pickup
3. **Timeout/heartbeat**: If a claimed issue goes stale (no activity for X minutes), it gets released back to the queue
4. **Bridge dispatch**: The Otter Camp bridge can push work notifications to OpenClaw agents

### Queue Endpoints
```
GET /api/projects/{id}/issues/queue?role=planner    — issues in `ready` state
GET /api/projects/{id}/issues/queue?role=worker      — sub-issues in `ready_for_work` state  
GET /api/projects/{id}/issues/queue?role=reviewer    — sub-issues in `review` state
POST /api/issues/{id}/claim                          — claim an issue for work
POST /api/issues/{id}/release                        — release a claimed issue
```

## What This Changes About How We Work Today

**Today:** Sam writes detailed specs in `issues/` markdown files → Codex reads the folder and implements everything → Frank checks progress logs.

**After:** Sam writes a brief issue → marks it `ready` → Planner agent reads codebase and writes the spec (what Codex's progress-log shows it doing: breaking specs into numbered GitHub issues) → Workers implement in branches → Reviewer checks and merges → Sam gets notified of completion or questions.

The human's job shifts from **writing specs** to **creating intent and reviewing results**. The spec-writing becomes the Planner's job.

## Inspiration

### From Gas Town (Yegge)
- **Role separation**: Distinct roles with clear handoff points (Mayor → Polecats → Refinery)
- **GUPP**: Automatic work pickup — if work exists, agents must run it
- **Molecules**: Chained workflows that survive crashes (our sub-issue chains)
- **Merge Queue**: Managed merging prevents conflicts (our Reviewer role)
- **Ephemeral workers**: Polecats spin up, do work, disappear (our Workers)
- **Graceful degradation**: System works with any subset of roles active

### From Our Codex Experience
- **Spec files work**: Detailed specs in a folder → Codex implements. This IS the Planner output.
- **Sub-issue breakdown works**: Codex's progress log shows it creating numbered issues (#257-#275) from specs. This IS planning.
- **Branch-per-work works**: Codex creates PRs. This IS the Worker→Reviewer flow.
- **The bottleneck is spec writing**: Sam spent hours writing 4 specs. A Planner agent could do this.

### What's Different From Gas Town
- **Web UI, not tmux**: Otter Camp is the interface, not a terminal multiplexer
- **Not code-only**: Our pipeline handles any kind of work — code, content, design, ops. "Tests" and "deploy" are project-configurable.
- **Human-in-the-loop by default**: The human marks issues ready and reviews flagged items. Gas Town is more autonomous.
- **Simpler role model**: 3 roles + human, not 7. We can always add complexity later.
- **Deploy is configurable**: Not just "merge to main" — could be publish, upload, email, webhook, or any combination.

## Project-Level Configuration

The pipeline is universal — plan, execute, review, deploy — but what each stage *means* is defined per project.

### Role Assignments (per project)

```json
{
  "planner": { "agent": "josh-s", "fallback": "frank" },
  "worker": { "agent": "codex", "allow_swarm": true, "max_concurrent": 3 },
  "reviewer": { "agent": "jeremy-h", "fallback": "derek" }
}
```

Each project sets who fills each role. Some projects might have the same agent in multiple roles (small projects). Others might have dedicated specialists.

### Review Criteria (per project)

What "review" means depends on the work:

| Project | Review Criteria |
|---------|----------------|
| Otter Camp (code) | Tests pass, no regressions, matches spec, code style |
| Technonymous (writing) | Matches writer's voice, hits key points, hook draws you in, no AI slop |
| Agent Avatars (design) | Consistent style, correct dimensions, matches brief |
| ItsAlive (product) | Tests pass, UI matches design, accessibility |
| Pearl (infra) | Tests pass, no performance regression, security review |

The Reviewer agent gets project-specific review criteria in its prompt. This is configurable in project settings.

### Deploy Actions (per project)

What "deploy" means after Reviewer approves:

| Project | Deploy Action | Auto/Manual |
|---------|--------------|-------------|
| Otter Camp | Merge to main → Railway auto-deploys | Auto after review |
| Technonymous | Merge to main → Publish to Substack | Manual (human signs off) |
| OpenClaw | Merge to main → GitHub sync → npm publish | Manual |
| Three Stones | Merge to main → Generate PDF/ebook | Manual |
| Agent Avatars | Upload assets via SSH/API | Auto |
| Email campaign | Send via email provider | Manual (always) |

Deploy is a **configurable action** per project:

```json
{
  "deploy": {
    "auto": false,
    "require_human_signoff": true,
    "actions": [
      { "type": "merge_to_main" },
      { "type": "github_sync" },
      { "type": "webhook", "url": "https://..." },
      { "type": "ssh_upload", "host": "...", "path": "..." },
      { "type": "command", "run": "npx itsalive-co" },
      { "type": "email", "template": "..." },
      { "type": "notify_human", "message": "Ready for your review" }
    ]
  }
}
```

Deploy actions are composable — a project might merge to main AND trigger a webhook AND notify the human. The `auto` flag determines whether this happens immediately after review approval or waits for human confirmation.

### Per-Issue Overrides

Project settings are defaults. Any issue can override any of them:

```json
{
  "title": "Redesign the onboarding flow",
  "overrides": {
    "planner": "jeff-g",
    "reviewer": "sam",
    "deploy": { "auto": false, "require_human_signoff": true },
    "review_criteria": "Focus on mobile UX and accessibility"
  }
}
```

Examples of when you'd override:
- **Different reviewer**: This issue touches billing — route to Sam, not Jeremy
- **Force human signoff**: This deploys a breaking API change — no auto-deploy
- **Different planner**: This is a design issue — Jeff G plans instead of Josh
- **Custom review criteria**: "Make sure the legal language matches the template exactly"
- **Different deploy**: This one issue needs to go to staging first, not straight to prod
- **Skip review entirely**: Typo fix — Worker merges directly (if project allows)

Overrides cascade: issue overrides > project defaults > system defaults.

In the CLI:
```bash
otter issue create --project Technonymous --reviewer sam --deploy-manual "Sensitive legal update"
```

In the web UI: an "Override settings" expandable section on the issue detail page.

### What This Means

The same pipeline handles:
- **Code**: Plan architecture → implement in branch → review diff + tests → merge + deploy
- **Writing**: Plan outline + research → draft content → review voice/quality/accuracy → publish
- **Design**: Plan brief + references → create assets → review against brand/spec → upload
- **Ops**: Plan changes → implement config/infra → review safety → apply
- **Comms**: Plan message + audience → draft → review tone/accuracy → send

The human's project-level settings define the pipeline's behavior. The agents just follow the pipeline.

## Open Questions

1. **Who is the Planner?** A dedicated agent? The project lead? A specialized planning agent? Or configurable per project?
2. **Branch management**: Do we use Otter Camp's git server for branches, or the local filesystem? How do Workers get isolated workspaces?
3. **Merge conflicts**: When multiple Workers commit to different branches, who resolves conflicts? The Reviewer? A dedicated merge agent?
4. **Notification surface**: How does the human get notified? Slack DM? Otter Camp inbox? Both?
5. **Cross-project issues**: Some work spans multiple projects. How do sub-issues reference different repos?
6. **Deploy plugin system**: How extensible should deploy actions be? Hardcoded types vs. arbitrary shell commands vs. a plugin API?

## Implementation Phases

### Phase 1: Sub-Issues & States
- Add `parent_issue_id` to issues
- Add new work states (`ready`, `planning`, `ready_for_work`, `flagged`)
- CLI: `otter issue create --parent <id>` for sub-issues
- UI: Show sub-issue tree on parent issue page

### Phase 2: Role Queues
- Add queue endpoints
- Add claim/release mechanism
- Add role assignment configuration
- Bridge: push work notifications to agents

### Phase 3: Planner Agent
- Build the Planner prompt/workflow
- Planner reads codebase, creates sub-issues with specs
- Test with real issues

### Phase 4: Automated Worker Pickup
- Workers auto-claim sub-issues from queue
- Branch creation and management
- Commit tracking per sub-issue

### Phase 5: Reviewer Agent
- Build the Reviewer prompt/workflow
- Diff review, test execution, merge decisions
- Human escalation pathway

## Files to Create/Modify

- `migrations/` — sub-issue schema, new states, branch tracking, role assignments
- `internal/store/project_issue_store.go` — parent/child queries, queue queries, claim/release
- `internal/api/issues.go` — queue endpoints, claim/release handlers
- `internal/api/router.go` — new routes
- `cmd/otter/main.go` — `--parent` flag, queue commands
- `web/src/` — sub-issue tree UI, queue views, role management
- `bridge/openclaw-bridge.ts` — work notification dispatch
