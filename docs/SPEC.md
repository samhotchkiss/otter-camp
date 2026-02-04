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

### F8: Ongoing Workflows

**Purpose:** Not all agent work is task-based. Some agents run continuous workflows — Nova posting to Twitter, Penny monitoring email, periodic health checks. These need first-class support.

#### F8.1: Workflow Definition
```yaml
workflow:
  id: "nova-twitter-engagement"
  name: "Twitter Engagement Cycle"
  agent: "nova"
  schedule:
    kind: "every"
    interval: "5m"
  status: "active" | "paused" | "disabled"
  last_run: timestamp
  next_run: timestamp
  config:
    # Workflow-specific settings
    feed_check: true
    max_posts_per_cycle: 3
```

#### F8.2: Workflow Types
- **Scheduled**: Run on interval (every 5m, hourly, daily)
- **Event-driven**: Triggered by external webhook (new email, mention, etc.)
- **Continuous**: Always-on with internal polling (monitoring)

#### F8.3: Workflow Dashboard
- List all workflows with status
- Last run / next run timestamps
- Error rate and recent failures
- Quick pause/resume controls
- Run history with logs

#### F8.4: Workflow vs Task
- **Tasks** are discrete units of work that complete
- **Workflows** are ongoing processes that run indefinitely
- A workflow can *create* tasks (e.g., Penny flags an important email → task for review)

---

### F9: Second Brain / Knowledge Base

**Purpose:** Centralized knowledge management for the operator and all agents. Not just task history — actual knowledge: decisions, preferences, context, learned patterns.

#### F9.1: Knowledge Entries
```yaml
entry:
  id: "kb-042"
  title: "Sam's email preferences"
  content: |
    - Never auto-reply to anything
    - Financial emails always flagged
    - Newsletter digests on Sundays
  tags: ["preferences", "email", "penny"]
  created_by: "frank"
  created_at: timestamp
  updated_at: timestamp
  references:
    - task: "ops-015"
    - workflow: "penny-email-triage"
```

#### F9.2: Knowledge Types
- **Decisions**: "We decided X because Y" — linked to tasks where decided
- **Preferences**: Operator preferences agents should follow
- **Context**: Background info (project history, relationships, etc.)
- **Procedures**: How to do recurring tasks
- **Lessons**: What we learned from mistakes

#### F9.3: Knowledge Search
- Full-text search across all entries
- Semantic search (find related knowledge)
- Filter by tag, agent, date range
- Auto-suggest relevant knowledge when creating tasks

#### F9.4: Knowledge Injection
- When task is dispatched, relevant knowledge auto-included
- Agents can query knowledge base mid-task
- Knowledge references in task context fields

#### F9.5: Knowledge Capture
- Manual entry via UI or API
- Auto-extract from task completions (agent summaries)
- Import from existing docs (markdown, notion, etc.)

---

### F10: OpenClaw Control Panel

**Purpose:** Direct control over the connected OpenClaw installation. Troubleshooting, restarts, session management — all from the Otter Camp UI.

#### F10.1: Gateway Status
- Connection status (live WebSocket indicator)
- Gateway version
- Uptime
- Current load (active sessions, queue depth)

#### F10.2: Gateway Controls
- **Restart Gateway**: Full restart with confirmation
- **Reload Config**: SIGUSR1 equivalent
- **View Logs**: Stream recent gateway logs

#### F10.3: Session Management
- List all agent sessions with status
- Per-session controls:
  - **Reset**: Clear session history, fresh start
  - **Pause**: Stop heartbeats, hold work
  - **Resume**: Restart paused session
  - **Kill**: Force terminate
- Bulk operations: Reset all, pause all

#### F10.4: Agent Health
- Heartbeat status per agent
- Error rate (last hour, day)
- Token usage
- Crash detection (repeated errors)
- Auto-recovery options

#### F10.5: Diagnostics
- Connection test to all configured services
- Token/API key validation
- Webhook delivery test
- Memory usage, disk space

---

### F11: Task Context Model

**Purpose:** Tasks must be self-contained for sub-agent handoff. When Derek receives a task, he immediately spawns a sub-agent — that sub-agent needs everything on a platter.

#### F11.1: Context Layers
```yaml
task:
  # Human-visible summary (shown in UI)
  summary: "Add retry logic for API 500 errors"
  
  # Full context (for agents, hidden from default UI)
  context:
    # What files to look at
    files:
      - path: "src/providers/anthropic.ts"
        reason: "Main provider implementation"
      - path: "src/core/retry.ts"  
        reason: "Existing retry utilities"
    
    # Decisions already made
    decisions:
      - "Use exponential backoff with jitter"
      - "Max 3 retries before failover"
      - "Log retry attempts to session"
    
    # What "done" looks like
    acceptance:
      - "500 errors trigger retry before failover"
      - "Retry count appears in session logs"
      - "Tests cover retry scenarios"
    
    # Related knowledge (auto-linked)
    knowledge:
      - "kb-015"  # Error handling standards
      - "kb-022"  # Anthropic API quirks
    
    # Dependencies context
    depends_on:
      - task: "eng-041"
        summary: "Why this blocks us"
```

#### F11.2: Context Expansion
- When dispatching, full context is assembled
- Knowledge entries are fetched and inlined
- File contents can be optionally included
- Previous task activity summarized

#### F11.3: UI Display
- **Operator view**: Summary + status + quick actions
- **Detail view**: Full context available but collapsed
- **Agent view**: Everything expanded, ready for handoff

#### F11.4: Sub-Agent Contract
A sub-agent should be able to complete the task with ONLY:
1. The task's `context` block
2. Access to the codebase
3. No conversation history required

This is enforced by design — if context is insufficient, the task shouldn't dispatch.

---

### F12: Native Applications

**Purpose:** While web is v1, native apps for Mac and iOS follow immediately. The operator should be able to monitor and intervene from anywhere.

#### F12.1: Mac App
- Menu bar presence (quick status glance)
- Native notifications for 🔴 blocked items
- Quick actions: pause all, resume, view dashboard
- Keyboard shortcuts for common operations
- Offline indicator + reconnection handling

#### F12.2: iOS App
- Dashboard view (project statuses at a glance)
- Push notifications for blocks and completions
- Quick approve/reject for review items
- Voice input for adding tasks
- Widget for home screen status

#### F12.3: Shared Features
- Real-time sync via WebSocket
- Consistent UI language (same status colors, icons)
- Deep links (open specific task from notification)
- Biometric auth for sensitive operations

#### F12.4: Priority
- Web: v1.0
- Mac app: v1.1
- iOS app: v1.2

---

### F14: Chat System

**Purpose:** Full chat capability — both DMs with agents and threaded discussions within issues. This replaces Slack as the primary agent communication channel.

#### F14.1: Unified Chat Sidebar
- **Single sidebar shows ALL chat contexts:**
  - Direct messages with agents (Derek, Nova, Stone, etc.)
  - Issue-specific discussions (eng-042, content-015, etc.)
  - Project-level channels (optional)
- Unread indicators per context
- Easy jump between DM and issue discussion
- Recent/pinned at top, full list scrollable

#### F14.2: Direct Messages
- DM any agent directly
- Messages route to that agent's OpenClaw session
- Real-time via WebSocket
- History persisted and searchable
- Can pull in additional agents (group DM)

#### F14.3: Issue Discussions
- Threaded conversation attached to any task
- @mention agents to pull them in
- Rich text, code blocks, file attachments
- Activity log vs discussion separated (what happened vs what we're talking about)

#### F14.4: Command Bar Integration
- Type "Derek" + Tab → drafts DM to Derek inline
- Type "eng-042" + Tab → opens issue discussion
- Send without leaving current view

#### F14.5: Multi-Agent Conversations
- Group chat with multiple agents on complex issues
- Clear indication of who's speaking
- Can assign "lead agent" for resolution ownership

---

### F15: Command Bar (Superhuman-style)

**Purpose:** Keyboard-driven navigation and actions. Press `/` or `⌘K` and do anything without touching the mouse.

#### F15.1: Core Behavior
- `/` or `⌘K` opens command bar from anywhere
- Type to search/filter
- Arrow keys to navigate, Enter to select
- Escape to close
- Stays open after action (quick successive commands)

#### F15.2: Navigation
- Type agent name → jump to DM ("Derek" + Enter)
- Type project name → jump to project ("Pearl" + Enter)
- Type task number → jump to task ("eng-042" + Enter)
- Fuzzy matching on all entities

#### F15.3: Quick Actions
- Type agent name + Tab → inline DM draft (don't leave current view)
- Type thought + `⌘S` → routes to Frank for processing
- "s [topic]" → routes to Stone for content memory
- Custom shortcuts user-definable via API

#### F15.4: Extensibility
- API for registering custom commands
- Shortcuts like "s" for Stone routing
- Agents can register their own quick actions
- User can customize mappings

#### F15.5: Mobile Adaptation
- Swipe-up or tap search icon
- Voice input option
- Simplified action set for touch

---

### F16: Dashboard Layout

**Purpose:** Two-column layout for wide screens. What do I need to do? What's happening?

#### F16.1: Main Column
- **Action Items**: Tasks needing your input (🔴 blocked items)
- **Your Feed**: 
  - Top card: Progress summary since last visit
  - Stream of agent updates qualifying for attention
  - Important emails, market summaries, news, etc.
  - Filterable by project/agent

#### F16.2: Secondary Column
- **Quick Add**: Otter-themed add button → input box for thoughts/tasks
- **Projects List**:
  - Project name
  - Status indicator (🔵🟢🟡🔴↺)
  - One-sentence status
  - Time since last update ("6 minutes ago")

#### F16.3: Progress Summary
- "Since you were last here..." card
- What completed
- What's blocked
- What's in progress
- Key highlights (don't make user read whole feed)

#### F16.4: Design Requirements
- Dark mode by default (toggle available)
- Draplin/Field Notes aesthetic
- Woodcut otter illustration somewhere prominent
- Fun otter fact in footer (100+ facts, rotates)
- Link to Sea Otter Foundation Trust donation

---

### F17: External Repository Sync

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
- [ ] Basic web UI (Dashboard → Projects → Tasks)
- [ ] REST API
- [ ] Webhook dispatch
- [ ] Live WebSocket connection to OpenClaw

### Phase 2: Integration
- [ ] OpenClaw channel plugin
- [ ] OpenClaw skill
- [ ] CLI tool
- [ ] GitHub import
- [ ] OpenClaw Control Panel (restart, reset sessions)
- [ ] Ongoing Workflows (scheduled/event-driven agents)

### Phase 3: Intelligence
- [ ] Second Brain / Knowledge Base
- [ ] Knowledge injection into task dispatch
- [ ] Dependency auto-detection from code
- [ ] Smart task suggestions
- [ ] Agent workload balancing

### Phase 4: Native + Scale
- [ ] Mac native app (menu bar + notifications)
- [ ] iOS app (dashboard + push notifications)
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
