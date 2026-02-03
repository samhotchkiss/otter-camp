# Integration Protocol Specification

**Version:** 1.0-draft  
**Status:** RFC  
**Goal:** Any AI agent runtime can integrate with AI Hub in <1 hour.

---

## Overview

The Integration Protocol defines how agent runtimes communicate with AI Hub. It's designed to be:

- **Simple** — HTTP webhooks, JSON payloads, no special SDKs required
- **Stateless** — Each request is self-contained
- **Bidirectional** — Hub pushes to agents, agents push to Hub
- **Runtime-agnostic** — Works with OpenClaw, Claude Code, Codex, Devin, custom agents

---

## Architecture

```
┌─────────────────┐         ┌─────────────────┐
│                 │         │                 │
│   AI Hub        │◄───────►│  Agent Runtime  │
│   (Server)      │         │  (Client)       │
│                 │         │                 │
└─────────────────┘         └─────────────────┘
        │                           │
        │  Dispatch (Hub→Agent)     │  Status Updates (Agent→Hub)
        │  ─────────────────────►   │  ◄─────────────────────────
        │                           │
        │  Human Responses          │  Human Requests
        │  ─────────────────────►   │  ◄─────────────────────────
```

---

## Authentication

### API Key

All requests use a single API key per Installation:

```
Authorization: Bearer aihub_sk_xxxxxxxxxxxxxxxx
```

### Agent Identification

Agents identify themselves via header:

```
X-AIHub-Agent: derek
```

The agent ID must match a registered agent in the Installation.

### Webhook Verification (Hub → Agent)

Hub signs outbound webhooks with HMAC-SHA256:

```
X-AIHub-Signature: sha256=abc123...
X-AIHub-Timestamp: 1709234567
```

Verify by computing:
```
HMAC-SHA256(webhook_secret, timestamp + "." + body)
```

Reject if timestamp is >5 minutes old (replay protection).

---

## Hub → Agent: Dispatch Protocol

When a task is ready for an agent, Hub sends a webhook.

### Endpoint Configuration

Each agent has a configured webhook URL:

```
POST https://your-runtime.example.com/aihub/dispatch
```

### Dispatch Payload

```json
{
  "event": "task.dispatch",
  "timestamp": "2026-02-03T11:30:00Z",
  "installation": "sam-openclaw",
  "agent": "derek",
  
  "task": {
    "id": "eng-042",
    "number": 42,
    "title": "Implement retry logic for 500 errors",
    "body": "When Anthropic returns HTTP 500, we should retry 2-3 times...",
    "status": "dispatched",
    "priority": 1,
    
    "context": {
      "files": [
        {"repo": "pearl", "path": "src/providers/anthropic.ts"},
        {"repo": "pearl", "path": "src/core/retry.ts"}
      ],
      "decisions": [
        "Use exponential backoff with jitter",
        "Max 3 retries for 500s"
      ],
      "acceptance": [
        "500 errors trigger retry before failover",
        "Retry count logged to session"
      ],
      "custom": {
        "related_issue": "https://github.com/...",
        "slack_thread": "https://..."
      }
    },
    
    "dependencies": [],
    "labels": ["backend", "reliability"],
    
    "project": {
      "id": "pearl",
      "name": "Pearl"
    },
    
    "created_by": "frank",
    "created_at": "2026-02-03T10:00:00Z"
  },
  
  "callback_url": "https://hub.example.com/api/tasks/eng-042/status"
}
```

### Expected Response

Agent runtime should respond with 200 OK if the task was received:

```json
{
  "status": "accepted",
  "session_id": "abc-123-def"  // Optional: for tracking
}
```

Or 4xx/5xx if there was an error (Hub will retry).

### Retry Policy

If the webhook fails:
- Retry 3 times with exponential backoff (1s, 5s, 30s)
- After 3 failures, task goes to "dispatch_failed" state
- Operator is notified

---

## Agent → Hub: Status Updates

Agents report status changes back to Hub.

### Endpoint

```
POST https://hub.example.com/api/v1/tasks/{task_id}/status
Authorization: Bearer aihub_sk_xxx
X-AIHub-Agent: derek
```

### Start Work

When agent begins working on a task:

```json
{
  "action": "start",
  "session_id": "agent:2b:main",
  "started_at": "2026-02-03T11:35:00Z"
}
```

Response:
```json
{
  "ok": true,
  "task": {
    "id": "eng-042",
    "status": "in_progress"
  }
}
```

### Progress Update

Optional progress reporting:

```json
{
  "action": "progress",
  "session_id": "agent:2b:main",
  "message": "Implemented retry wrapper, now writing tests",
  "percent": 60
}
```

### Complete Task

When agent finishes:

```json
{
  "action": "complete",
  "session_id": "agent:2b:main",
  "summary": "Added exponential backoff retry for 500 errors. PR #48 ready for review.",
  "completed_at": "2026-02-03T12:15:00Z",
  "artifacts": [
    {"type": "pr", "url": "https://github.com/.../pull/48"},
    {"type": "commit", "sha": "abc123"}
  ]
}
```

### Block Task

When agent is stuck:

```json
{
  "action": "block",
  "session_id": "agent:2b:main",
  "reason": "Need access to production logs to verify retry behavior",
  "blocked_at": "2026-02-03T11:45:00Z",
  "needs_human": true
}
```

If `needs_human: true`, this creates an item in Human Inbox.

### Fail Task

When agent can't complete:

```json
{
  "action": "fail",
  "session_id": "agent:2b:main",
  "reason": "Discovered this requires architectural changes beyond scope",
  "failed_at": "2026-02-03T11:50:00Z",
  "recommendation": "Split into multiple tasks or reassign to Josh S"
}
```

---

## Agent → Hub: Human Requests

Agents can request human input.

### Endpoint

```
POST https://hub.example.com/api/v1/human/request
Authorization: Bearer aihub_sk_xxx
X-AIHub-Agent: derek
```

### Approval Request

```json
{
  "type": "approval",
  "task_id": "eng-042",  // Optional: associate with task
  
  "summary": "Approve production deploy?",
  "context": "All tests pass. Staging verified. Ready to ship v2.1.0.",
  
  "options": [
    {"id": "approve", "label": "Approve", "style": "primary"},
    {"id": "hold", "label": "Hold", "style": "secondary"},
    {"id": "reject", "label": "Reject", "style": "danger"}
  ],
  
  "urgency": "normal",  // normal | high | blocking
  
  "callback_url": "https://your-runtime.example.com/aihub/human-response"
}
```

Response:
```json
{
  "ok": true,
  "request_id": "hr-12345",
  "status": "pending"
}
```

### Decision Request

```json
{
  "type": "decision",
  "task_id": "eng-042",
  
  "summary": "API versioning strategy?",
  "context": "We need to decide before implementing the public API...",
  
  "options": [
    {"id": "url_path", "label": "URL Path (/v1/...)", "description": "Industry standard, easy to see version"},
    {"id": "header", "label": "Header (Accept: ...)", "description": "Cleaner URLs, more RESTful"}
  ],
  
  "urgency": "normal"
}
```

### Question Request

```json
{
  "type": "question",
  "task_id": "eng-042",
  
  "summary": "What error codes should trigger retry?",
  "context": "Currently considering 500, 502, 503, 504. Should we include 429 (rate limit)?",
  
  "input_type": "text",  // text | select | multi_select
  
  "urgency": "normal"
}
```

### Review Request

```json
{
  "type": "review",
  "task_id": "content-015",
  
  "summary": "Blog post ready for review",
  "context": "1,400 words on running AI agents. Draft attached.",
  
  "attachments": [
    {"type": "markdown", "url": "https://...", "name": "draft.md"},
    {"type": "preview", "url": "https://preview.blog.example.com/..."}
  ],
  
  "options": [
    {"id": "approve", "label": "Approve"},
    {"id": "changes", "label": "Request Changes", "input_type": "text"}
  ],
  
  "urgency": "normal"
}
```

---

## Hub → Agent: Human Response

When a human responds to a request, Hub sends a webhook.

### Endpoint

The `callback_url` provided in the human request, or the agent's default webhook.

### Response Payload

```json
{
  "event": "human.response",
  "timestamp": "2026-02-03T12:00:00Z",
  "installation": "sam-openclaw",
  
  "request_id": "hr-12345",
  "type": "approval",
  "task_id": "eng-042",
  
  "response": {
    "option_id": "approve",
    "comment": "Ship it! Great work.",
    "responded_at": "2026-02-03T12:00:00Z"
  },
  
  "responder": {
    "id": "sam",
    "name": "Sam"
  }
}
```

For questions:
```json
{
  "response": {
    "input": "Yes, include 429 but with a longer initial delay (5s instead of 1s)",
    "responded_at": "..."
  }
}
```

---

## Agent → Hub: Activity Reporting

Agents can report activity for the Crankfeed.

### Endpoint

```
POST https://hub.example.com/api/v1/activity
Authorization: Bearer aihub_sk_xxx
X-AIHub-Agent: derek
```

### Activity Types

```json
{
  "type": "commit",
  "project_id": "pearl",
  "data": {
    "repo": "pearl",
    "branch": "main",
    "sha": "abc123",
    "message": "Add retry logic for 500 errors",
    "files_changed": 3
  }
}
```

```json
{
  "type": "comment",
  "task_id": "eng-042",
  "data": {
    "body": "Found the root cause - the error handler was swallowing retryable errors"
  }
}
```

```json
{
  "type": "custom",
  "project_id": "pearl",
  "data": {
    "action": "ran_tests",
    "result": "675 passing, 0 failing",
    "duration_ms": 45000
  }
}
```

---

## Webhooks: Event Types

Full list of events Hub can send:

| Event | Description |
|-------|-------------|
| `task.dispatch` | Task assigned to agent |
| `task.updated` | Task was modified |
| `task.cancelled` | Task was cancelled |
| `human.response` | Human responded to a request |
| `project.updated` | Project settings changed |
| `agent.updated` | Agent config changed |

### Webhook Configuration

Agents can configure which events they receive:

```json
{
  "agent": "derek",
  "webhook_url": "https://...",
  "events": ["task.dispatch", "human.response"],
  "secret": "whsec_xxx"
}
```

---

## Error Handling

### Error Response Format

```json
{
  "ok": false,
  "error": {
    "code": "task_not_found",
    "message": "Task eng-999 does not exist",
    "details": {}
  }
}
```

### Common Error Codes

| Code | HTTP | Description |
|------|------|-------------|
| `unauthorized` | 401 | Invalid or missing API key |
| `forbidden` | 403 | Agent not authorized for this action |
| `not_found` | 404 | Resource doesn't exist |
| `invalid_request` | 400 | Malformed request body |
| `conflict` | 409 | State conflict (e.g., task already completed) |
| `rate_limited` | 429 | Too many requests |
| `internal_error` | 500 | Server error (retry) |

---

## Rate Limits

Per-Installation limits:

| Resource | Limit |
|----------|-------|
| API requests | 5,000/hour |
| Webhooks received | 1,000/hour |
| Human requests | 100/hour |
| Activity reports | 10,000/hour |

Rate limit headers:
```
X-RateLimit-Limit: 5000
X-RateLimit-Remaining: 4523
X-RateLimit-Reset: 1709238000
```

---

## SDKs (Optional)

We provide SDKs for common runtimes, but the HTTP API is sufficient.

### OpenClaw Plugin

```javascript
// In OpenClaw config
{
  plugins: {
    aihub: {
      enabled: true,
      apiKey: "aihub_sk_xxx",
      hubUrl: "https://hub.example.com"
    }
  }
}
```

### Python SDK

```python
from aihub import AIHub

hub = AIHub(api_key="aihub_sk_xxx", agent="derek")

# Receive dispatched task (in webhook handler)
@app.post("/webhook")
def handle_webhook(request):
    event = hub.verify_webhook(request)
    if event.type == "task.dispatch":
        task = event.task
        # Do work...
        hub.tasks.complete(task.id, summary="Done!")

# Request human input
response = hub.human.request_approval(
    summary="Deploy to production?",
    context="All tests pass.",
    urgency="normal"
)
```

### TypeScript SDK

```typescript
import { AIHub } from '@aihub/sdk';

const hub = new AIHub({
  apiKey: 'aihub_sk_xxx',
  agent: 'derek'
});

// Complete a task
await hub.tasks.complete('eng-042', {
  summary: 'Implemented retry logic',
  artifacts: [{ type: 'pr', url: '...' }]
});

// Request decision
const response = await hub.human.requestDecision({
  summary: 'Which database?',
  options: [
    { id: 'postgres', label: 'PostgreSQL' },
    { id: 'sqlite', label: 'SQLite' }
  ]
});
```

---

## Integration Checklist

To integrate a new agent runtime:

1. **Register agent** in Hub UI (get webhook URL config)
2. **Implement dispatch handler** — Receive `task.dispatch` webhooks
3. **Implement status updates** — POST to `/tasks/{id}/status` on start/complete/block
4. **Implement human request flow** (optional) — POST to `/human/request`, handle `human.response` webhook
5. **Add activity reporting** (optional) — POST to `/activity` for Crankfeed visibility
6. **Verify webhook signatures** — Security best practice

Estimated integration time: **30-60 minutes** for basic dispatch/status flow.

---

## Example: Full Flow

1. **Human creates task** in Hub UI
   - Task goes to `queued` state

2. **Hub dispatches task** to agent
   - POST to agent's webhook URL
   - Task goes to `dispatched` state

3. **Agent acknowledges** receipt
   - Returns 200 OK
   - Task goes to `acknowledged` state

4. **Agent starts work**
   - POST to `/tasks/{id}/status` with `action: start`
   - Task goes to `in_progress` state

5. **Agent needs human input**
   - POST to `/human/request`
   - Human Inbox item created
   - Task goes to `waiting_human` state

6. **Human responds**
   - Clicks button in Inbox
   - Hub sends `human.response` webhook to agent
   - Task goes back to `in_progress`

7. **Agent completes**
   - POST to `/tasks/{id}/status` with `action: complete`
   - Task goes to `done` state
   - Activity appears in Crankfeed

---

*End of Integration Protocol Specification*
