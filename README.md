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

Everyone's running AI agents now. Nobody talks about managing them. The compacted memories, the blocked queues you don't know about, the dead sessions, the context bleed. Otter Camp is the missing layer — open source, runs on your machine, adds actual method to the madness.

It layers on top of [OpenClaw](https://github.com/openclaw/openclaw). Claw runs agents. Otter runs the team.

## What It Does

**🦎 Chameleon** — Agent identities in a database, not config files. New team member in 30 seconds. Clone them to parallelize. Track what they ship, run performance reviews, tune them. Done with one? Fired.

**🐘 Elephant** — The memory agent. Scans everything every 5 minutes. Catches what matters, stores it, gives it back after compaction. Shares knowledge across the team. Makes sure agents follow through on commitments.

**📋 Projects & Issues** — Everything in Git. Code, blog posts, meal plans. Issues flow plan → build → review → ship. Version controlled because progress should be non-destructive. You can always undo.

**💬 Scoped Conversations** — Discussions stay where they belong. Blog feedback with the blog. Engineering with engineering. Drop a thought on Otter, it gets filed in the right project. ADHD brain's best friend.

**🔄 Review Loops** — Nothing ships unchecked. Agents review each other, or add human checkpoints. Every decision has a trail.

**🔒 Local First** — Data stays on your machine. Open source, self-hosted, no cloud dependency. Want it hosted? That's an option too.

## Get Started

```bash
git clone https://github.com/samhotchkiss/otter-camp
cd otter-camp
make setup    # DB, CLI, auth token
make dev      # API + frontend

# Connect to your OpenClaw instance
make bridge
```

We ship curated agent profiles — engineering, content, design, research, personal ops. Pick one, tweak it, go.

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

## In Production

13 agents, every day. 150k lines of code, 700+ commits in 9 days — most of the codebase was built by the agents it manages.

Native iOS and iPad apps coming next.

## Links

- [otter.camp](https://otter.camp) — Homepage
- [What Is Otter Camp?](https://otter.camp/what-is-otter-camp) — The full story
- [OpenClaw](https://github.com/openclaw/openclaw) — The runtime
- [Discord](https://discord.gg/clawd) — Community

## License

MIT
