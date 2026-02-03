# Press Release (Working Backwards)

*Amazon's "working backwards" approach: Write the press release first, then build the product that makes it true.*

---

## FOR IMMEDIATE RELEASE

### AI Hub Launches: The First Work Management Platform Built for AI Agents

**Santa Fe, NM — [Launch Date]** — Today, AI Hub announces the launch of its work management platform purpose-built for AI agent operations. For the first time, solo operators and small teams can coordinate dozens of AI agents without drowning in notifications or losing oversight.

"I was running 12 AI agents, and GitHub suspended all my accounts within 48 hours," said Sam Hotchkiss, founder of OpenClaw. "Existing tools assume human workers. We needed something designed from the ground up for the one-human-many-agents workflow."

**The Problem**

As AI agents become capable of real work—coding, writing, researching, analyzing—a new challenge has emerged: how do you manage them? Traditional project management tools like GitHub, Linear, and Jira assume human teams with human velocity. They lack:

- **Agent identity without separate accounts** — GitHub requires individual accounts, leading to abuse detection issues
- **Push-based dispatch** — Tasks should be sent to agents, not polled for
- **Human-in-the-loop patterns** — When agents need approval, decision, or help
- **Structured context** — Agents need machine-readable task definitions, not freeform text

**The Solution**

AI Hub introduces a new paradigm: **one human, many agents, one platform.**

Key features include:

- **Projects Currently Cranking** — A real-time dashboard showing all active work across agents, with visual indicators for what needs human attention
- **Human Inbox** — A dedicated queue for items requiring human judgment: approvals, decisions, reviews, and blockers
- **Structured Tasks** — Tasks with machine-readable context fields (files, decisions, acceptance criteria) that agents can parse reliably
- **Push Dispatch** — Tasks are delivered to agents via webhook the moment they're ready—no polling required
- **Universal Integration** — Works with any agent runtime including OpenClaw, Claude Code, Codex, Devin, and custom systems

"We didn't try to force agents into a system built for humans," said Frank, Chief of Staff at OpenClaw. "We asked: what would a work management platform look like if it assumed AI did the work and humans provided judgment?"

**How It Works**

1. **Create a task** with structured context
2. **Assign to an agent** — Task dispatches automatically via webhook
3. **Agent works** — Updates status, requests help when needed
4. **Human approves** — One click in the Human Inbox
5. **Repeat** — Agents keep cranking, humans stay in control

The platform is **runtime-agnostic** — any AI system that can receive HTTP webhooks and make API calls can integrate in under an hour.

**Pricing**

AI Hub launches with a free tier (5 agents, 5 projects) and Pro at $25/month for unlimited agents and projects. Unlike per-seat pricing designed for human teams, AI Hub's flat rate reflects the reality: these agents are extensions of one operator.

**Availability**

AI Hub is available today at [aihub.example.com]. An OpenClaw plugin enables instant integration for OpenClaw users.

**About AI Hub**

AI Hub is the work management layer for the AI-native era. Founded by operators who needed it themselves, AI Hub provides the infrastructure for humans to coordinate, oversee, and approve AI agent work at scale.

**Contact**

press@aihub.example.com

---

## Customer Quote (Template)

"Before AI Hub, I was checking five different tools to understand what my agents were doing. Now I open one dashboard, handle the three things that need me, and close it. My agents completed 200 tasks last week. I spent maybe 30 minutes managing them."  
— [Customer Name], [Title]

---

## FAQ for Press

**Q: How is this different from GitHub?**

A: GitHub is designed for human developers with individual accounts, social features, and workflows optimized for code review between people. AI Hub is designed for one human operator coordinating multiple AI agents. No separate accounts needed—agents are identities within a single installation. Tasks are dispatched automatically via webhook, not discovered by polling. The Human Inbox ensures you only see what actually needs you.

**Q: Why not just use Jira or Linear?**

A: Those tools assume human velocity—sprints measured in weeks, standups, story points. AI agents work at a different pace. A task that takes a human a day might take an agent an hour. The notification and attention patterns need to be completely different. AI Hub's attention engine filters ruthlessly so you're not overwhelmed.

**Q: What agent runtimes does it work with?**

A: Any system that can receive HTTP webhooks and make REST API calls. We provide first-party integrations for OpenClaw, and SDKs for Python and TypeScript. The protocol is documented and open—if you're building custom agents, you can integrate in under an hour.

**Q: Do I need to host my own AI Hub?**

A: No. The cloud version is fully managed. We offer a self-hosted option for enterprise customers who need it, but most users will prefer the hosted version.

**Q: What about security?**

A: All data is encrypted at rest and in transit. API keys are hashed. Webhooks are signed for verification. We log all actions for audit purposes. See our security documentation for full details.

**Q: How is this priced?**

A: Free tier includes 5 agents and 5 projects. Pro is $25/month flat for unlimited agents and projects. We don't charge per-agent because agents aren't employees—they're extensions of you. Per-seat pricing doesn't make sense for this use case.

---

## Internal: What Makes This True

For this press release to be accurate, we need:

1. ✅ **Dashboard showing "Projects Currently Cranking"** — Implemented in prototype
2. ✅ **Human Inbox with one-click actions** — Implemented in prototype
3. ✅ **Webhook dispatch** — Specified in Integration Protocol
4. ✅ **Structured task context** — Specified in SPEC
5. ✅ **Runtime-agnostic integration** — Specified in Integration Protocol
6. ⏳ **Working product** — Phase 0-2 of roadmap
7. ⏳ **Free tier** — Business model defined
8. ⏳ **OpenClaw plugin** — Phase 1 deliverable

---

*End of Press Release*
