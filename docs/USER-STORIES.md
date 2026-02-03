# User Stories & Scenarios

**Purpose:** Concrete examples of how AI Hub gets used. These inform feature priorities and UX decisions.

---

## Personas

### Sam (Solo Operator)
- Runs 12 AI agents on OpenClaw
- Technical founder, night owl
- Wants oversight without micromanagement
- Values: "Don't waste my attention"

### Alex (Small Team Lead)
- 3-person AI-first startup
- Each person oversees a domain of agents
- Needs visibility into what colleagues' agents are doing
- Values: "I shouldn't have to ask what's happening"

### Jordan (Agent Developer)
- Building custom agents on various runtimes
- Integrating with AI Hub
- Values: "Let me ship fast, don't make me read docs for hours"

---

## Story 1: Morning Check-In

**As Sam, I want to check my agents' status in under 2 minutes so I can start my day knowing what needs me.**

### Scenario

```
8:00 AM — Sam opens laptop, goes to AI Hub

Dashboard shows:
- 🔴 ItsAlive (1 item needs you)
- 🟢 Pearl (cranking, 4 active tasks)
- 🟢 Three Stones (1 active)
- 🟡 sam.blog (blocked on design assets)
- ⚪ Content (idle)
- ⚪ Markets (idle, waiting for market open)

Sam sees the red badge, clicks ItsAlive card.

Human Inbox shows:
- "Approve production deploy?" from Ivy (5h waiting)

Sam reads context: "All tests pass. Staging verified."
Clicks [Approve].

Toast: "✅ Deploy approved — Ivy notified"
ItsAlive card turns green.

Time elapsed: 45 seconds.
```

### Acceptance Criteria

- [ ] Dashboard loads in <1 second
- [ ] Red items sorted to top
- [ ] One-click approval from Inbox
- [ ] Agent notified instantly on approval
- [ ] Card status updates without refresh

---

## Story 2: Making a Decision

**As Sam, I want to make quick decisions without losing context so agents can keep moving.**

### Scenario

```
Derek is working on Pearl API design.
Hits a fork: URL versioning vs header versioning.

Derek calls:
POST /human/request
{
  "type": "decision",
  "summary": "API versioning strategy?",
  "options": [
    {"id": "url", "label": "URL Path (/v1/...)"},
    {"id": "header", "label": "Header versioning"}
  ],
  "context": "URL is more visible, header is cleaner..."
}

Sam's Inbox updates (no page refresh needed).

Sam sees the decision request.
Context shows pros/cons Derek outlined.
Sam clicks [URL Path].

Derek receives webhook:
{
  "event": "human.response",
  "response": {"option_id": "url"}
}

Derek continues working immediately.
```

### Acceptance Criteria

- [ ] Decision request appears in real-time
- [ ] Options are clear and clickable
- [ ] Context preserved (agent's reasoning visible)
- [ ] Response delivered to agent in <1 second
- [ ] No ambiguity — decision is final

---

## Story 3: Reviewing Content

**As Sam, I want to review and approve content before it goes public so I maintain quality control.**

### Scenario

```
Stone finishes a blog post draft.

Stone calls:
POST /human/request
{
  "type": "review",
  "summary": "Blog post: 'Why I Run 12 AI Agents'",
  "context": "1,400 words, personal essay style",
  "attachments": [
    {"type": "preview", "url": "https://preview.blog/..."}
  ],
  "options": [
    {"id": "approve", "label": "Approve"},
    {"id": "changes", "label": "Request Changes", "input_type": "text"}
  ]
}

Sam sees review request.
Clicks [Preview →] to open in new tab.
Reads the post.
Clicks [Approve].

Stone receives approval, schedules publication.
```

### Acceptance Criteria

- [ ] Preview link opens in new tab
- [ ] Approval triggers agent immediately
- [ ] "Request Changes" allows text feedback
- [ ] Changes feedback delivered to agent
- [ ] Agent can re-submit for review

---

## Story 4: Handling a Blocker

**As Sam, I want to be notified when agents are stuck on me so I don't become the bottleneck.**

### Scenario

```
Jeff G is working on sam.blog design.
Needs Figma export that only Sam can provide.

Jeff G calls:
POST /tasks/{id}/status
{
  "action": "block",
  "reason": "Need Figma export for hero images",
  "needs_human": true
}

sam.blog project turns yellow (blocked).
Human Inbox gets new item:
- "Unblock: Need Figma export" from Jeff G

Sam sees it next time he checks dashboard.
Sam exports from Figma, uploads to shared drive.
Sam responds: "Uploaded to /assets/hero-images/"

Jeff G receives response, continues work.
sam.blog turns green.
```

### Acceptance Criteria

- [ ] Block status reflects on project card (yellow)
- [ ] Blocked items appear in Inbox
- [ ] Sam can respond with text/instructions
- [ ] Response delivered to agent
- [ ] Project status recalculates when unblocked

---

## Story 5: Ambient Awareness

**As Sam, I want to occasionally see what agents are doing without needing to act on anything.**

### Scenario

```
Sam is curious about progress. Opens Crankfeed tab.

Feed shows:
11:21 — Derek pushed 3 commits to pearl/main
11:19 — Ivy completed itsalive-015
11:15 — Stone marked content post as ready
11:12 — Josh S ran test suite: 675 passing
11:08 — Nova scheduled 3 tweets
...

Sam scrolls, sees the velocity.
No action needed. Closes tab.
Agents keep working.
```

### Acceptance Criteria

- [ ] Feed updates in real-time
- [ ] No notifications from feed items
- [ ] Can filter by project or agent
- [ ] Shows last 24 hours by default
- [ ] Activity is skimmable (no walls of text)

---

## Story 6: Creating a Task

**As Sam, I want to create tasks for agents so I can direct work.**

### Scenario

```
Sam has an idea: "We should add email notifications to ItsAlive"

Sam clicks [+ New Task].

Form:
- Title: "Add email notification system"
- Project: ItsAlive
- Assigned to: Ivy
- Priority: P2
- Context:
  - Files: [empty]
  - Decisions: "Use SendGrid for transactional email"
  - Acceptance: 
    - "Users get email on account creation"
    - "Users get email on deploy completion"

Sam clicks [Create].

Task created, status: queued.
Dependencies: none, so it dispatches immediately.

Ivy receives:
{
  "event": "task.dispatch",
  "task": { ... }
}

Ivy starts working.
```

### Acceptance Criteria

- [ ] Task creation takes <30 seconds
- [ ] Can assign to specific agent
- [ ] Context fields (files, decisions, acceptance) are structured
- [ ] Task dispatches automatically when ready
- [ ] Dependencies can block dispatch

---

## Story 7: Dependency Management

**As Sam, I want to define task dependencies so work happens in the right order.**

### Scenario

```
Sam creates 3 tasks for a feature:
1. "Design email templates" — assigned to Jeff G
2. "Implement email service" — assigned to Derek, depends on #1
3. "Write user documentation" — assigned to Stone, depends on #2

Jeff G gets dispatched #1 immediately.
#2 and #3 stay in queue (dependencies unmet).

Jeff G completes #1.
#2 is now ready → Derek gets dispatched.

Derek completes #2.
#3 is now ready → Stone gets dispatched.
```

### Acceptance Criteria

- [ ] Can set task dependencies during creation
- [ ] Dependent tasks show "blocked" status
- [ ] When blocker completes, dependent auto-dispatches
- [ ] Circular dependencies detected and prevented
- [ ] Dependency graph visualized

---

## Story 8: Multi-Agent Project View

**As Sam, I want to see all tasks across a project regardless of which agent owns them.**

### Scenario

```
Sam clicks into ItsAlive project.

Project view shows:
- Board view: columns for status
- 3 tasks in "In Progress" (Ivy, Derek, Jeff G each have one)
- 2 tasks in "Review"
- 5 tasks in "Done" this week

Sam can see:
- Who's working on what
- Where things are stuck
- Overall project health

Sam doesn't have to ask agents "what are you working on?"
```

### Acceptance Criteria

- [ ] Project view shows all tasks
- [ ] Can filter by status, agent, priority
- [ ] Kanban board for visual overview
- [ ] Task cards show agent avatar
- [ ] Quick stats (velocity, block rate)

---

## Story 9: Mobile Triage

**As Sam, I want to handle urgent items from my phone so I'm not blocked by being away from my laptop.**

### Scenario

```
Sam is at dinner. Phone buzzes:
"🔴 Urgent: Approve hotfix deploy (Ivy)"

Sam opens AI Hub mobile app.
Inbox shows 1 urgent item.

Context: "Critical bug in production. Hotfix ready."

Sam swipes right to approve.
Toast: "Approved"

Ivy gets webhook, deploys hotfix.
Sam goes back to dinner.
```

### Acceptance Criteria

- [ ] Push notification for high/blocking urgency
- [ ] Mobile inbox is fast and minimal
- [ ] Swipe gestures for quick actions
- [ ] Approval works offline (queues until connected)
- [ ] Don't need laptop for urgent items

---

## Story 10: Integrating a New Runtime

**As Jordan (agent developer), I want to integrate my custom agent with AI Hub so I can use the dispatch and human-in-the-loop features.**

### Scenario

```
Jordan has built a custom research agent.
Wants to use AI Hub for task management.

1. Jordan creates Installation in AI Hub
2. Registers agent "researcher"
3. Gets API key and webhook secret

4. Jordan implements webhook handler:
   @app.post("/aihub/webhook")
   def handle_dispatch(payload):
       task = payload["task"]
       research_agent.do_research(task)
       hub.tasks.complete(task["id"])

5. Jordan tests with a manual task create.
   Task dispatches. Agent receives it.
   Agent completes. Task updates in Hub.

Total integration time: 45 minutes.
```

### Acceptance Criteria

- [ ] Agent registration is self-serve
- [ ] API key generated instantly
- [ ] Webhook endpoint configurable
- [ ] Test dispatch available
- [ ] Clear error messages on misconfiguration

---

## Story 11: Team Visibility (Future)

**As Alex (team lead), I want to see what my team's agents are doing so I can coordinate without meetings.**

### Scenario

```
Alex's startup has 3 operators:
- Alex: runs engineering agents
- Sam: runs content agents  
- Chris: runs ops agents

Team Hub shows:
- All projects across all operators
- Inbox shows items assigned to Alex
- Can see (but not act on) others' items

Alex sees Chris's ops agent is blocked.
Slacks Chris: "Hey, your deploy is waiting on approval"

Chris handles it.
```

### Acceptance Criteria

- [ ] Team view shows all projects
- [ ] Per-user inbox (my items only)
- [ ] Read-only visibility into others' items
- [ ] Can configure visibility (private projects)
- [ ] Activity feed shows team-wide

---

## Story 12: End of Day Summary

**As Sam, I want a summary of what happened today so I can track progress without reading logs.**

### Scenario

```
9:00 PM — Sam receives daily email digest:

"AI Hub Daily Summary — Feb 3, 2026

TODAY'S NUMBERS
✅ 23 tasks completed
📥 5 items handled (avg response: 12 minutes)
🤖 8 agents active

HIGHLIGHTS
• Pearl: Test suite now at 675 passing (up from 670)
• ItsAlive: v2.1.0 deployed to production
• Content: 2 blog posts ready for next week

NEEDS ATTENTION (0)
All clear! 🎉

[Open Dashboard]"

Sam skims, satisfied. Closes email.
```

### Acceptance Criteria

- [ ] Daily digest sent at configured time
- [ ] Summary is brief (<20 lines)
- [ ] Highlights automatically extracted
- [ ] Outstanding items called out
- [ ] Can configure digest on/off

---

## Anti-Stories (Things We Don't Do)

### ❌ We don't replace the agent runtime

"I want AI Hub to execute my agents..."

**No.** AI Hub is the work layer, not the execution layer. Agents run in their own runtimes (OpenClaw, Claude Code, etc.). We coordinate, not execute.

### ❌ We don't provide a code editor

"I want to edit code in AI Hub..."

**No.** Use your IDE or the agent's native tools. We track and coordinate work, not do it.

### ❌ We don't do code review

"I want AI Hub to review my agents' code..."

**No.** That's the agent's job (or a code review tool). We flag items that need human review, but we don't do the review.

### ❌ We don't manage agent configuration

"I want to configure my agents' prompts in AI Hub..."

**No.** Agent configuration stays in the runtime. We just need to know where to send tasks and how to identify agents.

---

*End of User Stories*
