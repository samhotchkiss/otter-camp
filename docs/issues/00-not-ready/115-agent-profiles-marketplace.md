# Issue #115 — Agent Profiles & Marketplace

> STATUS: NOT READY

## Problem

Setting up a new agent is manual and intimidating. You write a SOUL.md from scratch, figure out what system prompt patterns work, configure channels, tune personality — it's 30+ minutes of setup before the agent does anything useful. Most people don't know what a good agent profile even looks like.

## Concept

Pre-built agent profiles that you can install in seconds and customize to your needs. Think WordPress themes but for agent identities.

### What's an Agent Profile?

A complete, portable agent configuration:

```
profile/
├── SOUL.md          # Personality, voice, behavior rules
├── IDENTITY.md      # Name, role, emoji, avatar
├── AGENTS.md        # Working instructions, tool usage patterns
├── TOOLS.md         # Default tool configs
├── HEARTBEAT.md     # What to check on periodic polls
├── config.json      # Model preferences, channel suggestions, thinking level
└── README.md        # What this agent does, how to use it
```

### Profile Categories

- **Roles** — Content Strategist, Code Reviewer, Research Analyst, Executive Assistant, etc.
- **Teams** — pre-configured groups that work together (Content Team, Dev Team, Ops Team)
- **Personas** — personality templates (direct & concise, warm & supportive, technical & precise)
- **Industry** — marketing agency, SaaS startup, solo dev, etc.

### User Flow

1. Browse profiles in OtterCamp UI or CLI
2. Pick one (or a team pack)
3. Customize name, personality tweaks, channel assignment
4. Deploy — agent is live with a working identity immediately

### OtterCamp Integration

With Chameleon architecture (#110):
- Profiles stored in OtterCamp as templates
- `otter agent create --from-profile "code-reviewer"` → instant agent
- Profiles are versioned — updates available, user chooses to apply
- Custom profiles saveable from existing agents ("export as profile")

### CLI

```bash
# Browse available profiles
otter profiles list
otter profiles list --category dev-team

# Preview a profile
otter profiles show code-reviewer

# Create agent from profile
otter agent create --from-profile code-reviewer --name "Elena"

# Export current agent as profile
otter agent export derek --as-profile "senior-engineer"

# Import a community profile
otter profiles import ./my-custom-profile/
```

## Reference: AgentPacks.ai

https://www.agentpacks.ai/ — existing product doing pre-built agent teams for OpenClaw.

Key observations:
- **7 team packs** (Content Creator, Dev Team, Solopreneur, and 4 more)
- **3 agents per pack** with defined roles (Content Strategist + Script Writer + Social Manager, etc.)
- **Channel config templates** included (Discord, WhatsApp, Telegram)
- **Install flow**: download ZIP → send to main agent → it configures everything
- **Self-hosted**: everything runs locally, no data leaves your machine
- **Subscription model** for access to all packs

What we can learn:
- The pack/team concept is smart — people want a working team, not individual agents
- Role definitions are the hard part — good SOUL.md templates have real value
- The "send ZIP to your agent" install flow is clever but fragile. OtterCamp can do this better with a proper profile system.
- Channel config templates reduce setup friction significantly

What we'd do differently:
- Profiles live in OtterCamp, not as ZIP files
- Install via CLI or UI, not by messaging an agent
- Community marketplace for sharing profiles (like VS Code extensions)
- Profiles are composable — mix a persona with a role with a team structure
- Version-tracked in git (naturally, since everything in OtterCamp is)

## Future: Community Marketplace

- Users create and share agent profiles
- Rating/review system
- "Fork" a profile and customize
- Featured profiles curated by us
- Revenue share for profile creators? (way down the road)

## Dependencies

- [ ] #110 — Chameleon Architecture (agent identities in OtterCamp)
- [ ] #107 — Agent Role Cards (structured role definitions)

## Open Questions

1. Should profiles include memory seeds? (e.g., "this agent knows about React best practices" → pre-loaded shared knowledge)
2. How do team packs handle inter-agent communication patterns? (e.g., "Code Reviewer watches for PRs from Tech Lead")
3. Free tier vs paid profiles? Or everything open source?
4. How to handle profile updates — auto-update agent identity, or user reviews diff first?
