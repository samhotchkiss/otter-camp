---
## Summary

This spec defines how OtterCamp V2 bridges the gap between "work complete" and "value realized." It establishes two delivery patterns. **Pattern 1 (During Flow)** is the common case: the delivery action (publish a blog post, send an email, change a setting) is just another flow node inside the task -- by the time the task reaches `done`, value is already delivered. **Pattern 2 (After Merge)** is for cases where delivery depends on the integrated state of `main`, most notably code deployment. Here, multiple tasks merge independently, and a separate deploy task ships the combined result. Deploy tasks are not a new entity type; they reuse the existing task system with a deploy flow template and store deploy-specific context (commit SHA, included task IDs, target environment) in the task's `metadata` jsonb field.

Remote push is treated as infrastructure, not a task. Projects configure one or more remotes (git hosts, SSH targets, deployment platforms) with `auto` or `manual` push behavior. Auto-push fires as a merge queue hook after every merge to `main`, with 3 retries and exponential backoff on failure. For many projects, the push itself IS the deployment (triggering an external platform like Vercel or GitHub Actions). Pattern 2 offers three delivery modes: **continuous** (deploy on every merge, with implicit skip if a deploy is already running), **gated** (human triggers via chat with the PM), and **scheduled** (runs on a cadence via doc 03's scheduled tasks).

Environments are optional and project-level -- an ordered promotion path (e.g., staging then production) where each environment tracks its current deployed commit, previous commit (one-level rollback target), and which deploy task deployed it. There is no environment branching; `main` is the only long-lived branch, and environments simply track which commit of `main` they are running. Rollback is modeled as a new deploy task targeting the previous commit, not a state transition -- original tasks stay `done` and history is append-only. The schema adds two new tables (`project_remote`, `project_environment`), two columns on `project` (`delivery_mode`, `deploy_flow_template_id`), and three new event types on `project_task_event` (`push_succeeded`, `push_failed`, `deployed`). All notifications flow through the existing event bus with no new mechanisms.

---

# 03a. Shipping and Delivery

> Status: Draft
> Depends on: 03-projects-and-task-flow.md (task lifecycle, merge queue)

## Core Model

Shipping is the bridge between "work complete" and "value realized." What shipping looks like varies wildly by project:

- A development project on GitHub: push to remote, maybe deploy to production.
- A blog post: publish it, or schedule it to go live.
- A greenfield project: part of the work is figuring out what deployment even means.
- A personal project: send an email, change settings on a media server, adjust smart home devices.

There is no universal "ship" step. Instead, there are two patterns for how delivery relates to tasks:

### Pattern 1: Delivery Is Part of the Task (During Flow)

For most work, the delivery action is a flow node inside the task. The agent does the thing — publishes the blog post, sends the email, changes the settings — as part of the flow. By the time the task reaches `done`, the value is already delivered.

```
Blog post:     [Write] → [Review] → [Publish] → [Done]
Email:         [Draft] → [Review] → [Send] → [Done]
Smart home:    [Research Settings] → [Apply Changes] → [Done]
Media server:  [Update Config] → [Verify] → [Done]
```

The delivery action is just another work node. The agent needs the right tools and skills (WordPress API, email service, smart home API, etc.), but the task system doesn't care what the action is — it's work like any other.

This is the common case. Most tasks deliver their own value.

**Edge case — content delivery and merge conflicts:** For Pattern 1 tasks that deliver content from their branch (e.g., publishing a blog post from a markdown file), the delivery happens before the branch merges to `main`. If the subsequent merge conflicts on the same file, the published version could temporarily differ from what lands on `main`. In practice this is rare — it requires two tasks modifying the same content. If it occurs, the PM resolves the merge conflict to match the published state, or creates a follow-up task if correction is needed. For action-based delivery (sending emails, changing settings), this doesn't apply — there's no file to conflict on.

### Pattern 2: Delivery Is Separate from the Task (After Merge)

Sometimes delivery depends on the integrated state of `main` — the combined result of multiple tasks, not just one. Code deployment is the primary example: you deploy the codebase, not individual tasks. A single deploy might include changes from five different tasks.

In this case, the task flow handles the work: write code → review → done → merge. Delivery is a separate task that runs after merge, deploying the integrated state of `main`.

```
Task flow:     [Write Code] → [Code Review] → [Done] → merge to main
Deploy task:   [Deploy to Staging] → [Verify] → [Deploy to Prod] → [Done]
```

The deploy task is triggered by merge events and picks up all changes since the last deploy.

### Why Two Patterns

The distinction matters because it determines who owns the delivery:

- **Pattern 1**: The task's flow template includes the delivery step. The PM designs it in when creating the flow. The same agent (or a specialized agent) handles delivery as part of the normal task execution. No additional coordination needed.
- **Pattern 2**: Delivery is a project-level concern. Multiple tasks' changes are batched. A deploy task is created separately — either automatically on merge, on a schedule, or manually triggered. The PM configures this at the project level, not per-task.

Most projects use Pattern 1, Pattern 2, or both. A project might use Pattern 1 for content tasks (publish immediately) and Pattern 2 for code changes (deploy the integrated codebase).

## Remote Push

Doc 03 establishes that every project is a git repo with `main` as the source of truth. But it doesn't address remotes. Many projects need their code pushed somewhere after merge — to a git host, a server, or a deployment platform.

### Project Remote Configuration

The PM configures one or more remotes during project setup:
- **Remote type and URL**: git host (GitHub, GitLab, Gitea), SSH target (`user@server:/path/to/repo`), deployment platform (ItsAlive.co, Vercel, etc.), or any destination that accepts a git push.
- **Push behavior**: `auto` (push on every merge to `main`) or `manual` (human or PM triggers push).
- **Authentication**: credentials managed at the project or org level — SSH keys, deploy tokens, API keys, platform-specific auth.
- **Multiple remotes**: a project can push to more than one remote. A project might push to GitHub (for collaboration/visibility) and to a deployment platform (for hosting) on every merge.

### Push as a Merge Queue Hook

When `auto` push is configured, the push happens as the final step of the merge queue — after a task's branch successfully merges to `main`, the system pushes `main` to each configured remote.

**Push failure handling:** On failure, the system retries up to 3 times with exponential backoff. If all retries fail, a `push_failed` event is recorded on the originating task (see doc 03, `project_task_event`), and the PM is notified with error details. Subsequent merges still attempt to push — transient issues self-heal. If a push succeeds after prior failures, it brings the remote up to date with current `main` (covering all skipped pushes). The PM can also manually trigger a push to any remote via chat at any time.

Push is not a flow node or a task — it's infrastructure. The merge queue handles it transparently, the same way it handles the merge itself.

### Push as Deployment Trigger

For some remotes, the push IS the deployment:
- **Deployment platforms** (ItsAlive.co, Vercel, Netlify, etc.): push triggers the platform's build and deploy pipeline. OtterCamp doesn't manage the deployment — the platform does.
- **External CI/CD** (GitHub Actions, GitLab CI): push triggers the pipeline. Same idea — OtterCamp pushes, the external system takes over.
- **SSH targets**: push delivers the code to the server. Whether that constitutes deployment depends on the server's setup (bare repo with post-receive hook, etc.).

In all these cases, Pattern 2 delivery may not need a deploy task at all — the push hook handles delivery. The PM configures the remote, and merges automatically flow through to the deployment target.

For projects where OtterCamp manages deployment directly (not delegated to a platform), push and deploy are separate: push to remote is infrastructure (merge queue hook), deploy is a task (Pattern 2).

## Pattern 2 In Detail: Post-Merge Delivery

### Delivery Modes

For projects that use Pattern 2, the PM configures a delivery mode:

**Continuous:**
Every merge to `main` emits a merge event on the event bus. An event handler checks whether the project has a deploy flow template configured and whether a deploy task is currently pending or in progress. If no deploy is active, it creates a deploy task from the template. If a deploy is already running, the merge is implicitly batched — when the current deploy completes, the system checks for new merges since the deploy started and triggers a follow-up deploy if needed. This prevents redundant concurrent deploys while ensuring every change eventually ships. Appropriate for internal tools and fast-iteration projects.

**Concurrency guard.** Deploy task creation is protected by a PostgreSQL advisory lock per project. Both the merge-event handler and the post-deploy follow-up check acquire the lock before creating a deploy task, preventing duplicate deploy tasks from race conditions.

**Gated:**
Merges accumulate. The human triggers a release through chat with the PM ("let's deploy," "push to production"). The PM creates a deploy task including all changes since the last successful deployment. The PM may also proactively suggest deployment ("5 tasks have merged since the last deploy — want me to trigger a deployment?"). Appropriate for production services, client-facing work.

**Scheduled:**
Deployment runs on a cadence via a task schedule (doc 03). Each deploy task picks up all changes since the last deploy. Overlap policy `skip`. Appropriate for regular release trains.

### Deploy Flow Templates

Deploy is a task with its own flow template. Same system, no new concepts. The deploy task's work can be anything — there's no assumption that "deploy" means a technical infrastructure operation.

Examples:

```
Code to production:     [Deploy to Staging] → [Verify] → [Deploy to Prod] → [Done]
Publish a book:         [Prepare Submission] → [Submit to Kindle Store] → [Verify Listing] → [Done]
Release a package:      [Bump Version] → [Publish to npm] → [Done]
Update a website:       [Deploy] → [Done]
Human-gated:            [Deploy to Staging] → [Verify] → [Human Approval] → [Deploy to Prod] → [Done]
```

The agent doing the deploy work uses whatever tools are needed — CLI commands, API calls, browser automation, file uploads, form submissions. Publishing a book to the Kindle store means the agent navigates Amazon's submission flow. Deploying to production means the agent runs the deploy script. The task system doesn't distinguish between these — they're all just work nodes with different skills and tools.

Deploy tasks appear on the task board, have flow progression, can be blocked, can have subtasks. The PM configures the deploy flow template during project setup. Agents with the relevant skills execute the work.

**Canonical task-state rule.** Background delivery services do not write deploy or rollback tasks directly into `queued`. They create the task through the canonical task service in `draft`, then transition it through the normal state machine to `queued`. This guarantees the same validation, event emission, runtime bookkeeping, and queue wakeups as any other task in the system. If a deploy flow template is structurally invalid, delivery task creation must fail closed instead of bypassing the state machine.

### What a Deploy Task Knows

- The commit SHA being deployed (`main` HEAD when triggered).
- Which source tasks' merges are included in this deploy (determined by querying `merge_queue_entry` for all entries merged between the last successful deploy's commit and the current commit).
- The target environment(s), if the project has them.

This context is stored in the deploy task's `metadata` jsonb field at creation time:

```json
{
  "deploy": {
    "commit_sha": "abc123...",
    "included_task_ids": ["uuid-1", "uuid-2", "uuid-3"],
    "target_environment": "production"
  }
}
```

Full traceability: "Deploy OC-247 shipped changes from OC-41, OC-42, and OC-45 to production."

## Environments

Optional, project-level. Not every project has them — a blog project doesn't need staging.

When defined, environments are an ordered list representing the promotion path:

```
staging → production
```

Each environment tracks:
- Currently deployed commit SHA
- Previous commit SHA (one-level rollback target; deeper history available by querying past deploy tasks)
- Last deployment timestamp
- Which deploy task deployed it

**No environment branching.** `main` is the only long-lived branch. Environments track which commit of `main` they're running. Deployment promotes a commit through environments, not merges between branches.

## Greenfield Projects

For greenfield projects where the deployment mechanism isn't known yet:

1. Early tasks have simple flows with no delivery step. The work is research, design, scaffolding. `done` = merged.
2. When the team figures out how to deploy, the PM updates the project's delivery configuration — adds a deploy flow template, configures the delivery mode.
3. Future tasks' flow templates may include delivery nodes (Pattern 1) if appropriate, or the project uses Pattern 2 with the new deploy template.
4. The delivery configuration evolves alongside the project. The PM manages this conversationally, same as everything else.

This is natural — you don't configure CI/CD on day one of a greenfield project. You figure it out as you go.

## Notification

### Task Completion + Merge

- **Task creator**: notified that their work is merged.
- **Dependent task owners**: auto-unblocked (doc 03). Notified via existing unblock event.
- **Project activity feed**: merge event (already in UI spec).

### Delivery Completion (Pattern 1)

No additional notification needed. The delivery action was a flow node — the task's `done` notification covers it.

### Deployment Completion (Pattern 2)

- **Project activity feed**: deploy event with environment and included tasks.
- **Task creators whose work shipped**: notified their work is live in [environment].
- **PM**: aware for tracking.
- **On failure**: PM notified to triage. Inbox item if deploy flow includes human review.

All notifications through the existing event bus. No new mechanisms.

## Release Notes

For Pattern 2 deployments that warrant release notes:
- Auto-generated by the PM from included tasks (titles, descriptions, summaries).
- PM drafts as part of the deploy flow. If a review node is included, the human can edit before publish.
- No separate "release" entity. A deploy task IS a release.

For Pattern 1, release notes aren't relevant — each task delivered its own value independently.

## Acceptance

**`done` is truly done.** The task's flow — including any review and delivery nodes — is the acceptance process. There is no separate post-ship acceptance step.

If the delivered result doesn't meet expectations:
- Human tells the PM.
- PM creates a new task (bug fix, adjustment, revert).
- Original task stays `done`. History is append-only.

Consistent with doc 03: "`done` and `cancelled` are terminal."

## Post-Ship Monitoring

For Pattern 2 projects that need monitoring:

**In the deploy flow:** Include a "Verify" node after the deploy node. Agent runs health checks, smoke tests. Failure routes to rollback or blocks for human decision.

**Ongoing monitoring:** A scheduled task runs periodic checks. Monitoring agent files a new task (bug) on regression. This follows the doc 03 principle: **blocked progress creates tasks, not notifications.**

## Rollback

Rollback is a task, not a state transition.

1. PM (or human) decides to roll back.
2. System creates a rollback deploy task targeting the last known-good commit — the environment's `previous_commit_sha`. For deeper rollbacks (more than one deploy back), the PM queries past deploy tasks to identify the target commit.
3. Task runs through the deploy flow (possibly a fast-path emergency flow).
4. On success, the environment reverts to the known-good state.

The original deploy task is not modified. The original source tasks stay `done`. Rollbacks are events in the project's deployment history, not retroactive changes to task status.

## Database Schema

### Additions to `project` (doc 03)

```sql
-- Add to project table:
delivery_mode            text check (delivery_mode in ('continuous', 'gated', 'scheduled')), -- null = inherits from project_remote
deploy_flow_template_id  uuid references flow_template(id),  -- flow template for Pattern 2 deploy tasks
```

- `delivery_mode = null` means no Pattern 2 delivery configured. The project may still use Pattern 1 (delivery nodes in task flows) or push-as-deployment (remote push triggers external platform).
- `deploy_flow_template_id` references the flow template used to create deploy tasks. Same immutability rules as task flow templates (doc 03).

### project_remote

```sql
create table project_remote (
  id              uuid primary key default gen_random_uuid(),
  project_id      uuid not null references project(id),
  name            text not null,           -- human-readable label, e.g., "GitHub", "Production Server"
  remote_type     text not null check (remote_type in ('git_host', 'ssh', 'deploy_platform')),
  url             text not null,           -- git remote URL, SSH target, platform endpoint
  push_behavior   text not null default 'auto' check (push_behavior in ('auto', 'manual')),
  auth_config     jsonb,                   -- encrypted credential reference (key ID, token ID — not raw secrets)
  position        int not null default 0,  -- ordering for display and push sequence
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}',
  unique (project_id, name)
);

create index on project_remote (project_id);
```

- Multiple remotes per project. Push sequence follows `position` ordering.
- `auth_config` stores references to credential records managed at the org level, not raw secrets.

### project_environment

```sql
create table project_environment (
  id                  uuid primary key default gen_random_uuid(),
  project_id          uuid not null references project(id),
  name                text not null,           -- e.g., "staging", "production"
  position            int not null,            -- ordering: lower = earlier in promotion path
  deployed_commit_sha text,                    -- current commit deployed to this environment
  previous_commit_sha text,                    -- commit before current deploy (one-level rollback target)
  deployed_at         timestamptz,             -- when the current commit was deployed
  deploy_task_id      uuid references project_task(id),  -- which deploy task deployed it
  created_at          timestamptz not null default now(),
  updated_at          timestamptz not null default now(),
  metadata            jsonb not null default '{}',
  unique (project_id, name)
);

create index on project_environment (project_id, position);
```

- Optional. Many projects have no environments.
- `previous_commit_sha` is set to the old `deployed_commit_sha` each time a deploy completes. Provides the rollback target.
- Deeper rollback history is available by querying past deploy tasks and their `metadata.deploy.commit_sha`.

### Additions to `project_task_event` (doc 03)

Three new event types added to the `event_type` column:

- `push_succeeded` — recorded on the originating task after its merge triggers a successful push. `metadata` includes remote name and URL.
- `push_failed` — recorded on the originating task after its merge triggers a failed push. `comment` includes error details. `metadata` includes remote name, URL, and retry count.
- `deployed` — recorded on the deploy task when it successfully deploys to an environment. `metadata` includes environment name and commit SHA.

### Deploy Task Metadata Convention

Deploy tasks use the standard `project_task.metadata` jsonb field (no additional columns needed):

```json
{
  "deploy": {
    "commit_sha": "abc123def456...",
    "included_task_ids": ["uuid-1", "uuid-2", "uuid-3"],
    "target_environment": "production"
  }
}
```

Set by the event handler (continuous mode) or PM (gated/scheduled mode) at deploy task creation time. `included_task_ids` is computed by querying `merge_queue_entry` for all entries merged between the last successful deploy's commit and the deploy's target commit.

### Merge Queue Entry Retention

**Retention.** `merge_queue_entry` rows are never hard-deleted. When a deploy that includes the entry completes successfully, the entry's `archived_at` timestamp is set. Queries for active/pending entries filter to `archived_at is null`. This preserves the full merge audit trail.

### Design Notes

- **3 new tables** for the delivery domain (`project_remote`, `project_environment`, plus 2 columns on `project`). Deliberately minimal — deploy tasks reuse the existing task system, and deploy-specific data lives in metadata.
- `project_environment` tracks current state + one level of rollback. It does not store full deployment history — that's reconstructible from deploy tasks and their events.
- Push outcomes are recorded on the originating task's event log, not on a separate table. The merge queue processes the push, records the outcome, and moves on.

## Resolved Decisions

1. **Two delivery patterns.** Pattern 1: delivery is a flow node inside the task (during flow). Pattern 2: delivery is a separate deploy task triggered by merge events (after merge). Most tasks use Pattern 1.
2. **Remote push is infrastructure, not a task.** Configured at the project level. Happens as a merge queue hook. Push failure retries 3x with backoff, then notifies PM. Subsequent merges still attempt push — transient issues self-heal.
3. **Deploy tasks reuse the existing task system.** No new entities. Deploy is a task with a deploy flow template. Deploy-specific context (commit SHA, included tasks, target environment) stored in task metadata.
4. **Three delivery modes for Pattern 2**: continuous, gated, scheduled. Configured by PM.
5. **Environments are optional, ordered, project-level.** No environment branching. `main` is the only long-lived branch. Track current and previous commit SHA for one-level rollback.
6. **Greenfield projects evolve their delivery config over time.** PM updates it as the project matures.
7. **`done` is truly done.** Post-ship issues create new tasks.
8. **Rollback is a new deploy task, not a state transition.** Original tasks stay `done`. Rollback target is the environment's `previous_commit_sha`. Deeper rollbacks query past deploy tasks.
9. **Notifications through existing event bus.** No new mechanisms.
10. **Release notes auto-generated by PM.** No separate release entity.
11. **Deploy tasks created by event handlers, not by the merge queue directly.** Merge events on the event bus trigger an event handler that checks project delivery config and creates deploy tasks. Clean separation — the merge queue doesn't know about delivery.
12. **Continuous mode uses implicit skip semantics.** No new deploy task is created if one is currently pending or in progress. When a deploy completes, the system checks for new merges and triggers a follow-up if needed. Prevents redundant concurrent deploys.
13. **Push outcomes recorded as task events.** `push_succeeded` and `push_failed` are event types on `project_task_event`, recorded on the originating task whose merge triggered the push.
14. **PM is opinionated about delivery nodes (Pattern 1).** PM proposes delivery nodes for project types where delivery is standard (content → publish, email → send), consistent with doc 03's flow template design pattern. Human adjusts if needed.
15. **Deploy tasks get a visual badge on the task board** but are functionally identical to regular tasks. Subtle visual distinction for quick scanning, not a separate entity.
16. **Environment state updated by deploy task completion.** The deploy task updates `project_environment` on success. Ongoing health monitoring is a separate concern — a scheduled task periodically checks health and files bugs on regression (see Post-Ship Monitoring). External webhooks are a future enhancement.
17. **Gated deploys triggered by human via chat with PM.** PM may also proactively suggest deployment when merges accumulate.
18. **Pattern 1 content delivery edge case acknowledged.** If published content drifts from main due to a merge conflict on the same file, the PM resolves the conflict to match the published state or creates a follow-up task. Rare in practice.

## Open Questions

_None currently outstanding._
