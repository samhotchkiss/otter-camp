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

I keep a paper notepad next to my desk. Things I need to tell my agents. Because if a thought hits me while I'm mid-conversation with one of them, it gets distracted, I lose the thread, and I've wrecked two things. So I write it down and wait for the right moment. In 2026. I have a fucking fleet of AI agents and I'm using a paper notepad so I don't confuse them.

My engineering lead made an architecture decision on Monday. Compacted overnight. Wednesday it makes the same mistake because it has no idea Monday happened. My content writer contradicts a post from last week — doesn't remember writing it. My personal assistant forgot what day my fat ass is supposed to take my Ozempic. Knew it Tuesday. Compacted. Gone.

Three of my agents are blocked on me right now. I didn't know until I went looking. Their questions are in different chat windows, nothing flagged them. I'm the bottleneck and I can't even tell. Two more have died midstream, and I have to figure out how to restart them.

Everyone posts about how many agents they're running. How much cool shit they're building. Nobody posts about this part.

---

We (me and my gang of clankers) are building Otter Camp. Open source, runs on your machine, adds actual method to the madness.

## How It Works

Otter Camp layers on top of [OpenClaw](https://github.com/openclaw/openclaw) — the runtime that handles LLMs, file system, skills, message routing. Claw runs agents. Otter runs the team. Install it, it pulls in your existing agents, manages everything from there. Two new agents get created: **Chameleon** and **Elephant**.

### 🦎 Chameleon

Adding an agent to my current setup is 30 minutes of config editing. System prompt, personality, workspace, tools, channels.

Chameleon stores identities in a database. New engineer? Social media person? Someone to manage your family calendar? Create them in the UI, pick a profile, customize. Thirty seconds. Done with one? One click. Fired.

Identities are just data, so you can clone them. Need to parallelize? Five copies of your writer, five blog posts at once. Nine women can't make a baby in one month, but they can make nine babies in nine months. Only bottleneck is what depends on what, not who's available.

Otter tracks what each agent ships, what gets rejected, how they handle feedback. You can run actual performance reviews and tune them.

### 📋 Projects & Issues

Code, blog posts, books, tweets, trading strategies, meal plans — everything lives in a project, everything in Git. Issues go plan → build → review → ship. Same flow for a feature and a grocery list.

Everything is version controlled because progress should be non-destructive. Agents can try stuff and you don't have to be afraid. Designer ships something bad at 2am, roll it back. Writer nukes a good draft, it's still there. You can always undo.

### 💬 Scoped Conversations

Discussions live where they belong — within an issue, a project, or org-wide. Your feedback on the blog draft stays with the blog draft. Doesn't bleed into unrelated context that's about to compact.

Thought pops into your head? Drop it on otter, it'll get filed as an issue within the right project, ready to flesh out when you are. Don't interrupt what you're working on. ADHD brain's best friend.

### 🔄 Review Loops

Nothing ships unchecked. Code, content, designs — reviewed before merge. Agents review each other, or you can choose to add human checkpoints. Every decision has a trail.

### 🐘 Elephant

The memory agent. (They never forget.)

Every five minutes, scans everything — conversations, commits, decisions, all of it. Two jobs:

**Remembering.** You told your assistant about the peanut allergy last Tuesday. Elephant caught it, stored it. Agent compacts and restarts, gets the memory back. Every agent gets persistent memory automatically.

**Sharing.** Your engineer finds a bad API pattern — the designer needs to know, the docs agent needs to know. You mention you're training for a marathon — your meal planner needs that. Elephant figures out who needs what and puts it in front of them. Targeted, not broadcast.

**Enforcing.** Elephant makes sure your agents follow through on what they say they're going to do. When a commitment is made it's captured and tracked to completion.

## Running in Production

I'm running this in production. The Otter Camp codebase was built by the agents it manages — 700+ commits and 150k lines of code in 9 days.

## Get Started

```bash
git clone https://github.com/samhotchkiss/otter-camp
cd otter-camp
make setup    # DB, CLI, auth token
make dev      # API + frontend

# Connect to your OpenClaw instance
make bridge
```

Otter runs on your machine. Data stays local. Open source. Your agents touch your files, calendar, messages, and you have control over that. If you want it hosted, that's an option, too.

We ship curated agent profiles so you don't have to build from scratch. Engineering, content, design, research, personal ops. Pick one, tweak it, go.

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

Native iOS and iPad apps coming next. Manage your agents from your phone. Just connect to your Mac Mini with TailScale and you've got native control.

Alpha will be ready this week for those out there who are fucking masochists. Should be good enough for your Gramma by the end of the month.

---

Thanks to: [@steve_yegge](https://x.com/steve_yegge) [@steipete](https://x.com/steipete) [@alexfinn](https://x.com/alexfinn) [@delba_oliveira](https://x.com/delba_oliveira) [@m_0_r_g_a_n_](https://x.com/m_0_r_g_a_n_) [@techNmak](https://x.com/techNmak) [@leonabboud](https://x.com/leonabboud) [@ericosiu](https://x.com/ericosiu)

## Links

- [otter.camp](https://otter.camp) — Homepage
- [OpenClaw](https://github.com/openclaw/openclaw) — The runtime
- [Discord](https://discord.gg/clawd) — Community

## License

MIT
