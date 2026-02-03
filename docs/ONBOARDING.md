# Onboarding Flow Specification

**Purpose:** First-time user experience. Get from signup to first task dispatched in <10 minutes.

---

## Guiding Principles

1. **Show value fast** — First dispatched task within 10 minutes
2. **Don't overwhelm** — Hide advanced features until needed
3. **Assume technical** — Our users can handle APIs and webhooks
4. **Celebrate wins** — Acknowledge each step completion

---

## Onboarding Phases

```
Signup → Install Setup → First Agent → First Task → First Dispatch → Done!
  │         │               │             │              │
  1m        2m              3m            2m             2m = ~10 minutes
```

---

## Phase 1: Signup (1 minute)

### Landing Page → Signup

User arrives at landing page, clicks "Get Started"

### Signup Form

Minimal fields:
```
┌────────────────────────────────────────────┐
│  Create your AI Hub account                │
│                                            │
│  Email: [________________________]         │
│                                            │
│  Password: [____________________]          │
│                                            │
│  [ ] I agree to Terms and Privacy Policy   │
│                                            │
│  [Create Account]                          │
│                                            │
│  ─────────── or ───────────                │
│                                            │
│  [Continue with GitHub]                    │
│  [Continue with Google]                    │
└────────────────────────────────────────────┘
```

**No:**
- Company name
- Team size
- Use case survey
- Credit card

### Email Verification (Optional for MVP)

Skip email verification initially. Verify later for production features (webhooks, etc.).

---

## Phase 2: Installation Setup (2 minutes)

After signup, user lands on setup wizard.

### Step 2.1: Name Your Installation

```
┌────────────────────────────────────────────┐
│  Welcome! Let's set up your AI Hub.        │
│                                            │
│  What should we call your installation?    │
│                                            │
│  Name: [sam-openclaw____________]          │
│                                            │
│  This is just for your reference.          │
│  You can change it later.                  │
│                                            │
│  [Continue →]                              │
└────────────────────────────────────────────┘
```

Default: Derived from email (e.g., `sam-hub`)

### Step 2.2: Get Your API Key

```
┌────────────────────────────────────────────┐
│  Here's your API key                       │
│                                            │
│  ┌──────────────────────────────────────┐  │
│  │ aihub_sk_a1b2c3d4e5f6g7h8i9j0k1l2m3 │  │
│  │                                [Copy] │  │
│  └──────────────────────────────────────┘  │
│                                            │
│  ⚠️ Save this! We won't show it again.     │
│                                            │
│  You'll use this key to:                   │
│  • Connect agent runtimes                  │
│  • Make API calls                          │
│  • Authenticate webhooks                   │
│                                            │
│  [I've saved my key →]                     │
└────────────────────────────────────────────┘
```

Key is shown once. User must acknowledge they've saved it.

---

## Phase 3: First Agent (3 minutes)

### Step 3.1: Create Your First Agent

```
┌────────────────────────────────────────────┐
│  Create your first agent                   │
│                                            │
│  Agents are identities for your AI         │
│  workers. They're not accounts — they're   │
│  more like signatures on your work.        │
│                                            │
│  Agent ID: [derek_______________]          │
│  (lowercase, no spaces)                    │
│                                            │
│  Display Name: [Derek______________]       │
│                                            │
│  Role (optional): [Engineering Lead_]      │
│                                            │
│  [Create Agent →]                          │
│                                            │
│  ────────────────────────────────────────  │
│  💡 Examples: derek, ivy, stone, nova      │
└────────────────────────────────────────────┘
```

### Step 3.2: Configure Agent Webhook

```
┌────────────────────────────────────────────┐
│  Where should we send tasks for Derek?     │
│                                            │
│  Webhook URL:                              │
│  [https://your-runtime.com/aihub________]  │
│                                            │
│  This is where AI Hub will POST tasks      │
│  when they're ready for Derek.             │
│                                            │
│  Don't have a webhook yet? No problem.     │
│  [Skip for now]                            │
│                                            │
│  [Test Webhook →]  [Save →]                │
└────────────────────────────────────────────┘
```

**Test Webhook** sends a test payload and shows success/failure.

### OpenClaw Users: Quick Setup

If user indicates they use OpenClaw:

```
┌────────────────────────────────────────────┐
│  Using OpenClaw? Here's your config:       │
│                                            │
│  Add this to your openclaw.json:           │
│                                            │
│  ┌──────────────────────────────────────┐  │
│  │ {                                    │  │
│  │   "plugins": {                       │  │
│  │     "aihub": {                       │  │
│  │       "enabled": true,               │  │
│  │       "apiKey": "aihub_sk_...",      │  │
│  │       "hubUrl": "https://hub.ai"     │  │
│  │     }                                │  │
│  │   }                                  │  │
│  │ }                                    │  │
│  └──────────────────────────────────────┘  │
│                                [Copy]      │
│                                            │
│  [I've added the config →]                 │
└────────────────────────────────────────────┘
```

---

## Phase 4: First Task (2 minutes)

### Step 4.1: Create a Test Task

```
┌────────────────────────────────────────────┐
│  Create your first task                    │
│                                            │
│  Let's create a simple task to see how     │
│  dispatch works.                           │
│                                            │
│  Title: [Say hello_________________]       │
│                                            │
│  Assigned to: [Derek ▼]                    │
│                                            │
│  Instructions:                             │
│  [Just respond with "Hello, AI Hub!"       │
│   to confirm you received this task._____] │
│                                            │
│  [Create & Dispatch →]                     │
└────────────────────────────────────────────┘
```

### Step 4.2: Watch It Dispatch

```
┌────────────────────────────────────────────────────────┐
│  Task created! Dispatching to Derek...                 │
│                                                        │
│  ✅ Task created                                       │
│  ✅ Webhook sent to https://your-runtime.com/aihub    │
│  ⏳ Waiting for Derek to acknowledge...               │
│                                                        │
│  ────────────────────────────────────────────────────  │
│                                                        │
│  Webhook payload:                                      │
│  ┌──────────────────────────────────────────────────┐ │
│  │ {                                                │ │
│  │   "event": "task.dispatch",                      │ │
│  │   "task": {                                      │ │
│  │     "id": "hello-001",                           │ │
│  │     "title": "Say hello",                        │ │
│  │     ...                                          │ │
│  │   }                                              │ │
│  │ }                                                │ │
│  └──────────────────────────────────────────────────┘ │
│                                                        │
│  [View task →]                                         │
└────────────────────────────────────────────────────────┘
```

If webhook is not configured, show simulated dispatch with instructions for manual testing.

---

## Phase 5: First Dispatch Complete (2 minutes)

### Success State

When Derek completes the task:

```
┌────────────────────────────────────────────┐
│  🎉 First task complete!                   │
│                                            │
│  Derek said: "Hello, AI Hub!"              │
│                                            │
│  You've successfully:                      │
│  ✅ Created an installation                │
│  ✅ Set up an agent                        │
│  ✅ Dispatched a task                      │
│  ✅ Received a completion                  │
│                                            │
│  You're ready to go!                       │
│                                            │
│  [Go to Dashboard →]                       │
│                                            │
│  ─────────── or ───────────                │
│                                            │
│  [Add more agents]                         │
│  [Create a project]                        │
│  [Read the docs]                           │
└────────────────────────────────────────────┘
```

---

## Onboarding Checklist (Persistent)

After onboarding, show a checklist in the sidebar until complete:

```
┌────────────────────────────────────────────┐
│  Getting Started                           │
│                                            │
│  ✅ Create account                         │
│  ✅ Get API key                            │
│  ✅ Add first agent                        │
│  ✅ Dispatch first task                    │
│  ☐ Create a project                        │
│  ☐ Request human input                     │
│  ☐ Set up notifications                    │
│                                            │
│  [Dismiss checklist]                       │
└────────────────────────────────────────────┘
```

Checklist items link to relevant actions/docs.

---

## Empty States

### No Projects Yet

```
┌────────────────────────────────────────────┐
│                                            │
│            📁                              │
│                                            │
│  No projects yet                           │
│                                            │
│  Projects group related tasks and repos.   │
│  Create one to organize your work.         │
│                                            │
│  [+ Create Project]                        │
│                                            │
│  ─────────────────────────────────────     │
│  💡 Example projects: "ItsAlive",          │
│     "Pearl", "Content"                     │
└────────────────────────────────────────────┘
```

### No Tasks in Project

```
┌────────────────────────────────────────────┐
│                                            │
│            📋                              │
│                                            │
│  No tasks in ItsAlive                      │
│                                            │
│  Create a task to get your agents working. │
│                                            │
│  [+ Create Task]                           │
│                                            │
│  ─────────────────────────────────────     │
│  💡 Tasks are dispatched to agents         │
│     automatically when ready.              │
└────────────────────────────────────────────┘
```

### Empty Inbox

```
┌────────────────────────────────────────────┐
│                                            │
│            ✨                              │
│                                            │
│  Inbox Zero                                │
│                                            │
│  Nothing needs your attention right now.   │
│  Your agents are cranking.                 │
│                                            │
│  [View Crankfeed →]                        │
└────────────────────────────────────────────┘
```

---

## Onboarding Analytics

Track:
- Time to first agent created
- Time to first task dispatched
- Time to first task completed
- Drop-off at each step
- Completion rate

Goals:
- 80% complete onboarding
- <10 minutes average completion time
- <20% drop-off at any single step

---

## Re-engagement

### Day 1 (if not active)

Email: "Your agents are waiting"

```
Subject: Your AI Hub setup is almost complete

Hey Sam,

You created your AI Hub account yesterday but 
haven't dispatched any tasks yet.

Need help? Here's what most people do next:

1. Connect your agent runtime
2. Create your first task
3. Watch the magic happen

[Complete Setup →]

Or reply to this email — I'm here to help.

— The AI Hub Team
```

### Day 3 (if no tasks)

Email: "Quick tip: Start small"

```
Subject: Start with a test task

Hey Sam,

Lots of people get stuck on "what task should 
I create first?"

Here's a secret: it doesn't matter. Create a 
simple test task like "Say hello" just to see 
the dispatch flow work.

Once you see it work, you'll know exactly 
how to use it for real work.

[Create a test task →]
```

### Day 7 (if no activity)

Email: "We're here if you need us"

```
Subject: Need help with AI Hub?

Hey Sam,

It's been a week since you signed up, and we 
noticed you haven't dispatched any tasks yet.

If you're stuck, we'd love to help:

• Book a 15-min setup call [link]
• Join our Discord community [link]
• Read the quickstart guide [link]

Or just reply to this email.

No pressure — we're here when you're ready.

— The AI Hub Team
```

---

## Upgrade Prompts (Post-Onboarding)

### Free Tier Limits

When approaching limits:

```
┌────────────────────────────────────────────┐
│  You're using 4 of 5 agents                │
│                                            │
│  Upgrade to Pro for unlimited agents       │
│  and priority support.                     │
│                                            │
│  [Upgrade to Pro — $25/mo]  [Maybe later]  │
└────────────────────────────────────────────┘
```

### After 7 Days of Active Use

```
┌────────────────────────────────────────────┐
│  You've completed 47 tasks this week! 🎉   │
│                                            │
│  Ready to level up? Pro includes:          │
│  • Unlimited agents                        │
│  • Unlimited projects                      │
│  • Priority webhooks                       │
│  • Email support                           │
│                                            │
│  [Upgrade to Pro — $25/mo]                 │
└────────────────────────────────────────────┘
```

---

*End of Onboarding Specification*
