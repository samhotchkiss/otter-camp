# Human Operator Workflow Specification

**Parent doc:** SPEC.md  
**Focus:** The human experience — what the operator sees, when they're pulled in, how they maintain oversight without micromanaging.

---

## Design Philosophy

### The Operator, Not the Worker

Traditional project management assumes the human does the work and the tool tracks it. GitClaw inverts this: **agents do the work, the human provides judgment**.

The human's job is:
1. **Set direction** — What are we building? What matters?
2. **Make decisions** — Resolve ambiguity, pick between options
3. **Approve outputs** — Sign off before external actions
4. **Unblock** — Provide access, context, or resources agents lack

The human's job is NOT:
- Assigning every task
- Checking status constantly
- Reading every commit message
- Attending to every update

### Attention is Scarce

An operator with 12 agents could receive 100+ updates per hour. Traditional notification models would bury them. GitClaw must **filter ruthlessly**.

**Core rule:** If it doesn't need human judgment, it doesn't need human attention.

---

## The Three Views

### 1. Projects Currently Cranking

The home screen. Shows all active work at a glance.

#### What is a "Project"?

A Project is a **grouping of related tasks** — usually mapped to a repository, but could be cross-repo (e.g., "Launch Campaign" spanning content + engineering + social).

```yaml
project:
  id: "itsalive"
  name: "ItsAlive"
  repos: ["itsalive"]
  agents: ["ivy", "derek", "jeffg"]  # Who typically works here
  status: "cranking" | "blocked" | "idle" | "paused"
```

#### Project Card Contents

Each card shows:

```
┌─────────────────────────────────────────────────┐
│ 🟢 ItsAlive                           2m ago   │
│                                                 │
│ Latest: Ivy pushed onboarding flow v2.         │
│         Derek reviewing auth implementation.    │
│                                                 │
│ ⚠️ NEEDS YOU: Approve production deploy (1)     │
│                                                 │
│ Tasks: 3 active · 12 done this week            │
└─────────────────────────────────────────────────┘
```

**Status Pulse Colors:**
- 🟢 **Green** — Active work, no blockers
- 🟡 **Yellow** — Blocked on something (dependency, external, waiting)
- 🔴 **Red** — Blocked on YOU
- ⚪ **Gray** — Idle (no active tasks)

**"NEEDS YOU" Badge:**
Only appears when there are items requiring human input. Clicking goes directly to those items.

#### Live Updates

Cards update in real-time:
- New activity → "Latest" line refreshes
- Status changes → Pulse color updates
- Human input resolved → Badge disappears

No page refresh. The dashboard is alive.

#### Sorting

Default sort: **Red first, then Yellow, then Green, then Gray.**

Within each color: Most recently active first.

This puts "needs you" at the top automatically.

---

### 2. Human Inbox

The **action queue**. Only items that require human judgment.

#### What Goes Here

Items appear in Human Inbox when an agent explicitly requests human input:

```yaml
human_request:
  task_id: "eng-042"
  type: "approval" | "decision" | "question" | "review" | "unblock"
  summary: "Approve production deploy?"
  context: "All tests pass. Staging verified. Ready to ship."
  options:
    - label: "Approve"
      action: "approve"
    - label: "Hold"
      action: "hold"
    - label: "Reject"
      action: "reject"
  urgency: "normal" | "high" | "blocking"
  requested_by: "ivy"
  requested_at: "2026-02-03T11:00:00Z"
```

#### Inbox Item Display

```
┌─────────────────────────────────────────────────┐
│ 🔴 APPROVAL · ItsAlive · from Ivy · 5m ago     │
│                                                 │
│ Approve production deploy?                      │
│                                                 │
│ All tests pass. Staging verified. Ready to ship.│
│                                                 │
│ [Approve]  [Hold]  [Reject]  [View Details →]  │
└─────────────────────────────────────────────────┘
```

#### One-Click Resolution

For simple approvals, the human can act directly from the inbox without opening the task. Click "Approve" → done → item disappears → agent notified.

#### Inbox Rules

1. **Explicit only** — Items appear here only when agents mark `needs_human=true`
2. **No duplicates** — One item per request, even if agent re-asks
3. **Auto-expire** — Stale requests (>48h) get flagged, can be bulk-dismissed
4. **Snooze** — "Remind me in 2 hours" option

#### Request Types

| Type | When Used | Typical Options |
|------|-----------|-----------------|
| **approval** | Before external action (deploy, send, publish) | Approve / Hold / Reject |
| **decision** | Fork in the road, agent can't choose | Option A / Option B / Other |
| **question** | Agent needs information | Free-text response |
| **review** | Work complete, needs sign-off | Approve / Request Changes |
| **unblock** | Agent stuck, needs help | Provide resource / Reassign / Cancel |

---

### 3. Crankfeed

The **ambient awareness stream**. Everything happening, skimmable, no action required.

#### What Goes Here

All activity across all projects:
- Task status changes
- Commits pushed
- Comments added
- Agent started/finished work
- Deploys, tests, builds

#### Display

```
┌─────────────────────────────────────────────────┐
│ CRANKFEED                            [Filters]  │
├─────────────────────────────────────────────────┤
│ 11:05 · Derek pushed 3 commits to pearl/main   │
│ 11:04 · Ivy marked eng-042 complete            │
│ 11:02 · Stone started content-015              │
│ 11:00 · Nova commented on social-008           │
│ 10:58 · Jeremy approved PR #47                 │
│ ...                                            │
└─────────────────────────────────────────────────┘
```

#### Filters

- By project
- By agent
- By activity type
- Time range

#### Not Notifications

Crankfeed does NOT push notifications. It's pull-only. The human checks it when they want ambient awareness, not because the system demanded attention.

---

## Notification Philosophy

### What Triggers a Notification

**Push notification (ping the human):**
- Item added to Human Inbox with `urgency: high` or `urgency: blocking`
- Project goes Red (blocked on human)
- Agent explicitly flags "urgent"

**No notification (human checks when ready):**
- Normal inbox items
- Project status changes (yellow, green)
- Crankfeed activity

### Notification Channels

Configurable per operator:
- In-app (always)
- Email digest (daily/weekly summary)
- Slack/Discord webhook
- SMS (high urgency only)
- Push notification (mobile app, if we build one)

### Quiet Hours

Operator sets quiet hours. During quiet hours:
- No push notifications except `urgency: blocking`
- Inbox items queue normally
- Crankfeed continues

---

## Agent-to-Human Communication

### How Agents Request Input

Agents don't DM the human. They create structured requests:

```python
# In agent code / skill
gitclaw.request_human(
    task_id="eng-042",
    type="approval",
    summary="Approve production deploy?",
    context="All tests pass. Staging verified.",
    options=["Approve", "Hold", "Reject"],
    urgency="normal"
)
```

This:
1. Adds item to Human Inbox
2. Updates task with `needs_human=true`
3. Changes project pulse to Red
4. Triggers notification if urgency warrants

### How Humans Respond

Human clicks a button or types a response. This:
1. Records the decision on the task
2. Notifies the agent (webhook to agent runtime)
3. Clears `needs_human` flag
4. Project pulse recalculates

### Response Payload to Agent

```json
{
  "event": "human_response",
  "task_id": "eng-042",
  "request_type": "approval",
  "response": "approve",
  "comment": "Ship it!",
  "responded_at": "2026-02-03T11:15:00Z"
}
```

Agent receives this via webhook and continues work.

---

## Dashboard States

### Empty States

**No projects:**
```
Welcome to [Product Name]!
Create your first project to get started.
[+ New Project]
```

**No inbox items:**
```
✨ Inbox Zero
Your agents are cranking. Nothing needs you right now.
```

**No crankfeed activity:**
```
Quiet so far today.
Activity will appear here as agents work.
```

### Loading States

Real-time connection establishing:
```
Connecting to live feed...
```

Reconnecting after disconnect:
```
Reconnecting... (last update: 2m ago)
```

### Error States

Agent webhook failed:
```
⚠️ Derek missed a dispatch (3 retries failed)
[View Details] [Retry Now] [Reassign Task]
```

---

## Mobile Experience

### Priority: Triage, Not Management

Mobile is for:
- Checking "am I needed?"
- Quick approvals (one-tap)
- Glancing at project status

Mobile is NOT for:
- Creating tasks
- Writing long responses
- Configuring anything

### Mobile Views

1. **Inbox** (default) — Action items only
2. **Projects** — Status cards, tap for details
3. **Feed** — Crankfeed, scrollable

### Gestures

- Swipe right on inbox item → Approve (if applicable)
- Swipe left → Snooze
- Pull down → Refresh

---

## Keyboard Navigation (Desktop)

Power users live in keyboard:

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate inbox items |
| `Enter` | Open selected item |
| `a` | Approve (in approval context) |
| `r` | Reject |
| `h` | Hold |
| `Esc` | Back to list |
| `g i` | Go to Inbox |
| `g p` | Go to Projects |
| `g f` | Go to Feed |
| `/` | Search |

---

## Integrations for Notifications

### Slack Integration

```
[Product Name] Bot

🔴 Approval needed: ItsAlive
Ivy is requesting: Approve production deploy?

[Approve] [Hold] [Reject] [Open in App]
```

Buttons work inline — approve without leaving Slack.

### Email Digest

Daily summary:
```
Subject: [Product Name] Daily Digest — Feb 3, 2026

TODAY'S NUMBERS
• 47 tasks completed
• 3 items need your input
• 12 agents active

NEEDS YOUR ATTENTION
1. Approve ItsAlive deploy (Ivy) — 2h waiting
2. Decision: API versioning strategy (Derek)
3. Review: Blog post draft (Stone)

[Open Dashboard]
```

---

## Metrics & Insights

### Operator Metrics

- **Response time** — How quickly do you resolve inbox items?
- **Bottleneck score** — How often are agents blocked on you?
- **Throughput** — Tasks completed per day/week

### Project Metrics

- **Velocity** — Tasks completed over time
- **Block rate** — % of time spent blocked
- **Agent utilization** — Active time vs idle

### Agent Metrics

- **Tasks completed**
- **Average task duration**
- **Block rate** (how often they get stuck)
- **Human request rate** (how often they need you)

---

## Summary: The Human Experience

1. **Open dashboard** → See projects at a glance, know immediately if you're needed
2. **Red badge?** → Go to inbox, handle the 1-3 items that need you
3. **Curious?** → Scroll crankfeed for ambient awareness
4. **Done** → Close tab, agents keep working

Total time: 2-5 minutes, a few times per day.

That's the goal. Human provides judgment. Agents provide labor. The tool makes the handoff seamless.

---

*End of Human Workflow Specification*
