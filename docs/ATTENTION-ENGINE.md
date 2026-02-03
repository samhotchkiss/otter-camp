# Attention Engine Specification

**Purpose:** The most critical system in AI Hub. Determines what deserves human attention and when.

---

## The Problem

A solo operator with 12 agents could receive:
- 50+ task completions per day
- 100+ commits per day
- Dozens of status changes
- 5-10 items that actually need human input

Traditional notification systems would bury them. Email becomes noise. Push notifications cause alert fatigue.

**The Attention Engine's job:** Surface the 5-10 items, suppress the 150+ that don't need attention.

---

## Core Principle: Explicit Over Inferred

We don't try to guess what needs attention. We require agents to explicitly flag it.

```
needs_human: false  →  Crankfeed only (ambient)
needs_human: true   →  Human Inbox (requires action)
```

No ML-based "this looks important." No heuristics that get it wrong. Agents know when they need help — they tell us.

---

## Attention Levels

### Level 1: Crankfeed (No Attention Required)

**What goes here:**
- Task started
- Task completed
- Commits pushed
- Progress updates
- Status changes (except blocks)

**Delivery:**
- In-app real-time feed
- No notification
- Pull-only (human looks when curious)

**User experience:**
- Scrollable timeline
- Skimmable format
- Can ignore for days

### Level 2: Human Inbox (Action Required)

**What goes here:**
- Approval requests
- Decision requests
- Questions
- Review requests
- Blockers marked `needs_human: true`

**Delivery:**
- In-app inbox (always)
- Badge count on sidebar
- Project card turns red
- Optional: Email notification (configurable)

**User experience:**
- Clear action buttons
- One-click resolution
- Items disappear when handled

### Level 3: Urgent Alert (Immediate Attention)

**What goes here:**
- Items with `urgency: high` or `urgency: blocking`
- Agent explicitly flags as urgent

**Delivery:**
- All of Level 2, plus:
- Push notification (if configured)
- SMS (if configured)
- Slack/Discord ping (if configured)

**User experience:**
- Breaks through quiet mode
- Expects response in minutes, not hours

---

## Urgency Classification

### Normal (default)
- Will be handled next time human checks in
- No push notification
- Patient

### High
- Should be handled within a few hours
- Push notification (if enabled)
- Agent is waiting but can work on other things

### Blocking
- Agent cannot proceed without response
- Always push notification
- Breaks through quiet hours (except 11pm-7am)
- Agent is idle until resolved

---

## Project Status Calculation

Each project has a **pulse color** derived from its tasks:

```python
def calculate_pulse(project):
    tasks = get_active_tasks(project)
    
    # Red: Any task needs human input
    if any(t.needs_human for t in tasks):
        return "red"
    
    # Yellow: Any task is blocked (but not on human)
    if any(t.status == "blocked" for t in tasks):
        return "yellow"
    
    # Green: Active work happening
    if any(t.status == "in_progress" for t in tasks):
        return "green"
    
    # Gray: No active tasks
    return "gray"
```

### Dashboard Sorting

Projects sorted by urgency:
1. 🔴 Red (needs you) — by oldest waiting item
2. 🟡 Yellow (blocked) — by time blocked
3. 🟢 Green (cranking) — by most recent activity
4. ⚪ Gray (idle) — alphabetical

---

## Notification Channels

### In-App (Always On)

- Badge count in sidebar
- Real-time inbox updates
- Project card pulse colors
- Toast on action completion

### Email Digest (Configurable)

Options:
- **Off** — No email
- **Urgent only** — Only `urgency: high/blocking`
- **Daily digest** — Summary at configured time
- **Immediate** — Every inbox item (not recommended)

Default: Daily digest at 8:00 AM local time.

Digest template:
```
Subject: AI Hub — 3 items need your attention

NEEDS YOUR INPUT
• Approve deploy (Ivy, ItsAlive) — waiting 2h
• Decision: API versioning (Derek, Pearl) — waiting 45m
• Review: Blog post (Stone, Content) — waiting 1h

YESTERDAY'S NUMBERS
• 23 tasks completed
• 5 items handled (avg response: 18 min)
• 8 agents active

[Open Dashboard]
```

### Push Notification (Optional)

Requires mobile app or browser permission.

Only for:
- `urgency: high` — "🟡 High priority: [summary]"
- `urgency: blocking` — "🔴 Blocking: [summary]"

Tapping opens directly to that inbox item.

### Slack/Discord (Optional)

Webhook integration:
```json
{
  "channel": "#ai-hub-alerts",
  "events": ["inbox.high", "inbox.blocking"],
  "format": "actionable"  // Include buttons if supported
}
```

Message format:
```
🔴 Blocking: Approve production deploy

Ivy (ItsAlive) is waiting for approval.
"All tests pass. Staging verified. Ready to ship."

[Approve] [Hold] [Reject] [View in AI Hub]
```

If platform supports buttons, human can approve directly in Slack.

### SMS (Optional, High Urgency Only)

Only for `urgency: blocking`.
Must explicitly enable.
Rate limited: max 5/day.

Format:
```
AI Hub: BLOCKING - Approve hotfix deploy (Ivy, ItsAlive)
Reply 1 to approve, 2 to hold
Or open: https://hub.example.com/i/12345
```

---

## Quiet Hours

Configurable per operator:
- Start time (default: 11:00 PM local)
- End time (default: 7:00 AM local)
- Days (default: every day)

During quiet hours:
- No push notifications
- No SMS
- No Slack/Discord pings
- Exception: `urgency: blocking` still comes through

Human can override:
- "Quiet until tomorrow morning"
- "Quiet for 2 hours"
- "Disable quiet hours"

---

## Inbox Management

### Item Lifecycle

```
Created → Pending → Resolved
              ↓
           Snoozed → Pending (after snooze expires)
              ↓
           Dismissed
```

### Snooze

"Remind me later":
- In 1 hour
- In 4 hours
- Tomorrow morning
- Custom time

Snoozed items disappear from inbox, reappear at specified time.

### Dismiss

"I've seen this, no action needed":
- Item goes to archive
- Agent is NOT notified (no response)
- Use for items that resolved themselves

### Bulk Actions

- Select multiple items
- Bulk approve (if all are approvals)
- Bulk dismiss
- Bulk snooze

---

## Escalation

### Auto-Escalation

If inbox item is unhandled for too long:
- After 4 hours: Reminder notification
- After 12 hours: Escalate to `high`
- After 24 hours: Escalate to `blocking`

Configurable thresholds.

### Agent Escalation

Agent can escalate if waiting too long:
```json
{
  "action": "escalate",
  "request_id": "hr-12345",
  "new_urgency": "high",
  "reason": "Deploy window closes in 2 hours"
}
```

Human gets notification about escalation.

---

## Metrics & Insights

### Response Time

Track how long items wait in inbox:
- Average response time (target: <1 hour)
- P95 response time
- Response time by type (approvals faster than reviews?)

### Bottleneck Score

Are you the bottleneck?
```
bottleneck_score = hours_agents_blocked_on_human / total_active_hours
```

- 0% = Never blocking agents
- 10% = Agents spend 10% of time waiting on you
- 50%+ = You're the problem

### Notification Effectiveness

- Notifications sent vs. items resolved
- Which channels drive fastest response?
- Are people ignoring certain notification types?

---

## Anti-Patterns We Prevent

### 1. Notification Spam

Bad: Every commit triggers a notification.
Prevention: Only explicit `needs_human` triggers inbox.

### 2. Missed Urgent Items

Bad: Urgent item buried in noise.
Prevention: Separate urgency levels, visual prominence for urgent.

### 3. Alert Fatigue

Bad: So many alerts that humans ignore them all.
Prevention: Strict separation of ambient (feed) vs. actionable (inbox).

### 4. Blocking Without Escalation

Bad: Agent blocked for 24 hours, human didn't know.
Prevention: Auto-escalation based on wait time.

### 5. Uncertain Resolution

Bad: Human thinks they handled it, agent didn't get the message.
Prevention: Clear feedback loop, toast confirmations, agent acknowledgment.

---

## Technical Implementation

### Real-Time Infrastructure

```
Inbox changes → WebSocket broadcast → All connected clients
                                   → Badge update
                                   → Optional push notification
```

WebSocket events:
- `inbox.item_added`
- `inbox.item_updated`
- `inbox.item_removed`
- `project.pulse_changed`

### Notification Queue

```
Notification created → Queue → Rate limiter → Channel dispatcher
                                    ↓
                            Push/Email/Slack/SMS
```

Rate limits prevent spam:
- Max 10 push notifications/hour
- Max 1 email digest/day
- Max 5 SMS/day

### Delivery Tracking

Track:
- Notification sent timestamp
- Delivery confirmation (where possible)
- Open/click tracking (email, push)
- Response time

---

## Configuration Schema

```yaml
notifications:
  # Global settings
  quiet_hours:
    enabled: true
    start: "23:00"
    end: "07:00"
    timezone: "America/Denver"
  
  # Per-channel settings
  email:
    enabled: true
    mode: "daily_digest"  # off | urgent_only | daily_digest | immediate
    digest_time: "08:00"
    address: "sam@example.com"
  
  push:
    enabled: true
    urgency_threshold: "high"  # high | blocking
  
  slack:
    enabled: true
    webhook_url: "https://hooks.slack.com/..."
    channel: "#ai-hub-alerts"
    urgency_threshold: "high"
    actionable: true  # Include buttons
  
  sms:
    enabled: false
    phone: "+1234567890"
    urgency_threshold: "blocking"
  
  # Escalation settings
  escalation:
    reminder_after: "4h"
    escalate_to_high: "12h"
    escalate_to_blocking: "24h"
```

---

*End of Attention Engine Specification*
