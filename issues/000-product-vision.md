# Issue #0: Product Vision & Deployment Tiers

## Product

Otter Camp is a work management platform for AI agent teams. It provides project tracking, content collaboration, issue management, file versioning, and agent coordination — built around a git-native, commit-everything model.

## Three Deployment Tiers

### Tier 1: Open Source / Self-Hosted
- User runs Otter Camp + OpenClaw on their own infrastructure
- Full source code available
- User manages everything: database, hosting, OpenClaw, agents
- Otter Camp connects to their local OpenClaw instance
- **Target audience:** Developers, tinkerers, privacy-conscious users

### Tier 2: Hosted + Bring Your Own OpenClaw
- Otter Camp hosted by us (otter.camp)
- User runs OpenClaw on their own machine (Mac, Linux, VPS)
- Bridge connects their OpenClaw to our hosted Otter Camp
- User manages their own agents and hardware
- We manage the web app, database, hosting
- **Target audience:** Power users who want the web UI without hosting it, but want control over their agent runtime

### Tier 3: Fully Managed
- We spin up a VPS and install OpenClaw for the customer
- Otter Camp is the **only interface they ever touch**
- We manage: VPS provisioning, OpenClaw installation, updates, monitoring
- Customer manages: agents (through Otter Camp UI), projects, content
- Otter Camp becomes the complete control plane
- **Target audience:** Non-technical users, teams, businesses

---

## Architecture Implications

### What Tier 3 Requires

Tier 3 is the most demanding — if we build for Tier 3, Tiers 1 and 2 are covered. Key requirements:

| Capability | Needed For | Related Issue |
|-----------|------------|---------------|
| **Full agent lifecycle management** — add, configure, retire agents entirely from web UI | Tier 3 (user never touches CLI) | #103 |
| **OpenClaw config management** — read, edit, apply config without SSH | Tier 3 | #103 |
| **Remote diagnostics & remediation** — restart gateway, port bump, view logs from browser | Tier 3 (no SSH access) | #102 |
| **VPS provisioning** — spin up new instances, install OpenClaw | Tier 3 | Future issue |
| **OpenClaw updates** — apply updates remotely through Otter Camp | Tier 3 | #102 |
| **Monitoring & alerting** — know when something breaks without user reporting | Tier 3 | #102 |
| **Issue/project management** — all work tracking in the web UI | All tiers | #100 |
| **File browsing & editing** — view and edit repo contents in browser | All tiers | #101 |
| **Multi-tenant isolation** — multiple customers on shared infrastructure | Tier 2 + 3 | Existing (org model) |
| **Bridge auto-connect** — seamless connection between OpenClaw and Otter Camp | Tier 2 + 3 | #102 |

### Bridge as the Critical Layer

The bridge (`bridge/openclaw-bridge.ts`) is the most important piece for Tiers 2 and 3. It's the connection between the OpenClaw runtime (wherever it runs) and the Otter Camp web app. Everything remote flows through it:

```
[Otter Camp Web UI] ←→ [api.otter.camp] ←→ [WebSocket Bridge] ←→ [OpenClaw on host]
```

For Tier 3, the bridge becomes the remote management agent. It must:
- Push host diagnostics continuously
- Execute commands on behalf of Otter Camp (restart, config changes, updates)
- Sync agent files bidirectionally
- Handle reconnection gracefully
- Report its own health
- Auto-update itself

### Database / Hosting

- **Tier 1:** User provides their own Postgres
- **Tier 2:** We host Postgres (Railway or equivalent)
- **Tier 3:** We host everything — Postgres, Otter Camp web, and manage the customer's VPS

### What We Don't Need Yet

- Billing / subscription management
- Customer onboarding wizard
- VPS provisioning automation
- Multi-region deployment

These are future work. The current issues (#100-#103) build the foundation that all three tiers need.

---

## Current Issue Map

| Issue | Description | Tier Coverage |
|-------|-------------|---------------|
| #100 | Issues as single work tracking primitive | All tiers |
| #101 | Files tab + review flow | All tiers |
| #102 | Connections & diagnostics page | Tier 2 + 3 (critical for 3) |
| #103 | Agent management interface | All tiers (critical for 3) |

---

## Key Design Principle

**Build for Tier 3, ship for all.** If the web UI can fully manage an OpenClaw instance without SSH, it works for everyone. Self-hosted users get the same UI but can also drop to CLI if they want. The UI should never be a lesser experience than the command line.
