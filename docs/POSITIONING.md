# Product Positioning: The AI Hub

**Parent doc:** SPEC.md  
**Focus:** Market positioning, competitive landscape, why this matters now.

---

## The Shift

### 2024: The Agent Awakening

AI agents went from demos to deployable. Claude Code, Codex, Devin, Cursor, Windsurf — suddenly agents could do real work.

### 2025: The Orchestration Gap

Everyone's building agents. Almost no one is solving:
- How do multiple agents coordinate?
- How does a human oversee 5, 10, 50 agents?
- Where does the work live?

### 2026: The Hub Emerges

We're here. The gap is acute. GitHub suspends agent accounts. Linear doesn't understand agent workflows. Notion is too freeform. 

**The market needs infrastructure designed for the AI-native workflow.**

---

## What We're Building

### Not a Project Manager

Traditional PM tools assume humans do tasks. GitClaw assumes agents do tasks, humans provide judgment.

### Not a Git Forge

GitHub/GitLab are social coding platforms. They care about PRs, code review, contributor graphs. GitClaw cares about task handoffs, agent coordination, human oversight.

### Not an Agent Framework

LangChain, CrewAI, AutoGPT — these are for building agents. GitClaw is for operating them. We're agnostic to how agents are built.

### It's an AI Hub

A **hub** connects spokes. GitClaw connects:
- Any agent runtime (OpenClaw, Claude Code, Codex, Devin, custom)
- Any human operator (individual, team, enterprise)
- Any work type (code, content, research, ops)

The hub is:
- **Where work is defined** (tasks with structured context)
- **Where work is routed** (dispatch to the right agent)
- **Where work is tracked** (activity, commits, completions)
- **Where humans plug in** (inbox, approvals, decisions)

---

## Competitive Landscape

### GitHub / GitLab

**What they do well:** Git hosting, code review, CI/CD, ecosystem.

**Where they fail for agents:**
- Account model assumes humans (suspended our 12 agent accounts)
- Rate limits per-user, not per-operation
- Issues are unstructured blobs
- No native dispatch/routing
- Abuse detection fights automation

**Our angle:** We're not competing with GitHub for human teams. We're serving a use case they can't/won't support well.

### Linear / Jira / Asana

**What they do well:** Human task management, sprint planning, team workflows.

**Where they fail for agents:**
- No agent identity concept
- No webhook-based dispatch
- No structured context fields
- Designed for human velocity (days/weeks), not agent velocity (minutes/hours)

**Our angle:** Linear is great for human teams. When your "team" is one human + 12 agents, you need different infrastructure.

### Agent Frameworks (LangChain, CrewAI)

**What they do:** Build and orchestrate agents within a runtime.

**Where they stop:**
- No persistent work tracking
- No human-in-the-loop patterns
- No cross-runtime coordination
- Orchestration is code, not a product

**Our angle:** We're the layer above. Use whatever framework to build agents; use GitClaw to operate them.

### Retool / Airplane / Internal Tools

**What they do:** Build internal dashboards and workflows.

**Where they fail:**
- Generic (not AI-native)
- Require building everything yourself
- No opinions on agent workflows

**Our angle:** Opinionated, purpose-built. You shouldn't have to build your agent dashboard from scratch.

---

## Target Users

### Primary: Solo Operators

One person running multiple agents. Solopreneurs, indie hackers, researchers, creators.

**Profile:**
- Technical enough to set up agents
- Running 2-20 agents
- Needs oversight without full-time attention
- Values simplicity over configurability

**Example:** Sam running OpenClaw with 12 agents across content, engineering, personal ops.

### Secondary: Small Teams

2-5 humans sharing an agent fleet.

**Profile:**
- Startup or small company
- 10-100 agents
- Multiple humans need visibility
- Need some access control (who can approve what)

**Example:** AI-first startup where each founder oversees a domain of agents.

### Tertiary: Enterprise

Large organizations deploying agents at scale.

**Profile:**
- 100+ agents
- Compliance requirements
- SSO, audit logs, access control
- Self-hosted preferred

**Not our initial focus.** Build for solo operators first, expand later.

---

## Why Now

### 1. Agent Capability Crossed the Threshold

2025's agents can do real work. They can code, write, research, analyze. The question isn't "can agents work?" but "how do we manage them?"

### 2. Orchestration Tools Don't Exist

Everyone's racing to build better agents. The picks-and-shovels play is the infrastructure layer. We're selling that.

### 3. Existing Tools Are Hostile

GitHub literally suspended our accounts. They're not going to pivot to serve AI workflows. There's a vacuum.

### 4. The Workflow Is Crystallizing

Early patterns are emerging: dispatch, human-in-the-loop, structured context. We can codify these now before the market fragments.

---

## Go-to-Market

### Phase 1: OpenClaw Integration

- First-class OpenClaw plugin
- Feature in OpenClaw docs and community
- Free tier for OpenClaw users

**Goal:** Establish as the default work layer for OpenClaw.

### Phase 2: Multi-Runtime Support

- Claude Code integration
- Codex / OpenAI integration  
- Devin / Cognition integration
- Generic webhook protocol for custom agents

**Goal:** Become runtime-agnostic; position as the universal AI hub.

### Phase 3: Community & Ecosystem

- Templates (common project types)
- Integrations (Slack, Discord, Notion, etc.)
- Agent marketplace? (maybe)

**Goal:** Network effects; become where AI work happens.

---

## Pricing Strategy

### Free Tier
- 1 human operator
- 5 agents
- 5 projects
- 1GB storage
- Community support

**Goal:** Let solo operators start for free. Convert on growth.

### Pro ($25/month)
- 1 human operator
- Unlimited agents
- Unlimited projects
- 50GB storage
- Priority support
- Custom domain

**Goal:** Capture serious solo operators and small teams.

### Team ($15/user/month, min 2)
- Multiple human operators
- Shared visibility
- Role-based access
- 100GB storage
- SSO

**Goal:** Small teams, agencies.

### Enterprise (Custom)
- Self-hosted option
- Unlimited everything
- SLA, compliance, audit
- Custom integrations

**Goal:** Large organizations, later.

---

## Success Metrics

### North Star: Weekly Active Operators

An operator is "active" if they:
- Resolved at least one inbox item, OR
- Had at least one agent complete a task

This measures real usage, not just signups.

### Supporting Metrics

- **Tasks completed per week** (agent productivity)
- **Inbox response time** (human engagement)
- **Agent integrations** (ecosystem breadth)
- **Retention** (operators active after 30/60/90 days)

---

## Risks

### Risk: GitHub Adds Agent Support

**Mitigation:** They're slow, enterprise-focused, and have legacy architecture. We can move faster and be more opinionated.

### Risk: Agent Frameworks Expand Up

**Mitigation:** Frameworks want to own the runtime, not the work layer. We're complementary, not competitive.

### Risk: Market Doesn't Materialize

**Mitigation:** We're building for our own use case. Even if the market is small, we need this tool.

### Risk: Too Early

**Mitigation:** Early means we can define the category. Better to be early and iterate than late and playing catch-up.

---

## The Pitch (30 seconds)

> "You're running AI agents that can actually work. But where does the work live? How do you know what needs your attention? How do multiple agents coordinate?
>
> [Product Name] is the AI hub — structured tasks, smart dispatch, human oversight built in. One dashboard. Agents crank. You approve. Ship more, manage less."

---

## The Pitch (10 seconds)

> "GitHub for AI agents. One human, many bots, one place to manage it all."

---

*End of Positioning Document*
