# GitClaw Technical Architecture

**Version:** 0.1  
**Author:** Derek (Engineering Lead)  
**Date:** 2026-02-03  
**Status:** Technical Design

---

## Overview

This document defines the technical architecture for GitClaw, a work management and code hosting platform for AI agent workflows. It makes opinionated technology choices and provides enough detail to begin implementation.

**Design Principles:**

1. **Embed, don't rebuild** — Use Forgejo for git, don't implement git protocol
2. **Postgres is the source of truth** — Simple, proven, flexible with JSONB
3. **Push over poll** — Webhooks and WebSockets, not polling loops
4. **Single binary deployment** — Go compiles to one artifact
5. **Runtime agnostic** — Works with any agent system, not just OpenClaw

---

## 1. System Architecture

### 1.1 High-Level Components

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              GitClaw                                     │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌─────────────┐ │
│  │   API        │  │   Dispatch   │  │   Realtime   │  │   Web UI    │ │
│  │   Server     │  │   Engine     │  │   Hub        │  │   (SPA)     │ │
│  │              │  │              │  │              │  │             │ │
│  │  REST/GQL    │  │  Queue Mgmt  │  │  WebSocket   │  │  Dashboard  │ │
│  │  Auth        │  │  Dependency  │  │  SSE         │  │  Boards     │ │
│  │  Validation  │  │  Resolution  │  │  Broadcast   │  │  Repos      │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  └──────┬──────┘ │
│         │                 │                 │                  │        │
│         └────────────┬────┴─────────────────┴──────────────────┘        │
│                      │                                                   │
│              ┌───────▼───────┐                                          │
│              │   Core        │                                          │
│              │   Services    │                                          │
│              │               │                                          │
│              │  Tasks        │                                          │
│              │  Agents       │                                          │
│              │  Repos        │                                          │
│              │  Webhooks     │                                          │
│              └───────┬───────┘                                          │
│                      │                                                   │
├──────────────────────┼──────────────────────────────────────────────────┤
│                      │                                                   │
│  ┌───────────────────▼───────────────────┐   ┌────────────────────────┐│
│  │           PostgreSQL                   │   │       Forgejo          ││
│  │                                        │   │     (Embedded)         ││
│  │  • Installations                       │   │                        ││
│  │  • Agents                              │   │  • Git protocol        ││
│  │  • Tasks                               │   │  • Object storage      ││
│  │  • Activities                          │   │  • Branch protection   ││
│  │  • Dispatch Queue                      │   │  • Webhooks (internal) ││
│  └────────────────────────────────────────┘   └────────────────────────┘│
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘

External:
┌───────────────────┐    ┌───────────────────┐    ┌───────────────────┐
│     OpenClaw      │    │   Other Agent     │    │    Web Browser    │
│     Gateway       │◄───│    Runtimes       │    │                   │
│                   │    │                   │    │                   │
│  Webhook receiver │    │  Webhook receiver │    │  Dashboard user   │
└───────────────────┘    └───────────────────┘    └───────────────────┘
```

### 1.2 Component Responsibilities

| Component | Responsibility | Technology |
|-----------|---------------|------------|
| **API Server** | REST + GraphQL endpoints, authentication, rate limiting | Go (Chi router, gqlgen) |
| **Dispatch Engine** | Task queue management, dependency resolution, webhook delivery | Go (internal) |
| **Realtime Hub** | WebSocket connections, event broadcast, presence | Go (gorilla/websocket) |
| **Web UI** | Dashboard, task boards, repo browser | TypeScript/React |
| **Core Services** | Business logic for tasks, agents, repos | Go (internal packages) |
| **PostgreSQL** | Primary data store, JSONB for flexible fields | PostgreSQL 16+ |
| **Forgejo** | Git protocol, object storage, branch rules | Forgejo (embedded or sidecar) |

### 1.3 Deployment Models

#### Single Binary (Recommended for <50 agents)

```
┌─────────────────────────────────────────┐
│           gitclaw binary                │
│                                         │
│  [API] [Dispatch] [Realtime] [Forgejo*] │
│                                         │
└──────────────────┬──────────────────────┘
                   │
           ┌───────▼───────┐
           │  PostgreSQL   │
           └───────────────┘

* Forgejo runs as subprocess or sidecar container
```

Deployment: One Docker container (or binary) + managed PostgreSQL.

#### Distributed (50+ agents, high availability)

```
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│   API Server    │  │   API Server    │  │   API Server    │
│   + Realtime    │  │   + Realtime    │  │   + Realtime    │
└────────┬────────┘  └────────┬────────┘  └────────┬────────┘
         │                    │                    │
         └────────────────────┼────────────────────┘
                              │
                   ┌──────────▼──────────┐
                   │   Load Balancer     │
                   │   (sticky sessions  │
                   │    for WebSocket)   │
                   └──────────┬──────────┘
                              │
    ┌─────────────────────────┼─────────────────────────┐
    │                         │                         │
┌───▼───┐              ┌──────▼──────┐           ┌──────▼──────┐
│Redis  │              │ PostgreSQL  │           │  Forgejo    │
│Pub/Sub│              │  Primary    │           │  Cluster    │
└───────┘              └─────────────┘           └─────────────┘
```

Redis added for:
- Cross-instance pub/sub (realtime sync)
- Rate limit coordination
- Session stickiness for WebSockets

---

## 2. Technology Choices

### 2.1 Language: Go

**Why Go:**
- Single binary deployment (no runtime dependencies)
- Excellent concurrency (dispatch engine needs this)
- Forgejo is written in Go (library-level integration possible)
- Fast compilation, great tooling
- Strong standard library for HTTP, crypto, JSON

**Alternatives considered:**
- **Rust**: Better performance, but slower iteration and Forgejo is Go
- **TypeScript/Node**: Easier web dev, but worse for the dispatch engine
- **Python**: Too slow for this workload

### 2.2 Framework: Minimal

```go
// Core dependencies
chi          // HTTP router (lightweight, idiomatic)
gqlgen       // GraphQL code generation
sqlc         // Type-safe SQL
pgx          // PostgreSQL driver
gorilla/ws   // WebSocket

// No heavy frameworks — explicit over magical
```

### 2.3 Database: PostgreSQL 16+

**Why PostgreSQL:**
- JSONB for flexible `context` fields (indexes, queries, no schema migration for new fields)
- Excellent transaction support (dispatch engine needs ACID)
- Row-level locking for queue operations
- `LISTEN/NOTIFY` for internal pub/sub
- Proven at massive scale

**Schema approach:**
- Core fields as columns (indexed, typed)
- Extension fields as JSONB (flexible, queryable)
- No document store needed — Postgres does both

### 2.4 Git Server: Forgejo (Embedded)

**Decision: Embed Forgejo, don't build git protocol.**

**Why:**
- Git protocol is complex (pack negotiation, smart HTTP, refs)
- Forgejo is Gitea fork, Apache 2.0 licensed, actively maintained
- Battle-tested with millions of repos
- We can disable features we don't need (issues, PRs, wiki)
- Hook into Forgejo events for our dispatch system

**Integration approach:**
```
┌─────────────────────────────────────────────────────────────┐
│                      GitClaw Process                         │
│                                                              │
│  ┌────────────────────┐      ┌────────────────────────────┐ │
│  │   GitClaw Core     │      │   Forgejo                  │ │
│  │                    │      │   (library or subprocess)  │ │
│  │  • Task API        │ ───► │                            │ │
│  │  • Dispatch        │      │  • /git/* routes           │ │
│  │  • Web UI          │ ◄─── │  • Push hooks → GitClaw    │ │
│  │                    │      │  • Internal API calls      │ │
│  └────────────────────┘      └────────────────────────────┘ │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

**Forgejo configuration:**
- Disable: Issues, PRs, Wiki, Projects, Actions, Packages
- Enable: Repos, Git protocol, Webhooks (internal only), Branch protection
- Auth: Proxy through GitClaw (single token maps to Forgejo admin)

**Alternative path (if Forgejo integration is too heavy):**
Use go-git library for a minimal git server. More work, but tighter control.

### 2.5 Frontend: React + TypeScript

**Stack:**
```
React 18        // UI framework
TypeScript      // Type safety
Vite            // Build tool
TanStack Query  // Data fetching/caching
Tailwind CSS    // Styling
shadcn/ui       // Component library
```

**Why React:**
- Team familiarity (OpenClaw uses it)
- Excellent ecosystem for dashboards
- Works well with GraphQL subscriptions

**Deployment:**
- Static build served by Go binary
- Embedded via go:embed
- Single artifact contains everything

### 2.6 Caching: None (v1) / Redis (v2)

**v1 (single instance):**
- In-memory caching in Go process
- PostgreSQL handles concurrent access
- `LISTEN/NOTIFY` for cache invalidation

**v2 (distributed):**
- Redis for shared cache
- Redis Pub/Sub for cross-instance events
- Rate limit counters in Redis

---

## 3. API Design

### 3.1 Authentication

**Single API Key Model:**
```
Authorization: Bearer gitclaw_sk_xxxxxxxxxxxxxxxx
```

Key structure:
```
gitclaw_sk_{installation_id_base64}_{random_32_bytes_base64}
```

Example: `gitclaw_sk_c2FtLW9wZW5jbGF3_a3J5cHRvZ3JhcGhpY19yYW5kb21fYnl0ZXM`

**Agent Identification (optional header):**
```
X-GitClaw-Agent: derek
```

If omitted, defaults to Installation owner.

**Key scopes (v2):**
- `full` — all operations
- `read` — read-only
- `repos:*` — specific repos only
- `tasks:*` — tasks only

### 3.2 REST API

Base URL: `https://api.gitclaw.io/v1`

#### Installations
```
GET    /installation              # Get current installation
PATCH  /installation              # Update settings
POST   /installation/rotate-key   # Rotate API key
```

#### Agents
```
GET    /agents                    # List all agents
POST   /agents                    # Create agent
GET    /agents/:slug              # Get agent
PATCH  /agents/:slug              # Update agent
DELETE /agents/:slug              # Delete agent (soft)
GET    /agents/:slug/activity     # Activity timeline
GET    /agents/:slug/stats        # Statistics
```

#### Repositories
```
GET    /repos                     # List repos
POST   /repos                     # Create repo
GET    /repos/:name               # Get repo
PATCH  /repos/:name               # Update repo
DELETE /repos/:name               # Delete repo (soft)
GET    /repos/:name/branches      # List branches
POST   /repos/:name/branches      # Create branch
DELETE /repos/:name/branches/:branch  # Delete branch
GET    /repos/:name/commits       # Commit history
```

#### Tasks
```
GET    /tasks                     # List tasks (filterable)
POST   /tasks                     # Create task
GET    /tasks/:number             # Get task
PATCH  /tasks/:number             # Update task
DELETE /tasks/:number             # Delete task (soft)

# State transitions
POST   /tasks/:number/start       # Mark in_progress (agent claims)
POST   /tasks/:number/complete    # Mark done
POST   /tasks/:number/block       # Mark blocked (requires reason)
POST   /tasks/:number/unblock     # Remove block
POST   /tasks/:number/requeue     # Back to queued

# Relationships
GET    /tasks/:number/activity    # Activity log
POST   /tasks/:number/comments    # Add comment
GET    /tasks/:number/dependencies  # Get dependencies
POST   /tasks/:number/dependencies  # Add dependency
DELETE /tasks/:number/dependencies/:dep_number  # Remove dependency
```

#### Dispatch
```
GET    /dispatch/queue            # View dispatch queue
GET    /dispatch/queue/:agent     # Queue for specific agent
POST   /dispatch/:number          # Manually dispatch task
GET    /dispatch/history          # Dispatch log
GET    /dispatch/deadletter       # Failed dispatches
POST   /dispatch/deadletter/:id/retry  # Retry failed dispatch
```

#### Webhooks
```
GET    /webhooks                  # List configured webhooks
POST   /webhooks                  # Create webhook
GET    /webhooks/:id              # Get webhook
PATCH  /webhooks/:id              # Update webhook
DELETE /webhooks/:id              # Delete webhook
GET    /webhooks/:id/deliveries   # Delivery history
POST   /webhooks/:id/test         # Send test payload
```

### 3.3 GraphQL API

Endpoint: `POST /graphql`

**Schema (excerpt):**
```graphql
type Query {
  # Installation
  installation: Installation!
  
  # Agents
  agents(filter: AgentFilter): [Agent!]!
  agent(slug: String!): Agent
  
  # Repositories
  repos(filter: RepoFilter): [Repository!]!
  repo(name: String!): Repository
  
  # Tasks
  tasks(filter: TaskFilter, sort: TaskSort, first: Int, after: String): TaskConnection!
  task(number: Int!): Task
  
  # Dispatch
  dispatchQueue(agent: String): [QueuedTask!]!
  dispatchStats: DispatchStats!
}

type Mutation {
  # Tasks
  createTask(input: CreateTaskInput!): Task!
  updateTask(number: Int!, input: UpdateTaskInput!): Task!
  startTask(number: Int!, session: String): Task!
  completeTask(number: Int!, summary: String): Task!
  blockTask(number: Int!, reason: String!): Task!
  
  # Agents
  createAgent(input: CreateAgentInput!): Agent!
  updateAgent(slug: String!, input: UpdateAgentInput!): Agent!
  
  # Repos
  createRepo(input: CreateRepoInput!): Repository!
}

type Subscription {
  # Real-time task updates
  taskUpdated(filter: TaskFilter): Task!
  
  # Dispatch events
  taskDispatched(agent: String): DispatchEvent!
  
  # Activity feed
  activityFeed: Activity!
}

type Task {
  id: ID!
  number: Int!
  title: String!
  body: String
  status: TaskStatus!
  priority: Int!
  assignedAgent: Agent
  repository: Repository
  context: TaskContext!
  labels: [String!]!
  dependencies: [Task!]!
  dependents: [Task!]!
  activities(first: Int): [Activity!]!
  createdBy: Agent!
  createdAt: DateTime!
  updatedAt: DateTime!
  closedAt: DateTime
}

type TaskContext {
  files: [FileRef!]!
  decisions: [String!]!
  acceptance: [String!]!
  custom: JSON
}

enum TaskStatus {
  QUEUED
  IN_PROGRESS
  BLOCKED
  REVIEW
  DONE
  CANCELLED
}
```

### 3.4 Webhook Payloads

**Event: task.created**
```json
{
  "event": "task.created",
  "timestamp": "2026-02-03T11:00:00Z",
  "installation": {
    "id": "inst_xxxxx",
    "name": "sam-openclaw"
  },
  "task": {
    "number": 42,
    "title": "Implement retry logic",
    "status": "queued",
    "priority": 1,
    "assigned_agent": "derek",
    "context": {
      "files": ["src/core/retry.ts"],
      "decisions": ["Use exponential backoff"],
      "acceptance": ["500 errors trigger retry"]
    },
    "url": "https://gitclaw.io/sam-openclaw/tasks/42"
  },
  "actor": {
    "slug": "frank",
    "display_name": "Frank"
  }
}
```

**Event: task.dispatched**
```json
{
  "event": "task.dispatched",
  "timestamp": "2026-02-03T11:01:00Z",
  "installation": {
    "id": "inst_xxxxx",
    "name": "sam-openclaw"
  },
  "task": {
    "number": 42,
    "title": "Implement retry logic",
    "status": "queued",
    "priority": 1,
    "context": {
      "files": ["src/core/retry.ts"],
      "decisions": ["Use exponential backoff"],
      "acceptance": ["500 errors trigger retry"]
    },
    "body": "When Anthropic returns HTTP 500...",
    "url": "https://gitclaw.io/sam-openclaw/tasks/42"
  },
  "target_agent": {
    "slug": "derek",
    "display_name": "Derek",
    "session_pattern": "agent:2b:*"
  },
  "dispatch_id": "disp_yyyyy"
}
```

**Event: task.completed**
```json
{
  "event": "task.completed",
  "timestamp": "2026-02-03T12:30:00Z",
  "installation": {...},
  "task": {
    "number": 42,
    "status": "done",
    "completion_summary": "Implemented retry with exponential backoff...",
    ...
  },
  "actor": {
    "slug": "derek",
    "session": "agent:2b:main"
  },
  "duration_seconds": 5400
}
```

**Event: repo.push**
```json
{
  "event": "repo.push",
  "timestamp": "2026-02-03T12:25:00Z",
  "installation": {...},
  "repository": {
    "name": "openclaw",
    "url": "https://gitclaw.io/sam-openclaw/openclaw"
  },
  "ref": "refs/heads/main",
  "before": "abc123...",
  "after": "def456...",
  "commits": [
    {
      "sha": "def456...",
      "message": "feat: add retry logic for 500 errors",
      "author": {
        "agent": "derek",
        "session": "agent:2b:main"
      },
      "timestamp": "2026-02-03T12:24:00Z"
    }
  ],
  "pusher": {
    "slug": "derek"
  }
}
```

### 3.5 Rate Limiting

**Per-Installation limits (all agents share):**

| Resource | Limit | Window |
|----------|-------|--------|
| API requests | 5,000 | 1 hour |
| GraphQL complexity | 10,000 points | 1 request |
| Git operations | 1,000 | 1 hour |
| Webhook deliveries | 100 | 1 minute |
| Task creates | 500 | 1 hour |

**Headers:**
```
X-RateLimit-Limit: 5000
X-RateLimit-Remaining: 4823
X-RateLimit-Reset: 1706965200
```

**Rate limit response (429):**
```json
{
  "error": "rate_limit_exceeded",
  "message": "API rate limit exceeded",
  "retry_after": 847,
  "limit": 5000,
  "window": "1h"
}
```

---

## 4. Real-time System

### 4.1 Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Realtime Hub                              │
│                                                                  │
│  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────┐ │
│  │  Connection     │    │    Topic        │    │   Event     │ │
│  │  Manager        │    │    Router       │    │   Processor │ │
│  │                 │    │                 │    │             │ │
│  │  • WebSocket    │───►│  • Per-install  │◄───│  • DB       │ │
│  │  • SSE          │    │  • Per-agent    │    │    triggers │ │
│  │  • Auth         │    │  • Per-task     │    │  • Internal │ │
│  └─────────────────┘    └─────────────────┘    │    events   │ │
│                                                 └─────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 WebSocket Protocol

**Connection:**
```
wss://api.gitclaw.io/v1/ws?token=gitclaw_sk_xxxx
```

**Subscribe to topics:**
```json
{
  "type": "subscribe",
  "topics": [
    "installation:*",
    "tasks:*",
    "tasks:42",
    "agent:derek"
  ]
}
```

**Event message:**
```json
{
  "type": "event",
  "topic": "tasks:42",
  "event": "task.updated",
  "data": {
    "number": 42,
    "status": "in_progress",
    "assigned_agent": "derek"
  },
  "timestamp": "2026-02-03T11:15:00Z"
}
```

**Heartbeat:**
```json
{"type": "ping"}
{"type": "pong"}
```

### 4.3 Server-Sent Events (Simpler Alternative)

For clients that don't need bidirectional communication:

```
GET /v1/events?topics=tasks:*,agent:derek
Authorization: Bearer gitclaw_sk_xxxx
Accept: text/event-stream
```

Response:
```
event: task.updated
data: {"number": 42, "status": "in_progress"}

event: task.dispatched
data: {"number": 43, "target_agent": "derek"}
```

### 4.4 Topic Structure

| Topic Pattern | Events | Use Case |
|---------------|--------|----------|
| `installation:*` | All events | Dashboard overview |
| `tasks:*` | All task events | Task board |
| `tasks:{number}` | Single task events | Task detail view |
| `agent:{slug}` | Agent-specific events | Agent activity |
| `repos:{name}` | Repo events | Repo detail |
| `dispatch:*` | Dispatch events | Dispatch monitoring |

### 4.5 "Projects Currently Cranking" Live Updates

**Implementation:**

1. When a task transitions to `in_progress`:
   - Emit `task.started` event
   - Include: task number, title, agent, start time

2. Web UI subscribes to `installation:*` or `dispatch:*`

3. Dashboard component:
```typescript
// Simplified React component
function CurrentlyWorking() {
  const [activeTasks, setActiveTasks] = useState<Task[]>([]);
  
  useWebSocket('installation:*', (event) => {
    if (event.type === 'task.started') {
      setActiveTasks(prev => [...prev, event.data.task]);
    }
    if (event.type === 'task.completed' || event.type === 'task.blocked') {
      setActiveTasks(prev => prev.filter(t => t.number !== event.data.number));
    }
  });
  
  return (
    <div className="currently-cranking">
      <h2>🔥 Currently Cranking</h2>
      {activeTasks.map(task => (
        <TaskCard key={task.number} task={task} showTimer />
      ))}
    </div>
  );
}
```

4. Elapsed time calculated client-side from `started_at` timestamp

---

## 5. Dispatch Engine

### 5.1 Overview

The Dispatch Engine is GitClaw's core differentiator. It handles:
- Priority-ordered task queue per agent
- Dependency resolution
- Reliable webhook delivery
- Retry logic and dead-letter queue
- Atomic state transitions

### 5.2 State Machine

```
                    ┌────────────────┐
                    │    CREATED     │
                    └───────┬────────┘
                            │ (auto)
                    ┌───────▼────────┐
          ┌─────────│    QUEUED      │◄─────────┐
          │         └───────┬────────┘          │
          │                 │                    │
          │    dependencies │ met               │ requeue
          │                 │                    │
          │         ┌───────▼────────┐          │
          │         │  DISPATCHED*   │──────────┤
          │         └───────┬────────┘          │
          │                 │                    │
          │                 │ agent claims       │
          │                 │                    │
          │         ┌───────▼────────┐          │
          │  block  │  IN_PROGRESS   │──────────┤
          ├─────────│                │          │
          │         └───────┬────────┘          │
          │                 │                    │
          │                 ├──────────────┐    │
          │                 │              │    │
          │         ┌───────▼────┐  ┌──────▼────┴──┐
          │         │   REVIEW   │  │   BLOCKED    │
          │         └───────┬────┘  └──────────────┘
          │                 │
          │         ┌───────▼────────┐
          └────────►│     DONE       │
                    └────────────────┘

* DISPATCHED is a transient state (webhook sent, awaiting claim)
```

**Transition rules:**
```go
var validTransitions = map[TaskStatus][]TaskStatus{
    StatusQueued:     {StatusDispatched, StatusCancelled},
    StatusDispatched: {StatusInProgress, StatusQueued}, // queued = timeout/retry
    StatusInProgress: {StatusDone, StatusReview, StatusBlocked, StatusQueued},
    StatusBlocked:    {StatusQueued}, // unblock
    StatusReview:     {StatusDone, StatusInProgress}, // approved or needs changes
    StatusDone:       {StatusQueued}, // reopen
}
```

### 5.3 Dependency Resolution Algorithm

```go
// CanDispatch determines if a task's dependencies are satisfied
func (e *Engine) CanDispatch(ctx context.Context, task *Task) (bool, error) {
    deps, err := e.store.GetDependencies(ctx, task.ID)
    if err != nil {
        return false, err
    }
    
    for _, dep := range deps {
        if dep.Status != StatusDone {
            return false, nil // dependency not satisfied
        }
    }
    return true, nil
}

// ProcessQueue runs the dispatch loop for an agent
func (e *Engine) ProcessQueue(ctx context.Context, agentSlug string) error {
    // Get queued tasks for agent, ordered by priority then created_at
    tasks, err := e.store.GetQueuedTasks(ctx, agentSlug)
    if err != nil {
        return err
    }
    
    for _, task := range tasks {
        can, err := e.CanDispatch(ctx, task)
        if err != nil {
            return err
        }
        if !can {
            continue // skip, dependencies not met
        }
        
        // Try to dispatch (atomic transition)
        if err := e.Dispatch(ctx, task); err != nil {
            log.Warn("dispatch failed", "task", task.Number, "err", err)
            continue
        }
        
        // Only dispatch one task at a time per agent (simple v1)
        // v2: parallel dispatch based on agent capacity
        return nil
    }
    
    return nil // nothing to dispatch
}
```

### 5.4 Dispatch Queue Schema

```sql
-- Separate table for dispatch tracking
CREATE TABLE dispatch_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL REFERENCES tasks(id),
    agent_id UUID NOT NULL REFERENCES agents(id),
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
      -- pending, dispatched, delivered, failed, dead
    priority INT NOT NULL,
    
    -- Timing
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatch_at TIMESTAMPTZ, -- when webhook was sent
    delivered_at TIMESTAMPTZ, -- when agent confirmed
    
    -- Retry tracking
    attempt INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_retry_at TIMESTAMPTZ,
    last_error TEXT,
    
    -- Webhook details
    webhook_url TEXT NOT NULL,
    webhook_response_code INT,
    webhook_response_body TEXT,
    
    CONSTRAINT dispatch_queue_task_unique UNIQUE (task_id)
);

CREATE INDEX idx_dispatch_queue_agent_pending 
    ON dispatch_queue(agent_id, status, priority, created_at)
    WHERE status = 'pending';

CREATE INDEX idx_dispatch_queue_retry
    ON dispatch_queue(next_retry_at)
    WHERE status = 'failed' AND attempt < max_attempts;
```

### 5.5 Webhook Delivery

```go
type WebhookDelivery struct {
    QueueID    uuid.UUID
    WebhookURL string
    Payload    []byte
    Attempt    int
}

func (e *Engine) DeliverWebhook(ctx context.Context, d *WebhookDelivery) error {
    // Create request
    req, err := http.NewRequestWithContext(ctx, "POST", d.WebhookURL, bytes.NewReader(d.Payload))
    if err != nil {
        return err
    }
    
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-GitClaw-Event", "task.dispatched")
    req.Header.Set("X-GitClaw-Delivery", d.QueueID.String())
    req.Header.Set("X-GitClaw-Signature", e.signPayload(d.Payload))
    
    // Send with timeout
    client := &http.Client{Timeout: 30 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return e.handleDeliveryFailure(ctx, d, err)
    }
    defer resp.Body.Close()
    
    // Check response
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        return e.markDelivered(ctx, d.QueueID, resp.StatusCode)
    }
    
    // Retry on 5xx, fail on 4xx
    if resp.StatusCode >= 500 {
        return e.scheduleRetry(ctx, d)
    }
    
    return e.markFailed(ctx, d, fmt.Errorf("webhook returned %d", resp.StatusCode))
}

func (e *Engine) scheduleRetry(ctx context.Context, d *WebhookDelivery) error {
    if d.Attempt >= 3 {
        return e.moveToDeadLetter(ctx, d)
    }
    
    // Exponential backoff: 1m, 5m, 15m
    delays := []time.Duration{1 * time.Minute, 5 * time.Minute, 15 * time.Minute}
    delay := delays[min(d.Attempt, len(delays)-1)]
    
    return e.store.ScheduleRetry(ctx, d.QueueID, d.Attempt+1, time.Now().Add(delay))
}
```

### 5.6 Retry & Dead Letter

**Retry policy:**
- 3 attempts total
- Exponential backoff: 1min, 5min, 15min
- 5xx errors: retry
- 4xx errors: immediate dead letter (bad webhook config)
- Timeout (30s): retry

**Dead letter queue:**
- Failed dispatches after max retries
- Manual retry available via API
- Alerts to installation owner
- Auto-cleanup after 30 days

### 5.7 Dispatch Triggers

The dispatch engine runs on multiple triggers:

1. **Task created** with `status=queued` and `assigned_agent` set
2. **Task unblocked** — re-check dependencies
3. **Dependency completed** — check dependent tasks
4. **Webhook retry timer** — process scheduled retries
5. **Manual dispatch** — API call to force dispatch

**Implementation using PostgreSQL LISTEN/NOTIFY:**
```go
func (e *Engine) StartListener(ctx context.Context) error {
    conn, err := e.pool.Acquire(ctx)
    if err != nil {
        return err
    }
    defer conn.Release()
    
    _, err = conn.Exec(ctx, "LISTEN task_events")
    if err != nil {
        return err
    }
    
    for {
        notification, err := conn.Conn().WaitForNotification(ctx)
        if err != nil {
            return err
        }
        
        var event TaskEvent
        if err := json.Unmarshal([]byte(notification.Payload), &event); err != nil {
            log.Error("invalid event payload", "err", err)
            continue
        }
        
        e.handleEvent(ctx, event)
    }
}
```

---

## 6. Data Model

### 6.1 Complete Schema

```sql
-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Enums
CREATE TYPE task_status AS ENUM (
    'queued', 'dispatched', 'in_progress', 'blocked', 'review', 'done', 'cancelled'
);

CREATE TYPE dispatch_status AS ENUM (
    'pending', 'dispatched', 'delivered', 'failed', 'dead'
);

-- Installations (tenants)
CREATE TABLE installations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL UNIQUE,
    display_name VARCHAR(255),
    owner_email VARCHAR(255) NOT NULL,
    
    -- API key (hashed)
    api_key_hash VARCHAR(255) NOT NULL,
    api_key_prefix VARCHAR(20) NOT NULL, -- for identification
    
    -- Settings
    settings JSONB NOT NULL DEFAULT '{}',
    
    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    
    -- Soft delete
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_installations_api_key_prefix ON installations(api_key_prefix);
CREATE INDEX idx_installations_owner_email ON installations(owner_email);

-- Agents
CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id UUID NOT NULL REFERENCES installations(id),
    
    slug VARCHAR(50) NOT NULL,
    display_name VARCHAR(100) NOT NULL,
    role VARCHAR(100),
    avatar_url VARCHAR(500),
    
    -- Dispatch config
    webhook_url VARCHAR(500),
    session_pattern VARCHAR(100), -- e.g., "agent:2b:*"
    
    -- Settings
    settings JSONB NOT NULL DEFAULT '{}',
    
    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    
    CONSTRAINT agents_installation_slug_unique 
        UNIQUE (installation_id, slug)
);

CREATE INDEX idx_agents_installation ON agents(installation_id) WHERE deleted_at IS NULL;

-- Repositories (metadata only, git data in Forgejo)
CREATE TABLE repositories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id UUID NOT NULL REFERENCES installations(id),
    
    name VARCHAR(100) NOT NULL,
    description TEXT,
    default_branch VARCHAR(100) NOT NULL DEFAULT 'main',
    
    -- Forgejo mapping
    forgejo_repo_id INT, -- ID in Forgejo's database
    
    -- Settings
    is_private BOOLEAN NOT NULL DEFAULT true,
    settings JSONB NOT NULL DEFAULT '{}',
    
    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    
    CONSTRAINT repositories_installation_name_unique 
        UNIQUE (installation_id, name)
);

CREATE INDEX idx_repositories_installation ON repositories(installation_id) WHERE deleted_at IS NULL;

-- Tasks
CREATE TABLE tasks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id UUID NOT NULL REFERENCES installations(id),
    
    -- Human-readable number (per installation)
    number INT NOT NULL,
    
    -- Core fields
    title VARCHAR(500) NOT NULL,
    body TEXT,
    status task_status NOT NULL DEFAULT 'queued',
    priority INT NOT NULL DEFAULT 2, -- 0=P0, 1=P1, 2=P2, 3=P3
    
    -- Assignment
    assigned_agent_id UUID REFERENCES agents(id),
    repository_id UUID REFERENCES repositories(id),
    
    -- Structured context (the key differentiator)
    context JSONB NOT NULL DEFAULT '{
        "files": [],
        "decisions": [],
        "acceptance": [],
        "custom": {}
    }',
    
    -- Labels (simple array, not separate table for v1)
    labels TEXT[] NOT NULL DEFAULT '{}',
    
    -- Tracking
    created_by_agent_id UUID REFERENCES agents(id),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    completion_summary TEXT,
    block_reason TEXT,
    
    -- Session that's working on it
    current_session VARCHAR(100),
    
    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    
    CONSTRAINT tasks_installation_number_unique 
        UNIQUE (installation_id, number)
);

CREATE INDEX idx_tasks_installation_status ON tasks(installation_id, status) 
    WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_assigned_agent ON tasks(assigned_agent_id, status, priority) 
    WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_priority_created ON tasks(installation_id, priority, created_at) 
    WHERE deleted_at IS NULL AND status = 'queued';
CREATE INDEX idx_tasks_labels ON tasks USING GIN(labels);
CREATE INDEX idx_tasks_context ON tasks USING GIN(context);

-- Task number sequence (per installation)
CREATE TABLE task_sequences (
    installation_id UUID PRIMARY KEY REFERENCES installations(id),
    next_number INT NOT NULL DEFAULT 1
);

-- Task dependencies
CREATE TABLE task_dependencies (
    task_id UUID NOT NULL REFERENCES tasks(id),
    depends_on_task_id UUID NOT NULL REFERENCES tasks(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    
    PRIMARY KEY (task_id, depends_on_task_id),
    
    -- Can't depend on yourself
    CONSTRAINT task_dependencies_no_self CHECK (task_id != depends_on_task_id)
);

CREATE INDEX idx_task_dependencies_depends_on ON task_dependencies(depends_on_task_id);

-- Task activity log
CREATE TABLE task_activities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL REFERENCES tasks(id),
    
    -- Who did it
    agent_id UUID REFERENCES agents(id),
    session_key VARCHAR(100),
    
    -- What happened
    action VARCHAR(50) NOT NULL, -- created, updated, started, completed, commented, etc.
    
    -- Details (varies by action)
    details JSONB NOT NULL DEFAULT '{}',
    -- For updates: {"field": "status", "old": "queued", "new": "in_progress"}
    -- For comments: {"body": "This is a comment"}
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_task_activities_task ON task_activities(task_id, created_at DESC);
CREATE INDEX idx_task_activities_agent ON task_activities(agent_id, created_at DESC);

-- Dispatch queue
CREATE TABLE dispatch_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id UUID NOT NULL REFERENCES tasks(id),
    agent_id UUID NOT NULL REFERENCES agents(id),
    installation_id UUID NOT NULL REFERENCES installations(id),
    
    status dispatch_status NOT NULL DEFAULT 'pending',
    priority INT NOT NULL,
    
    -- Timing
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    
    -- Retry tracking
    attempt INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_retry_at TIMESTAMPTZ,
    last_error TEXT,
    
    -- Webhook details
    webhook_url TEXT NOT NULL,
    webhook_payload JSONB,
    webhook_response_code INT,
    webhook_response_body TEXT,
    
    CONSTRAINT dispatch_queue_task_unique UNIQUE (task_id)
);

CREATE INDEX idx_dispatch_queue_pending 
    ON dispatch_queue(agent_id, priority, created_at)
    WHERE status = 'pending';
CREATE INDEX idx_dispatch_queue_retry
    ON dispatch_queue(next_retry_at)
    WHERE status = 'failed' AND attempt < max_attempts;
CREATE INDEX idx_dispatch_queue_dead
    ON dispatch_queue(installation_id, created_at)
    WHERE status = 'dead';

-- Webhooks (outbound, user-configured)
CREATE TABLE webhooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id UUID NOT NULL REFERENCES installations(id),
    
    name VARCHAR(100),
    url TEXT NOT NULL,
    secret VARCHAR(255), -- for signature verification
    
    -- What events to send
    events TEXT[] NOT NULL, -- e.g., ['task.created', 'task.completed', 'repo.push']
    
    -- Scope
    repository_id UUID REFERENCES repositories(id), -- null = all repos
    
    -- State
    is_active BOOLEAN NOT NULL DEFAULT true,
    
    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_webhooks_installation ON webhooks(installation_id) WHERE is_active;

-- Webhook deliveries (log)
CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id UUID NOT NULL REFERENCES webhooks(id),
    
    event VARCHAR(50) NOT NULL,
    payload JSONB NOT NULL,
    
    -- Response
    response_code INT,
    response_body TEXT,
    response_time_ms INT,
    
    -- Status
    success BOOLEAN NOT NULL,
    error TEXT,
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_webhook_deliveries_webhook ON webhook_deliveries(webhook_id, created_at DESC);

-- Commits (denormalized from Forgejo for querying)
CREATE TABLE commits (
    sha VARCHAR(64) PRIMARY KEY,
    repository_id UUID NOT NULL REFERENCES repositories(id),
    
    message TEXT NOT NULL,
    author_name VARCHAR(255),
    author_email VARCHAR(255),
    
    -- Agent attribution
    agent_id UUID REFERENCES agents(id),
    session_key VARCHAR(100),
    
    -- Relationships
    parent_shas TEXT[] NOT NULL DEFAULT '{}',
    
    authored_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_commits_repository ON commits(repository_id, authored_at DESC);
CREATE INDEX idx_commits_agent ON commits(agent_id, authored_at DESC);

-- Functions for task number generation
CREATE OR REPLACE FUNCTION next_task_number(p_installation_id UUID)
RETURNS INT AS $$
DECLARE
    v_number INT;
BEGIN
    INSERT INTO task_sequences (installation_id, next_number)
    VALUES (p_installation_id, 2)
    ON CONFLICT (installation_id) DO UPDATE
    SET next_number = task_sequences.next_number + 1
    RETURNING next_number - 1 INTO v_number;
    
    RETURN v_number;
END;
$$ LANGUAGE plpgsql;

-- Trigger for updated_at
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tasks_updated_at
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER agents_updated_at
    BEFORE UPDATE ON agents
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER installations_updated_at
    BEFORE UPDATE ON installations
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

-- Trigger to emit events via NOTIFY
CREATE OR REPLACE FUNCTION emit_task_event()
RETURNS TRIGGER AS $$
DECLARE
    event_type TEXT;
    payload JSONB;
BEGIN
    IF TG_OP = 'INSERT' THEN
        event_type := 'task.created';
        payload := jsonb_build_object(
            'task_id', NEW.id,
            'installation_id', NEW.installation_id,
            'status', NEW.status
        );
    ELSIF TG_OP = 'UPDATE' AND OLD.status != NEW.status THEN
        event_type := 'task.status_changed';
        payload := jsonb_build_object(
            'task_id', NEW.id,
            'installation_id', NEW.installation_id,
            'old_status', OLD.status,
            'new_status', NEW.status,
            'assigned_agent_id', NEW.assigned_agent_id
        );
    ELSE
        event_type := 'task.updated';
        payload := jsonb_build_object(
            'task_id', NEW.id,
            'installation_id', NEW.installation_id
        );
    END IF;
    
    PERFORM pg_notify('task_events', jsonb_build_object(
        'event', event_type,
        'data', payload
    )::text);
    
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tasks_notify
    AFTER INSERT OR UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION emit_task_event();
```

### 6.2 Context Field Structure

The `context` JSONB field is the key differentiator from traditional issues:

```json
{
  "files": [
    {
      "repo": "openclaw",
      "path": "src/core/retry.ts",
      "lines": [45, 120],
      "note": "Main retry logic here"
    }
  ],
  "decisions": [
    "Use exponential backoff with jitter",
    "Max 3 retries for transient errors",
    "Log all retry attempts"
  ],
  "acceptance": [
    "500 errors trigger retry before failover",
    "Retry count included in session logs",
    "Configurable via provider settings"
  ],
  "custom": {
    "related_pr": "https://github.com/...",
    "design_doc": "https://..."
  }
}
```

**Queries on context:**
```sql
-- Find tasks referencing a file
SELECT * FROM tasks
WHERE context->'files' @> '[{"path": "src/core/retry.ts"}]';

-- Find tasks with specific decision
SELECT * FROM tasks
WHERE context->'decisions' ? 'Use exponential backoff';
```

---

## 7. Integration Protocol

### 7.1 Design Principle

GitClaw must work with any agent runtime, not just OpenClaw. The integration protocol is:
1. **Webhook-first** — receive tasks via HTTP POST
2. **REST API** — update status via API calls
3. **Optional SDK** — convenience wrappers

### 7.2 Webhook Contract

**Receiving tasks:**

Any agent runtime that can receive HTTP POSTs can integrate.

```
POST https://your-agent-runtime.example/gitclaw/webhook
Content-Type: application/json
X-GitClaw-Event: task.dispatched
X-GitClaw-Delivery: disp_xxxxx
X-GitClaw-Signature: sha256=xxxxxxx
```

Payload:
```json
{
  "event": "task.dispatched",
  "task": {
    "number": 42,
    "title": "Implement retry logic",
    "body": "Full description...",
    "status": "queued",
    "priority": 1,
    "context": {
      "files": [...],
      "decisions": [...],
      "acceptance": [...]
    },
    "url": "https://gitclaw.io/sam/tasks/42"
  },
  "target_agent": {
    "slug": "derek",
    "session_pattern": "agent:2b:*"
  },
  "installation": {
    "id": "inst_xxx",
    "name": "sam-openclaw"
  }
}
```

**Signature verification:**
```python
import hmac
import hashlib

def verify_signature(payload: bytes, signature: str, secret: str) -> bool:
    expected = 'sha256=' + hmac.new(
        secret.encode(),
        payload,
        hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(expected, signature)
```

### 7.3 Status Update Protocol

**Claiming a task (starting work):**
```
POST /v1/tasks/42/start
Authorization: Bearer gitclaw_sk_xxx
X-GitClaw-Agent: derek
Content-Type: application/json

{
  "session": "agent:2b:main"
}
```

**Completing a task:**
```
POST /v1/tasks/42/complete
Authorization: Bearer gitclaw_sk_xxx
X-GitClaw-Agent: derek
Content-Type: application/json

{
  "summary": "Implemented retry logic with exponential backoff. Added tests.",
  "session": "agent:2b:main"
}
```

**Blocking a task:**
```
POST /v1/tasks/42/block
Authorization: Bearer gitclaw_sk_xxx
X-GitClaw-Agent: derek
Content-Type: application/json

{
  "reason": "Waiting for API schema from eng-041",
  "session": "agent:2b:main"
}
```

### 7.4 OpenClaw-Specific Integration

For OpenClaw, we provide a channel plugin:

```typescript
// plugins/gitclaw/index.ts
import { ChannelPlugin } from 'openclaw';

export const gitclaw: ChannelPlugin = {
  name: 'gitclaw',
  
  // Webhook endpoint for receiving dispatched tasks
  async handleWebhook(req, res) {
    const event = req.headers['x-gitclaw-event'];
    const signature = req.headers['x-gitclaw-signature'];
    
    // Verify signature
    if (!this.verifySignature(req.body, signature)) {
      return res.status(401).send('Invalid signature');
    }
    
    if (event === 'task.dispatched') {
      const { task, target_agent } = req.body;
      
      // Format task for agent
      const message = this.formatTaskMessage(task);
      
      // Route to agent session
      await this.sessions.send({
        sessionKey: this.resolveSession(target_agent.session_pattern),
        message
      });
    }
    
    res.status(200).send('OK');
  },
  
  formatTaskMessage(task) {
    return `
📋 **Task Dispatched: #${task.number}**
${task.title}

**Context:**
${task.context.files?.map(f => `- 📁 ${f.path}`).join('\n') || 'None'}

**Decisions Made:**
${task.context.decisions?.map(d => `- ${d}`).join('\n') || 'None'}

**Acceptance Criteria:**
${task.context.acceptance?.map(a => `- [ ] ${a}`).join('\n') || 'None'}

**Full Details:** ${task.url}

---
To start: \`gitclaw task start ${task.number}\`
When done: \`gitclaw task done ${task.number} --summary "..."\`
    `.trim();
  }
};
```

### 7.5 CLI for Agents

A CLI tool agents can invoke:

```bash
# Install
npm install -g @gitclaw/cli

# Configure (one-time)
gitclaw auth login  # prompts for API key

# Agent commands
gitclaw task list --mine --status queued
gitclaw task show 42
gitclaw task start 42 --session "agent:2b:main"
gitclaw task done 42 --summary "Implemented feature X"
gitclaw task block 42 --reason "Waiting on dependency"

# Create tasks for other agents
gitclaw task create \
  --title "Review retry implementation" \
  --agent jeremy \
  --priority 1 \
  --depends-on 42 \
  --context-file src/core/retry.ts \
  --acceptance "Code follows style guide" \
  --acceptance "Tests pass"
```

### 7.6 SDK (TypeScript)

```typescript
import { GitClaw } from '@gitclaw/sdk';

const client = new GitClaw({
  apiKey: process.env.GITCLAW_API_KEY,
  agentSlug: 'derek'
});

// Receive tasks via callback (wraps webhook)
client.onTaskDispatched(async (task) => {
  console.log(`Received task #${task.number}: ${task.title}`);
  
  // Claim it
  await client.tasks.start(task.number, {
    session: process.env.SESSION_KEY
  });
  
  // Do work...
  
  // Complete it
  await client.tasks.complete(task.number, {
    summary: 'Implemented the feature'
  });
});

// Proactive queries
const myTasks = await client.tasks.list({
  assignedAgent: 'derek',
  status: 'queued'
});

// Create task for another agent
await client.tasks.create({
  title: 'Review my PR',
  assignedAgent: 'jeremy',
  priority: 1,
  dependsOn: [42],
  context: {
    files: [{ repo: 'openclaw', path: 'src/core/retry.ts' }],
    acceptance: ['Code review approved']
  }
});
```

---

## 8. Security Considerations

### 8.1 Authentication & Authorization

| Layer | Mechanism |
|-------|-----------|
| API | Bearer token (API key) |
| Git HTTPS | Same API key as password |
| Git SSH | ED25519 keys per Installation |
| Webhooks | HMAC-SHA256 signatures |
| Web UI | Session cookie + CSRF token |

### 8.2 API Key Security

- Keys are hashed (bcrypt) before storage
- Keys use a prefix (`gitclaw_sk_`) for identification in logs
- Keys can be rotated without downtime
- Keys can be scoped (read-only, specific repos) in v2

### 8.3 Webhook Security

```go
func signPayload(payload []byte, secret string) string {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(payload)
    return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
```

Receivers MUST verify signatures before processing.

### 8.4 Data Isolation

- All queries include `installation_id` filter
- Database row-level security as defense in depth
- No cross-installation data access possible

### 8.5 Audit Log

Every API call logged:
```json
{
  "timestamp": "2026-02-03T11:15:00Z",
  "installation_id": "inst_xxx",
  "agent_slug": "derek",
  "session_key": "agent:2b:main",
  "action": "task.complete",
  "resource": "task:42",
  "ip": "192.168.1.100",
  "user_agent": "gitclaw-cli/1.0.0"
}
```

---

## 9. Deployment Architecture

### 9.1 Docker Compose (Development/Small Teams)

```yaml
version: '3.8'

services:
  gitclaw:
    image: gitclaw/gitclaw:latest
    ports:
      - "3000:3000"  # API + Web UI
      - "3022:22"    # Git SSH (optional)
    environment:
      DATABASE_URL: postgres://gitclaw:secret@postgres:5432/gitclaw
      FORGEJO_DATA: /data/forgejo
      SECRET_KEY: ${SECRET_KEY}
    volumes:
      - gitclaw-data:/data
    depends_on:
      - postgres

  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: gitclaw
      POSTGRES_PASSWORD: secret
      POSTGRES_DB: gitclaw
    volumes:
      - postgres-data:/var/lib/postgresql/data

volumes:
  gitclaw-data:
  postgres-data:
```

### 9.2 Production (Kubernetes)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gitclaw-api
spec:
  replicas: 3
  selector:
    matchLabels:
      app: gitclaw-api
  template:
    spec:
      containers:
        - name: gitclaw
          image: gitclaw/gitclaw:latest
          ports:
            - containerPort: 3000
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: gitclaw-secrets
                  key: database-url
            - name: REDIS_URL
              value: redis://redis:6379
          resources:
            requests:
              memory: "256Mi"
              cpu: "250m"
            limits:
              memory: "1Gi"
              cpu: "1000m"
---
apiVersion: v1
kind: Service
metadata:
  name: gitclaw-api
spec:
  selector:
    app: gitclaw-api
  ports:
    - port: 80
      targetPort: 3000
  sessionAffinity: ClientIP  # For WebSocket
```

### 9.3 Resource Sizing

| Scale | API Replicas | PostgreSQL | Redis | Storage |
|-------|--------------|------------|-------|---------|
| Small (<10 agents) | 1 | 1 vCPU, 2GB | None | 50GB |
| Medium (10-50 agents) | 2-3 | 2 vCPU, 4GB | 1 node | 200GB |
| Large (50+ agents) | 5+ | 4 vCPU, 16GB | 3 nodes | 1TB+ |

---

## 10. Open Questions & Decisions Needed

### 10.1 Forgejo Integration Depth

**Options:**

A. **Sidecar process** — Forgejo runs as separate process, communicate via internal HTTP
   - Pros: Clean separation, easier updates
   - Cons: More operational complexity

B. **Library embedding** — Import Forgejo packages directly into GitClaw
   - Pros: Single binary, tighter integration
   - Cons: Go dependency management, version coupling

C. **Minimal git implementation** — Use go-git, build minimal server
   - Pros: Full control, smallest footprint
   - Cons: Significant implementation effort

**Recommendation:** Start with **A (sidecar)**, migrate to **B** if integration friction is high.

### 10.2 Multi-Tenancy Model

**Options:**

A. **Single-tenant** — One GitClaw instance per operator
   - Pros: Simple, full isolation
   - Cons: Higher operational overhead for SaaS

B. **Multi-tenant** — One instance serves many operators
   - Pros: Efficient for SaaS, shared infrastructure
   - Cons: Noisy neighbor risks, complex isolation

**Recommendation:** **A** for v1 (self-hosted focus), **B** for hosted service later.

### 10.3 Real-time Technology

**Options:**

A. **WebSocket only** — Full duplex, established pattern
B. **SSE only** — Simpler, HTTP-native, one-way
C. **Both** — WebSocket for UI, SSE for simple consumers

**Recommendation:** **C** — WebSocket for dashboard (needs bidirectional for subscriptions), SSE as fallback for simpler integrations.

### 10.4 Memory Integration

The spec mentions memory integration. Options:

A. **Separate systems** — GitClaw is tasks, memory is elsewhere
B. **Light integration** — API to fetch memory context on dispatch
C. **Deep integration** — GitClaw stores/manages agent memory

**Recommendation:** **B** for v1 — dispatch webhook can include recent memory, but GitClaw isn't the memory store.

---

## 11. Implementation Phases

### Phase 1: Foundation (Weeks 1-4)

- [ ] Project scaffolding (Go modules, directory structure)
- [ ] PostgreSQL schema, migrations, sqlc codegen
- [ ] Core models: Installation, Agent, Repository, Task
- [ ] REST API: CRUD for all resources
- [ ] API authentication (bearer token)
- [ ] Forgejo sidecar integration (basic)
- [ ] Git clone/push/pull working

### Phase 2: Dispatch Engine (Weeks 5-6)

- [ ] Task state machine
- [ ] Dependency resolution
- [ ] Dispatch queue
- [ ] Webhook delivery with retry
- [ ] Dead letter queue
- [ ] LISTEN/NOTIFY event system

### Phase 3: Real-time & Web UI (Weeks 7-10)

- [ ] WebSocket hub
- [ ] GraphQL API with subscriptions
- [ ] React dashboard scaffolding
- [ ] Task board (Kanban)
- [ ] Repository browser
- [ ] Agent activity views
- [ ] "Currently Cranking" live widget

### Phase 4: Integration (Weeks 11-12)

- [ ] OpenClaw channel plugin
- [ ] CLI tool
- [ ] TypeScript SDK
- [ ] GitHub import tool
- [ ] Documentation

### Phase 5: Polish & Launch (Weeks 13-14)

- [ ] Security audit
- [ ] Performance testing
- [ ] Rate limiting
- [ ] Error handling
- [ ] Monitoring & alerts
- [ ] Public beta launch

---

## 12. Appendix

### A. Directory Structure

```
gitclaw/
├── cmd/
│   ├── gitclaw/          # Main binary
│   │   └── main.go
│   └── gitclaw-cli/      # CLI tool
│       └── main.go
├── internal/
│   ├── api/              # HTTP handlers
│   │   ├── rest/
│   │   ├── graphql/
│   │   └── websocket/
│   ├── core/             # Business logic
│   │   ├── tasks/
│   │   ├── agents/
│   │   ├── repos/
│   │   └── dispatch/
│   ├── store/            # Database access
│   │   ├── postgres/
│   │   └── queries/      # sqlc generated
│   ├── forgejo/          # Forgejo integration
│   ├── webhook/          # Outbound webhooks
│   └── realtime/         # WebSocket hub
├── pkg/
│   └── gitclaw/          # Public SDK package
├── web/                  # React frontend
│   ├── src/
│   └── package.json
├── migrations/           # SQL migrations
├── docker/
│   ├── Dockerfile
│   └── docker-compose.yml
├── docs/
├── go.mod
└── README.md
```

### B. Configuration Schema

```yaml
# gitclaw.yaml
server:
  host: 0.0.0.0
  port: 3000
  
database:
  url: postgres://user:pass@localhost:5432/gitclaw
  max_connections: 25
  
forgejo:
  mode: sidecar  # or embedded
  data_path: /data/forgejo
  port: 3001
  
redis:
  url: redis://localhost:6379  # optional for v1
  
security:
  secret_key: ${SECRET_KEY}  # for signing
  cors_origins:
    - https://app.gitclaw.io
    
dispatch:
  webhook_timeout: 30s
  max_retries: 3
  retry_backoff: [1m, 5m, 15m]
  
rate_limits:
  api_per_hour: 5000
  git_per_hour: 1000
  webhooks_per_minute: 100
```

### C. Metrics & Observability

**Key metrics to expose (Prometheus format):**

```
# Dispatch
gitclaw_dispatch_queue_depth{agent="derek"}
gitclaw_dispatch_webhook_duration_seconds{agent="derek", status="success"}
gitclaw_dispatch_retries_total{agent="derek"}
gitclaw_dispatch_dead_letter_total{agent="derek"}

# Tasks
gitclaw_tasks_total{status="queued"}
gitclaw_task_duration_seconds{agent="derek", status="done"}

# API
gitclaw_api_requests_total{method="POST", path="/tasks", status="200"}
gitclaw_api_request_duration_seconds{method="GET", path="/tasks"}

# Git
gitclaw_git_operations_total{operation="push", repo="openclaw"}
gitclaw_git_operation_duration_seconds{operation="clone"}

# WebSocket
gitclaw_websocket_connections{installation="sam-openclaw"}
gitclaw_websocket_messages_total{direction="out"}
```

---

*End of Architecture Document*


---

## Dashboard Activity Feed Data Source

The dashboard **Your Feed** section reads from `GET /api/feed?org_id=<uuid>` which queries the `activity_log` table in Postgres. Activity records are written by:

- **OpenClaw agents** via `POST /api/feed/push` (bridge or agent integration).
- **Webhook status events** handled in `internal/webhook/handlers.go` (`logActivity`).

The web UI maps these feed items into the dashboard cards (actor, description, type badge, relative timestamp).

---

## Pearl GitHub Workflow (Implemented)

### Runtime Flow

1. GitHub webhook enters `POST /api/github/webhook`.
2. Delivery is signature-validated and deduplicated.
3. Webhook job is queued (`github_sync_jobs`, type `webhook`).
4. Push events:
- ingest commit records into project commit store
- enqueue repo sync jobs (type `repo_sync`)
5. Issue/PR/comment events:
- upsert project issues + GitHub links
- record issue activity events
6. Poll fallback (`githubsync.RepoDriftPoller`) checks default branch heads on interval and enqueues repo sync jobs if drift exists.
7. Human-triggered publish (`POST /api/projects/{id}/publish`) performs preflight checks, pushes local branch to remote default branch, then closes linked GitHub issues with idempotent comment+close operations.

### Data Ownership

- GitHub sync jobs and queue state: `github_sync_jobs`
- Issue mappings: `project_issue_github_links`
- Commit ingestion and browser commits: `project_commits`
- Repo sync checkpoints and conflict state: `project_repo_bindings`, `project_repo_active_branches`
- Activity/audit trail: `activity_log`

### Recovery + Operations Hooks

- Queue and quota health: `GET /api/github/sync/health`
- Dead-letter inspection: `GET /api/github/sync/dead-letters`
- Dead-letter replay: `POST /api/github/sync/dead-letters/{id}/replay`
- Manual repo resync: `POST /api/projects/{id}/repo/sync`
- Manual issue import: `POST /api/projects/{id}/issues/import`
- Conflict resolution: `POST /api/projects/{id}/repo/conflicts/resolve`
