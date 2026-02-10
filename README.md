<p align="center">
  <img src="branding/illustrations/clean/otters-sailing-clean.png" alt="Otter Camp" width="400">
</p>

<h1 align="center">🦦 Otter Camp</h1>

<p align="center">
  <strong>Basecamp + GitHub + Slack for a world where AI is part of the team, not just a tool.</strong>
</p>

<p align="center">
  <a href="https://otter.camp">Website</a> ·
  <a href="https://discord.gg/clawd">Discord</a> ·
  <a href="#get-started">Get Started</a>
</p>

---

I have 13 AI agents. They do my engineering, write my content, manage my email, watch markets while I sleep, help me plan meals, keep track of my kids' schedules. It's how I work now.

I keep a paper notepad next to my desk. Things I need to tell my agents. Because if a thought hits me while I'm mid-conversation with one of them, it gets distracted, I lose the thread, and I've wrecked two things. So I write it down and wait. In 2026. Paper notepad.

My engineering lead made an architecture decision on Monday. Compacted overnight. Wednesday it makes the same mistake because it has no idea Monday happened. My personal assistant forgot my daughter's peanut allergy. Knew it Tuesday. Compacted. Wednesday it's recommending pad thai with crushed peanuts.

Three of my agents are blocked on me right now. I didn't know until I went looking. Their questions are in different chat windows, nothing flagged them.

Everyone posts about how many agents they're running. Nobody posts about this part.

We're building Otter Camp. Open source, runs on your machine. Other people are working on this problem too — we think ours is the best.

## How It Works

Otter Camp layers on top of [OpenClaw](https://github.com/openclaw/openclaw) — the runtime that handles LLMs, file system, skills, message routing. Claw runs agents. Otter runs the team. Install it, it pulls in your existing agents, manages everything from there.

Two new agents get created: **Chameleon** and **Elephant**.

### 🦎 Chameleon

Adding an agent to my current setup is 30 minutes of config editing. System prompt, personality, workspace, tools, channels. Renaming one is worse.

Chameleon stores identities in a database. New engineer? Social media person? Someone to manage your family calendar? Create them in the UI, pick a profile, customize. Thirty seconds. Done with one? One click.

Identities are just data, so you can clone them. Five copies of your writer, five blog posts at once. Nine women can't make a baby in one month, but they can make nine babies in nine months.

### 📋 Projects & Issues

Code, blog posts, books, trading strategies, meal plans — everything lives in a project, everything in Git. Issues go plan → build → review → ship.

Everything version controlled. Progress is non-destructive. Designer ships something bad at 2am, roll it back. Writer nukes a good draft, it's still there.

### 💬 Scoped Conversations

Discussions live where they belong — with an issue, with a project, org-wide. Your feedback on the blog draft stays with the blog draft. Doesn't bleed into unrelated context that's about to compact.

Thought pops into your head? File it as an issue, come back later, tag an agent when you're ready. Don't interrupt what you're working on.

### 🔄 Review Loops

Nothing ships unchecked. Code, content, designs — reviewed before merge. Agents review each other. You can override. Every decision has a trail.

### 🐘 Elephant

The memory agent. Every five minutes, scans everything — conversations, commits, decisions.

**Remembering.** You told your assistant about the peanut allergy last Tuesday. Elephant caught it, stored it. Agent compacts and restarts, gets the memory back. Every agent gets persistent memory automatically.

**Sharing.** Your engineer finds a bad API pattern — the designer needs to know, the docs agent needs to know. Elephant figures out who needs what and puts it in front of them. Targeted, not broadcast. Your team gets smarter together.

## Running in Production

We run this in production. 13 agents, every day. ~143k lines of code. Most of the codebase was built by the agents it manages — 700+ commits in 9 days.

```
Go:   82k lines
TSX:  44k lines
TS:   14k lines
SQL:   2k lines
CSS:  1.7k lines
```

## Get Started

```bash
git clone https://github.com/samhotchkiss/otter-camp
cd otter-camp
make setup    # DB, CLI, auth token
make dev      # API + frontend

# Connect to your OpenClaw instance
make bridge
```

### Agent Profiles

You don't have to build agents from scratch. We ship curated profiles — engineering, content, design, research, personal ops. Pick one, tweak it, go.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                  YOU (Operator)                   │
│              Browser → otter.camp                │
└───────────────────────┬─────────────────────────┘
                        │
                ┌───────┴───────┐
                │  Otter Camp   │
                │               │
                │  Go API       │
                │  React UI     │
                │  PostgreSQL   │
                │  Git repos    │
                └───────┬───────┘
                        │ Bridge (WebSocket)
                ┌───────┴───────┐
                │   OpenClaw    │
                │               │
                │  LLM routing  │
                │  File system  │
                │  Skills       │
                │  Channels     │
                └───────────────┘
```

Runs locally on your machine. Data stays local. Open source.

Native iOS and iPad apps coming in Phase 2.

## Links

- [otter.camp](https://otter.camp) — Homepage & waitlist
- [OpenClaw](https://github.com/openclaw/openclaw) — The runtime
- [Discord](https://discord.gg/clawd) — Community

## License

MIT
