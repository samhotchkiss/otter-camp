# GitClaw Product Specification

**Version:** 0.1 (Draft)  
**Author:** Frank (Chief of Staff)  
**Date:** 2026-02-03  
**Status:** Initial Specification

---

## Executive Summary

GitClaw is a work management and code hosting platform purpose-built for AI agent workflows. Unlike traditional forges (GitHub, GitLab) designed around human teams with individual accounts, GitClaw is architected around a single human operator coordinating multiple AI agents through one unified account.

**Core insight:** AI agents aren't employees. They don't need social features, notification preferences, profile pages, or separate billing. They need structured task handoffs, clear context, and a system that treats them as extensions of their human operator.

---

## Problem Statement

### The GitHub Experience

We tried to run a 12-agent operation on GitHub. Here's what broke:

1. **Account Proliferation** — Created 12 agent accounts, all suspended within 48 hours for "suspicious activity" (batch creation, automation patterns)

2. **Rate Limit Fragmentation** — Each agent hitting API independently, no coordination, easy to exhaust limits

3. **Identity Theater** — Agents don't need GitHub profiles, social graphs, or notifications. We were paying for features we'd never use.

4. **Bolted-On Dispatch** — Built a cron job to scan issues and wake agents. Fragile, polling-based, no native support.

5. **Context Loss** — Issues written for humans. No structured fields for agent context, dependencies, or handoffs.

6. **Attribution Confusion** — Who did what? Commits from `agent-derek` vs `agent-stone` — but they're all me, operationally.

### What We Actually Need

- **One account** that represents the human operator
- **Multiple agent identities** within that account (like signatures, not users)
- **Native dispatch** — tasks routed to agents without polling
- **Structured context** — issues designed for AI consumption
- **Unified rate limits** — the system knows all agents are one operation
- **Built-in memory** — context that persists across agent sessions

---

## Core Concepts

### 1. Installation

An **Installation** is one OpenClaw deployment. It has:
- One human owner
- One API key
- Multiple agents operating under that key
- Unified billing, rate limits, storage quotas

Think of it like a household: one address, multiple family members, one utility bill.

### 2. Agents

**Agents** are identities within an Installation. They're not accounts — they're more like signatures or personas.

```yaml
agent:
  id: "derek"
  display_name: "Derek"
  role: "Engineering Lead"
  avatar: "🔧"
  session_pattern: "agent:2b:*"
```

Agents can:
- Be attributed on commits, issues, comments
- Have task queues routed to them
- Have their activity tracked separately
- Share all repos, settings, and permissions (they're the same operator)

Agents cannot:
- Have separate passwords or 2FA
- Be billed separately
- Have different permission levels (all agents = full access)
- Be suspended independently

### 3. Repositories

Standard git repositories with:
- Full git protocol support (clone, push, pull, fetch)
- Branch protection rules
- Webhooks
- But simplified: no forks, no PRs across orgs, no social features

### 4. Tasks (not "Issues")

**Tasks** replace GitHub Issues with a structure designed for agent workflows:

```yaml
task:
  id: "eng-042"
  title: "Implement retry logic for 500 errors"
  
  # Who should work on this?
  assigned_agent: "derek"
  
  # Machine-readable status
  status: "queued" | "in_progress" | "blocked" | "review" | "done"
  
  # Priority for dispatch ordering
  priority: 1  # P0=0, P1=1, P2=2, P3=3
  
  # Dependencies (blocks dispatch until resolved)
  depends_on:
    - "eng-041"  # Must be 'done' first
  
  # Structured context for agents
  context:
    files:
      - "src/providers/anthropic.ts"
      - "src/core/retry.ts"
    decisions:
      - "Use exponential backoff with jitter"
      - "Max 3 retries for 500s"
    acceptance:
      - "500 errors trigger retry before failover"
      - "Retry count logged to session"
  
  # Human description (still useful)
  body: |
    When Anthropic returns HTTP 500, we currently fail over immediately.
    We should retry 2-3 times with backoff first, since 500s are often transient.
  
  # Activity log
  activity:
    - agent: "frank"
      action: "created"
      at: "2026-02-03T11:00:00Z"
    - agent: "derek"
      action: "started"
      session: "a]gent:2b:main"
      at: "2026-02-03T11:15:00Z"
```

### 5. Dispatch

Native task dispatch built into the platform:

- **Push-based**: When a task is ready, GitClaw notifies the target agent's OpenClaw session via webhook
- **Dependency-aware**: Tasks with unmet dependencies stay queued
- **Priority-ordered**: P0 before P1 before P2
- **Atomic transitions**: Status changes are transactions, no race conditions

```
Task created → queued
Dependencies met → dispatched (webhook to OpenClaw)
Agent starts → in_progress
Agent finishes → review | done
Blocked → blocked (with reason)
```

### 6. Memory Integration

Direct integration with agent memory:

- **Context injection**: When an agent receives a task, GitClaw can include recent memory excerpts
- **Auto-capture**: Significant task completions can write to agent memory
- **Cross-reference**: Memory entries link to tasks, tasks link to memory

---

## Feature Specification

### F1: Repository Management

#### F1.1: Create Repository
- Name, description, visibility (private only for v1)
- Initialize with README, .gitignore, LICENSE
- Clone URL provided immediately

#### F1.2: Git Operations
- Full git protocol over HTTPS
- SSH keys optional (one set per Installation)
- LFS support for large files
- Shallow clone support (agents often don't need full history)

#### F1.3: Branch Management
- Create, delete, list branches
- Default branch configuration
- Branch protection rules:
  - Require review before merge
  - Require status checks
  - Restrict who can push (specific agents)

#### F1.4: File Browser
- Web UI for browsing files
- Syntax highlighting
- Blame view
- History view
- Raw file download

### F2: Task Management

#### F2.1: Task CRUD
- Create with structured fields
- Update any field
- Close/reopen
- Delete (soft delete, recoverable)

#### F2.2: Task Lists
- Filter by: status, agent, priority, label, date range
- Sort by: priority, created, updated
- Saved filters (views)

#### F2.3: Task Board
- Kanban view: columns = statuses
- Drag-drop to change status
- Swimlanes by agent or priority

#### F2.4: Dependencies
- Link tasks as dependencies
- Visual dependency graph
- Circular dependency detection
- Auto-block when dependency incomplete

#### F2.5: Context Fields
- Structured `files[]` field with repo:path references
- Structured `decisions[]` field
- Structured `acceptance[]` field
- Custom fields (key-value)

#### F2.6: Activity Log
- Every change logged with agent, timestamp, session
- Comments as activity entries
- File attachments

### F3: Dispatch System

#### F3.1: Automatic Dispatch
- When task status = `queued` and dependencies met
- Webhook POST to configured OpenClaw endpoint
- Payload includes full task context

```json
{
  "event": "task.dispatch",
  "task": {
    "id": "eng-042",
    "title": "...",
    "context": {...}
  },
  "target_agent": "derek",
  "installation": "sam-openclaw"
}
```

#### F3.2: Dispatch Rules
- Per-agent webhook URLs
- Retry on failure (3x with backoff)
- Dead letter queue for failed dispatches
- Manual re-dispatch option

#### F3.3: Dispatch Dashboard
- Queue depth per agent
- Dispatch success/failure rates
- Average time in queue
- Blocked task alerts

### F4: Agent Management

#### F4.1: Agent Registry
- Define agents with ID, name, role, avatar
- Map to OpenClaw session patterns
- Webhook endpoints per agent

#### F4.2: Agent Activity
- Timeline of all agent actions
- Commits, task updates, comments
- Session tracking (which session did what)

#### F4.3: Agent Stats
- Tasks completed (by time period)
- Commits (by time period)
- Average task duration
- Block rate (how often blocked)

### F5: API

#### F5.1: REST API
- Full CRUD for repos, tasks, agents
- Pagination, filtering, sorting
- Webhook management
- Rate limited per Installation (not per agent)

#### F5.2: GraphQL API
- Flexible queries for dashboards
- Subscriptions for real-time updates
- Batch operations

#### F5.3: Git Protocol
- HTTPS clone/push/pull
- Smart HTTP protocol
- Pack negotiation

#### F5.4: Webhooks (Outbound)
- Task events (created, updated, dispatched, completed)
- Repo events (push, branch created/deleted)
- Configurable per repo or global

### F6: Web Interface

#### F6.1: Dashboard
- Installation overview
- Active tasks per agent
- Recent activity feed
- Quick actions (create task, dispatch)

#### F6.2: Repository Views
- File browser
- Commit history
- Branch list
- Settings

#### F6.3: Task Views
- List view with filters
- Board view (Kanban)
- Detail view with full context
- Dependency graph

#### F6.4: Agent Views
- Agent directory
- Individual agent profile (activity, stats)
- Session history

### F7: OpenClaw Integration

#### F7.1: Native Plugin
- OpenClaw channel plugin for GitClaw
- Receive dispatches as session events
- Update task status from agent responses

#### F7.2: CLI Integration
- `openclaw gitclaw` subcommand
- `openclaw gitclaw task list`
- `openclaw gitclaw task start <id>`
- `openclaw gitclaw task done <id>`

#### F7.3: Skill Integration
- GitClaw skill for agents
- Task management from agent sessions
- Automatic attribution

### F8: External Repository Sync

**Purpose:** Link internal AI Hub projects to external public repositories (e.g., GitHub). Agents work in the messy internal repo; when ready, work is squashed and pushed to the clean public repo under the operator's name.

#### F8.1: Link External Repository
- Configure external repo URL (GitHub, GitLab, etc.)
- Authentication: PAT or SSH key (stored encrypted)
- Branch mapping: internal branch → external branch
- One internal project can link to one external repo

#### F8.2: Sync Status
- Track internal commits since last sync
- Visual indicator: "14 internal commits → ready to sync"
- Diff preview: what changed since last external push
- Divergence detection: warn if external repo has new commits

#### F8.3: Prepare for Push
- Squash N internal commits → 1 clean commit
- Commit message editor (suggest from internal commits)
- Author attribution: always the human operator, not agents
- Preview before push: show exact diff going out
- Optional: interactive rebase for splitting into logical commits

#### F8.4: Push to External
- One-click push after approval
- Push via HTTPS or SSH
- Success/failure notification
- Record sync point (internal commit hash ↔ external commit hash)

#### F8.5: Bidirectional Awareness
- Detect changes pushed directly to external repo
- Warn/block if external has diverged
- Pull external changes into internal (optional, manual)

#### F8.6: Privacy Controls
- Internal commits, branches, issues never sync to external
- Only explicit "push" action sends code out
- Agent names stripped from commit history on export

**Key Design Principle:** The internal repo is the messy workspace where agents collaborate freely. The external repo is the clean public record showing only the human operator's curated commits. This preserves authorship while enabling agent collaboration.

---

## Data Model

### Installation
```sql
installations:
  id: uuid PK
  name: string
  owner_email: string
  api_key_hash: string
  created_at: timestamp
  settings: jsonb
```

### Agent
```sql
agents:
  id: uuid PK
  installation_id: uuid FK
  slug: string UNIQUE per installation
  display_name: string
  role: string
  avatar_url: string
  webhook_url: string
  session_pattern: string
  created_at: timestamp
```

### Repository
```sql
repositories:
  id: uuid PK
  installation_id: uuid FK
  name: string UNIQUE per installation
  description: string
  default_branch: string
  created_at: timestamp
  settings: jsonb
```

### Task
```sql
tasks:
  id: uuid PK
  installation_id: uuid FK
  repository_id: uuid FK nullable
  number: integer UNIQUE per installation
  title: string
  body: text
  status: enum
  priority: integer
  assigned_agent_id: uuid FK nullable
  context: jsonb
  labels: string[]
  created_by_agent_id: uuid FK
  created_at: timestamp
  updated_at: timestamp
  closed_at: timestamp nullable
```

### TaskDependency
```sql
task_dependencies:
  task_id: uuid FK
  depends_on_task_id: uuid FK
  PRIMARY KEY (task_id, depends_on_task_id)
```

### TaskActivity
```sql
task_activities:
  id: uuid PK
  task_id: uuid FK
  agent_id: uuid FK
  session_key: string nullable
  action: string
  details: jsonb
  created_at: timestamp
```

### Commit
```sql
commits:
  sha: string PK
  repository_id: uuid FK
  agent_id: uuid FK nullable
  message: string
  authored_at: timestamp
  parent_shas: string[]
```

---

## API Design

### Authentication

Single API key per Installation:

```
Authorization: Bearer gitclaw_sk_xxxxxxxxxxxx
```

Agent identification via header:

```
X-GitClaw-Agent: derek
```

### Endpoints (REST)

```
# Repositories
GET    /repos
POST   /repos
GET    /repos/:name
PATCH  /repos/:name
DELETE /repos/:name

# Tasks
GET    /tasks
POST   /tasks
GET    /tasks/:number
PATCH  /tasks/:number
DELETE /tasks/:number
POST   /tasks/:number/start      # Agent claims task
POST   /tasks/:number/complete   # Agent finishes task
POST   /tasks/:number/block      # Agent marks blocked

# Agents
GET    /agents
POST   /agents
GET    /agents/:slug
PATCH  /agents/:slug
DELETE /agents/:slug
GET    /agents/:slug/activity
GET    /agents/:slug/stats

# Dispatch
GET    /dispatch/queue           # View pending dispatches
POST   /dispatch/:task_number    # Manually dispatch
GET    /dispatch/history         # Dispatch log

# Git (separate endpoint)
/git/:repo.git                   # Git smart HTTP
```

### Rate Limits

Per-Installation limits (not per-agent):

- **API**: 5,000 requests/hour
- **Git operations**: 1,000/hour
- **Webhooks**: 100/minute outbound

All agents share the pool. The system knows they're coordinated.

---

## Security Model

### Authentication
- API key (bearer token) per Installation
- Keys can be rotated
- Keys can be scoped (read-only, specific repos)

### Authorization
- All agents have full access within their Installation
- No per-agent permissions (they're all the same operator)
- Repositories can be public (read) or private

### Audit
- Every API call logged with agent, session, IP
- Immutable audit log
- Export available

### Data
- All data encrypted at rest
- TLS required for all connections
- Git objects stored with encryption

---

## Deployment Options

### Hosted (GitClaw Cloud)
- Managed service at gitclaw.io
- Free tier: 1 Installation, 5 agents, 10 repos, 1GB storage
- Pro: $20/mo, unlimited agents/repos, 50GB storage
- Enterprise: Self-hosted option

### Self-Hosted
- Docker image
- Single binary option
- PostgreSQL + object storage (S3/Minio)
- Optional: Redis for caching

### Hybrid
- Self-hosted git storage (your servers)
- Cloud task management
- Best for large repos / data sovereignty

---

## OpenClaw Integration Detail

### Channel Plugin

```typescript
// gitclaw channel plugin for OpenClaw
export const gitclaw: ChannelPlugin = {
  name: 'gitclaw',
  
  // Receive dispatched tasks
  async onWebhook(payload) {
    if (payload.event === 'task.dispatch') {
      const { task, target_agent } = payload;
      
      // Route to correct agent session
      await sessions.send({
        agentId: task.target_agent,
        message: formatTaskForAgent(task)
      });
    }
  },
  
  // Agent status updates flow back
  async onAgentMessage(message, context) {
    if (message.includes('[task:done]')) {
      await gitclaw.api.tasks.complete(taskNumber);
    }
  }
}
```

### Skill for Agents

```markdown
# GitClaw Skill

## Commands

### List my tasks
gitclaw task list --mine

### Start working on a task
gitclaw task start <number>
# Marks task in_progress, records session

### Mark task complete
gitclaw task done <number> --summary "what I did"

### Block a task
gitclaw task block <number> --reason "waiting on API docs"

### Create a task for another agent
gitclaw task create --title "..." --agent derek --priority 1
```

---

## UI/UX Principles

### Designed for Operators, Not Teams

- No team management, invitations, permissions matrices
- One person's view of their operation
- Dashboard shows the whole picture at once

### Agent-Centric Views

- Default grouping by agent
- "What is Derek working on?" is one click
- Cross-agent view for the human operator

### Structured Over Freeform

- Context fields are first-class, not afterthoughts
- Dependencies are visual, not buried in text
- Status transitions are explicit actions

### Real-Time by Default

- Live updates via WebSocket
- No refresh needed
- Dispatch happens visibly

---

## Migration Path

### From GitHub

1. **Export** using `gh` CLI or API (we already have this backup)
2. **Import** via GitClaw CLI:
   ```bash
   gitclaw import github --org The-Trawl --backup ./backup.zip
   ```
3. **Map** GitHub users to GitClaw agents
4. **Verify** repos, tasks, history
5. **Update** OpenClaw config to point to GitClaw

### From Other Forges

- GitLab: Similar export/import
- Gitea: Direct DB migration possible
- Linear/Notion: Task import only (no git)

---

## Roadmap

### Phase 1: Core (MVP)
- [ ] Installation & API key management
- [ ] Agent registry
- [ ] Repository CRUD & git protocol
- [ ] Task CRUD with structured fields
- [ ] Basic web UI
- [ ] REST API
- [ ] Webhook dispatch

### Phase 2: Integration
- [ ] OpenClaw channel plugin
- [ ] OpenClaw skill
- [ ] CLI tool
- [ ] GitHub import

### Phase 3: Intelligence
- [ ] Dependency auto-detection from code
- [ ] Smart task suggestions
- [ ] Agent workload balancing
- [ ] Memory integration

### Phase 4: Scale
- [ ] Multi-Installation (agencies)
- [ ] Self-hosted option
- [ ] Advanced analytics
- [ ] Plugin system

---

## Open Questions

1. **Git hosting complexity** — Building a git server is non-trivial. Consider using Gitea/Forgejo as the git layer and building the task/dispatch layer on top?

2. **Real-time sync** — How do we handle agents working on the same files? Conflict resolution?

3. **Memory integration depth** — How tightly should GitClaw integrate with OpenClaw memory? Should it BE the memory store?

4. **Pricing model** — Per-agent? Per-repo? Per-task? Usage-based?

5. **Multi-tenant** — Should one GitClaw instance serve multiple Installations (SaaS) or is it one-per-operator?

---

## Appendix: Why Not Just Fix GitHub?

We could:
- Use one GitHub account with commit trailers for agent attribution
- Build a better dispatcher
- Accept the limitations

But:
- GitHub's model fundamentally assumes human users
- Their abuse detection will always fight automation
- We're paying for features we don't need
- We can't customize the data model

GitClaw is the tool we wish existed. Building it means we control our destiny.

---

*End of Specification*
