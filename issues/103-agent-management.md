# Issue #103: Agent Management Interface

> ⚠️ **NOT READY FOR WORK** — This issue is still being specced. Do not begin implementation until this banner is removed.


## Problem

Managing agents currently requires:
- Editing `openclaw.json` on the Mac Studio to add/remove agents or change config
- Editing files in `~/Documents/SamsBrain/Agents/<Name>/` for identity/personality
- Running `openclaw gateway restart` to apply config changes
- No way to do any of this remotely

Otter Camp should provide a full agent management interface — adding, retiring, configuring, and editing identity files from the browser.

---

## Part 1: Agent Identity Files in Otter Camp

### Proposal

Store agent identity files in an Otter Camp git repo. Each agent gets a folder:

```
agents/                          # Otter Camp project: "Agent Files"
├── Frank/
│   ├── SOUL.md                  # Personality, voice, values
│   ├── IDENTITY.md              # Name, creature, vibe, emoji, avatar
│   ├── MEMORY.md                # Long-term memory (main session only)
│   ├── TOOLS.md                 # Local tool notes, credentials
│   └── memory/
│       ├── 2026-02-07.md        # Daily memory files
│       └── 2026-02-06.md
├── Derek/
│   ├── SOUL.md
│   ├── IDENTITY.md
│   └── ...
├── Stone/
│   └── ...
└── AGENTS.md                    # Shared global instructions (loaded by all)
```

### How It Connects to OpenClaw

OpenClaw workspaces currently use symlinks:
```
~/.openclaw/workspace-main/ → ~/Documents/SamsBrain/Agents/Frank/
~/.openclaw/workspace-2b/   → ~/Documents/SamsBrain/Agents/Derek/
```

The migration path:
1. Create "Agent Files" project in Otter Camp with the folder structure above
2. Clone it locally to `~/Documents/OtterCamp/agent-files/`
3. Update OpenClaw workspace symlinks to point to the clone:
   ```
   ~/.openclaw/workspace-main/ → ~/Documents/OtterCamp/agent-files/Frank/
   ```
4. The bridge periodically pulls from Otter Camp to pick up remote edits
5. Agents commit locally → push to Otter Camp → visible in web UI
6. Sam edits in browser → commit → bridge pulls → OpenClaw picks up changes

### Benefits

- **Edit SOUL.md from the browser** — Document Workspace already supports markdown editing + commit
- **Version history** — see how identity evolved over time (git log)
- **Review flow** — could use issue-as-PR to review identity changes before they go live
- **Agents can self-edit** — they commit, push, it shows up in Otter Camp
- **Remote access** — manage agent personalities from anywhere
- **Backup** — Otter Camp repo is the canonical store, not a single laptop

### What About MEMORY.md and Daily Files?

Memory files are high-frequency (agents write to daily files constantly). Options:

**Option A: All files in Otter Camp (recommended)**
- Agent commits daily memory files to the repo like any other file
- Higher commit volume but that's fine — Otter Camp is built for this
- Sam can browse an agent's memories from the web UI
- Full history preserved

**Option B: Memory files stay local, identity files in Otter Camp**
- Only SOUL.md, IDENTITY.md, TOOLS.md in the repo
- Daily memory files stay in SamsBrain (local only)
- Simpler but loses remote visibility into agent memory

---

## Part 2: Agents Page Enhancements

### Current State

The Agents page (`web/src/pages/AgentsPage.tsx`) shows agent cards with:
- Name, status (online/busy/offline)
- Current model
- Session info

### New Features

#### 2a. Agent Detail View

Clicking an agent card opens a detail page with:

**Overview Tab:**
- Name, slot, status, model, avatar
- Current session info (tokens, channel, last active)
- Heartbeat configuration
- Slack channels (primary + listening)

**Identity Tab:**
- Renders the agent's identity files from the Otter Camp repo
- **SOUL.md** — editable in Document Workspace (rendered markdown + source toggle)
- **IDENTITY.md** — editable
- **TOOLS.md** — editable
- Edit → commit → push, just like the content review flow
- View history of changes to each file

**Memory Tab:**
- **MEMORY.md** — the agent's long-term memory (viewable, editable)
- **Daily files** — browse `memory/YYYY-MM-DD.md` files
- Calendar view showing which days have memory entries
- Search across all memory files

**Activity Tab:**
- Recent commits by this agent (across all projects)
- Recent issues assigned to this agent
- Cron jobs associated with this agent
- Session history / transcript excerpts

**Settings Tab:**
- Model configuration (primary + fallbacks)
- Heartbeat interval
- Channel bindings (which Slack channels, with requireMention setting)
- Enabled/disabled toggle

#### 2b. Add Agent

**UI:** "Add Agent" button on the Agents page.

**Flow:**
1. Enter agent slot name (e.g., `research`)
2. Enter display name (e.g., "Riley")
3. Select model (dropdown of available models)
4. Configure heartbeat (optional)
5. Configure channel bindings (optional)
6. System creates:
   - Agent folder in the Otter Camp repo with template SOUL.md, IDENTITY.md
   - Agent entry in `openclaw.json` config
   - Triggers gateway restart to pick up new agent

**Template IDENTITY.md:**
```markdown
# IDENTITY.md - Who Am I?

- **Name:** [entered name]
- **Creature:** *(to be determined)*
- **Vibe:** *(to be determined)*
- **Emoji:** *(pick one)*
- **Avatar:** *(workspace-relative path or URL)*
```

**Template SOUL.md:**
```markdown
# SOUL.md - Who You Are

*You're not a chatbot. You're becoming someone.*

[Default soul content from the shared template]

---

_This file is yours to evolve. As you learn who you are, update it._
```

#### 2c. Retire Agent

**UI:** "Retire" button on agent detail page (Settings tab).

**Flow:**
1. Confirmation dialog: "Retiring [Name] will disable their sessions and archive their files. This can be undone."
2. System:
   - Moves agent folder to `agents/_retired/[Name]/` in the repo
   - Disables agent in `openclaw.json` (remove from config or add `enabled: false`)
   - Triggers gateway restart
   - Agent's historical data (commits, issues, activity) remains visible but marked as retired

**Undo:** "Reactivate" button on retired agents restores them.

#### 2d. Agent Roster View

Enhanced agents page showing all agents in a table/grid:

| Name | Slot | Status | Model | Tokens | Last Active | Heartbeat | Channels |
|------|------|--------|-------|--------|-------------|-----------|----------|
| Frank | main | 🟢 Online | opus-4-6 | 45k | 2m ago | 15m | all |
| Derek | 2b | 🟡 Busy | gpt-5.2-codex | 120k | now | — | #engineering |
| Stone | three-stones | 🔴 Offline | opus-4-6 | 0 | 3h ago | — | #content |
| ... | | | | | | | |

With:
- Sort by any column
- Filter by status, channel
- Bulk actions (restart all, ping all)
- Quick-edit model inline

---

## Part 3: OpenClaw Config Management

### Current State

Agent configuration lives in `openclaw.json` on the Mac Studio. Changes require editing the file and restarting the gateway.

### Proposal

Otter Camp should be able to read and write the OpenClaw config:

#### Config Sync

1. Bridge reads `openclaw.json` and includes it in sync payloads (or a separate endpoint)
2. Otter Camp stores the current config
3. When Sam makes changes in the UI (add agent, change model, update channels), Otter Camp:
   - Sends the config patch through the bridge
   - Bridge applies it to `openclaw.json`
   - Bridge restarts the gateway
   - Reports success/failure back

#### What's Configurable

| Setting | Scope | Description |
|---------|-------|-------------|
| Agent list | Global | Which agents exist |
| Model | Per-agent | Primary model + fallbacks |
| Heartbeat | Per-agent | Interval and enabled/disabled |
| Channel bindings | Per-agent | Which channels, requireMention |
| Gateway port | Global | Port number |
| Proxy URL | Global | Pearl proxy endpoint |
| Cron jobs | Global | Scheduled tasks |

#### Safety

- Config changes require confirmation
- Show a diff before applying ("These changes will be made:")
- Keep config history (store previous versions)
- Rollback button if something breaks

---

## Files to Create/Modify

### Backend

- **New: `internal/api/admin_agents.go`** — Handlers for:
  - `GET /api/admin/agents` — Full agent roster with config details
  - `GET /api/admin/agents/{id}` — Agent detail (identity files, config, sessions)
  - `POST /api/admin/agents` — Create new agent
  - `PATCH /api/admin/agents/{id}` — Update agent config
  - `POST /api/admin/agents/{id}/retire` — Retire agent
  - `POST /api/admin/agents/{id}/reactivate` — Reactivate retired agent
  - `GET /api/admin/agents/{id}/files` — List agent identity files
  - `GET /api/admin/agents/{id}/files/{path}` — Get file content
  - `PUT /api/admin/agents/{id}/files/{path}` — Update file + commit
  - `GET /api/admin/agents/{id}/memory` — List memory files
  - `GET /api/admin/agents/{id}/memory/{date}` — Get daily memory file
- **New: `internal/api/admin_config.go`** — Handlers for:
  - `GET /api/admin/config` — Current OpenClaw config
  - `PATCH /api/admin/config` — Apply config patch (via bridge)
  - `GET /api/admin/config/history` — Config change history
  - `POST /api/admin/config/rollback/{version}` — Rollback to previous config
- **Modify: `internal/api/router.go`** — Register admin routes
- **Modify: `internal/api/openclaw_sync.go`** — Include config in sync payload

### Bridge

- **Modify: `bridge/openclaw-bridge.ts`** —
  - Read `openclaw.json` and include in sync (or on-demand endpoint)
  - Handle config patch commands from Otter Camp
  - Handle agent create/retire commands
  - Execute gateway restart after config changes
  - Manage agent file repo (pull/push for remote edits)

### Frontend

- **New: `web/src/pages/AgentDetailPage.tsx`** — Agent detail with tabs (Overview, Identity, Memory, Activity, Settings)
- **New: `web/src/components/agents/AgentIdentityEditor.tsx`** — Edit SOUL.md, IDENTITY.md etc. using Document Workspace
- **New: `web/src/components/agents/AgentMemoryBrowser.tsx`** — Browse daily memory files with calendar view
- **New: `web/src/components/agents/AddAgentModal.tsx`** — New agent creation form
- **New: `web/src/components/admin/ConfigEditor.tsx`** — OpenClaw config viewer/editor with diff preview
- **Modify: `web/src/pages/AgentsPage.tsx`** — Enhanced roster view
- **Modify: `web/src/router.tsx`** — Add `/agents/{id}` route

---

## Migration Plan

### Phase 1: Agent Files Repo
1. Create "Agent Files" project in Otter Camp
2. Copy current SamsBrain/Agents/ content into the repo
3. Update workspace symlinks to point to the clone
4. Verify agents can still read/write their files

### Phase 2: Read-Only UI
1. Build agent detail page with identity/memory tabs
2. Fetch files from the Otter Camp repo via git API (tree/blob from Issue #2)
3. Display rendered markdown

### Phase 3: Edit + Config
1. Enable editing identity files from the browser
2. Build config management (read/patch/restart via bridge)
3. Add agent creation and retirement flows

---

## Relationship to Other Issues

- **Depends on Issue #2** (Files tab) — The file tree/blob API is needed to browse agent files
- **Depends on Issue #3** (Connections) — Gateway restart is needed for config changes
- **Extends Issue #1** (Issues) — Agent assignment on issues requires knowing the agent roster
