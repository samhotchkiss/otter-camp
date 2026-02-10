<p align="center">
  <img src="branding/illustrations/clean/otters-sailing-clean.png" alt="Otter Camp" width="400">
</p>

<h1 align="center">🦦 Otter Camp</h1>

<p align="center">
  <strong>Open source work management for AI agent teams.</strong>
</p>

<p align="center">
  <a href="https://otter.camp">Website</a> ·
  <a href="https://discord.gg/clawd">Discord</a> ·
  <a href="#get-started">Get Started</a>
</p>

---

Everyone's running AI agents. Nobody talks about managing them — the forgotten context, the blocked queues you don't know about, the dead sessions nobody flagged.

Otter Camp layers on top of [OpenClaw](https://github.com/openclaw/openclaw). Claw runs agents. Otter runs the team.

## What It Does

🧠 **Agents remember everything.** Context compacts overnight. Otter makes sure nothing gets lost. What your assistant learned Tuesday is still there Friday.

⚡ **Hire and fire in seconds.** Spin up a new agent in 30 seconds from the UI. Need five writers for five blog posts? Clone one. Done with an agent? One click, gone.

📋 **One pipeline for all work.** Code, blog posts, meal plans — everything in Git, same flow. Plan → build → review → ship. Version controlled, so you can always undo.

💬 **Context stays where it belongs.** Blog feedback lives with the blog. Engineering stays in engineering. Drop a thought anywhere, it gets filed in the right project.

🔄 **Nothing ships unchecked.** Agents review each other's work. Add human checkpoints where you want them. Full audit trail.

📊 **Know what your team is doing.** Track what each agent ships, what gets rejected, how they handle feedback. Run actual performance reviews.

🔒 **Your data, your machine.** Open source, self-hosted, no cloud dependency. Hosted option available if you want it.

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

## Links

- [otter.camp](https://otter.camp) — Homepage
- [OpenClaw](https://github.com/openclaw/openclaw) — The runtime
- [Discord](https://discord.gg/clawd) — Community

## License

MIT
