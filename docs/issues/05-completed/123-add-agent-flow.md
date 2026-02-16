# Issue #123 — Add Agent Flow ("Hire an Employee")

> STATUS: READY

## Problem

The current "Add Agent" modal is a boring form with raw config fields (slot, display name, model, heartbeat, channel). It feels like filling out a database row. There's no soul to it. Also includes fields that shouldn't be there (Channel has no business in agent creation).

The error "Agent Files project is not configured" blocks creation entirely if the backend project doesn't exist yet — bad UX with no guidance.

## Vision

Adding an agent should feel like **hiring a new team member**. You're browsing candidates, picking someone interesting, then making them yours. It should be fun.

## Design

### Step 1: Browse Profiles

Full-screen takeover (not a modal). Show a grid of **10+ pre-built agent profiles** as cards. Each card has:

- **Avatar** (illustrated, distinct personality)
- **Name** (a real name, not "Content Agent")
- **Tagline** (one-liner: "Reads everything so you don't have to")
- **Role category** badge (Engineering, Content, Operations, Personal, Creative, Research, etc.)
- **Personality preview** — 2-3 sentences of how they talk/think

Cards should feel like browsing a talent agency portfolio. Dark theme, high contrast, personality forward.

**Filter/search bar** at top: filter by role category, or search by keyword.

Include a "Start from Scratch" card at the end for power users.

### Step 2: Customize

After picking a profile, slide into a customization panel:

- **Name** — pre-filled from profile, editable
- **Personality tweaks** — show the SOUL.md preview, let them edit key traits (tone slider? adjective tags?)
- **Avatar** — show the default, option to change
- **Role** — pre-filled, editable description of what they do
- **Model** — dropdown, pre-selected based on role (e.g. Opus for complex reasoning, Sonnet for fast tasks)

**NOT in this form:**
- ~~Channel~~ — agents aren't married to channels. Channel binding is a separate config concern.
- ~~Heartbeat interval~~ — default is fine, advanced users can change in settings later
- ~~Slot name~~ — auto-generate from display name (slugify), show in "advanced" disclosure if anyone cares

### Step 3: Welcome

After creation, show a welcome screen:

- Agent's avatar + name + "Ready to go"
- Quick-start suggestion: "Send them their first message" with a link to chat
- Brief onboarding tip based on role

## Profile Gallery (Starter Set)

Ship with 10-15 profiles spanning different roles. Examples:

| Name | Role | Vibe |
|------|------|------|
| Marcus | Chief of Staff | Calm, organized, sees the big picture |
| Sage | Research Analyst | Curious, thorough, loves going deep |
| Kit | Content Writer | Witty, concise, hates fluff |
| Rory | Code Reviewer | Precise, opinionated, catches everything |
| Jules | Personal Assistant | Warm, proactive, remembers everything |
| Harlow | Creative Director | Bold, visual, pushes boundaries |
| Quinn | Data Analyst | Pattern-obsessed, loves charts |
| Blair | Social Media | Trend-aware, authentic, anti-cringe |
| Avery | DevOps / Infra | Calm under pressure, automates everything |
| Morgan | Project Manager | Ruthlessly organized, ships on time |
| Reese | Customer Support | Patient, empathetic, solution-focused |
| Sloane | Executive Comms | Sharp, polished, ghostwriter energy |
| Finley | Security / Compliance | Paranoid (in a good way), thorough |
| Emery | Product Manager | Opinionated, user-obsessed, cuts scope |
| Rowan | Learning / Tutor | Patient, Socratic, adjusts to your level |

Each profile is a directory in the codebase:

```
web/src/data/agent-profiles/
├── marcus/
│   ├── profile.json    # name, tagline, role, model, avatar path
│   ├── soul.md         # SOUL.md template
│   ├── identity.md     # IDENTITY.md template
│   └── avatar.png      # Default avatar
├── sage/
│   └── ...
└── index.ts            # Exports all profiles
```

## Backend Changes

### Agent Files Project Auto-Creation

When the first agent is created and no "Agent Files" project exists:

1. **Auto-create it** — don't show an error. Create a project named "Agent Files" with an initialized git repo.
2. Scaffold the directory structure for the new agent.
3. Commit the profile files (SOUL.md, IDENTITY.md) to the repo.

### API: `POST /api/admin/agents`

Update to accept:

```json
{
  "displayName": "Marcus",
  "profileId": "marcus",     // optional, references a built-in profile
  "soul": "...",             // SOUL.md content (from profile or custom)
  "identity": "...",         // IDENTITY.md content
  "model": "claude-sonnet-4-20250514",
  "avatar": "..."            // base64 or URL
}
```

Slot auto-generated: slugify displayName → `marcus`. If collision, append number → `marcus-2`.

### Remove from API/form:
- `channel` — not part of agent identity
- `heartbeat` — use system default, configurable later in agent settings

## Frontend

- **New route**: `/agents/new` — full-page profile browser (not a modal)
- **Profile cards**: `AgentProfileCard` component
- **Customization panel**: `AgentCustomizer` component (right panel or slide-in)
- **Welcome screen**: `AgentWelcome` component (post-creation)
- Replace current `AddAgentModal` entirely

## Technical Notes

- Profile data is static/bundled (no API call to fetch profiles)
- Avatar images: ship as static assets, optimize for web (WebP, max 256x256)
- SOUL.md templates can include `{{name}}` placeholders replaced at creation time
- The "Agent Files" project auto-creation should be idempotent

## Out of Scope

- Community/marketplace profiles (see #115)
- Profile import/export
- Agent-to-agent relationships or team composition
- Channel/binding configuration (separate settings page)

## Success Criteria

- [ ] Adding an agent feels delightful, not administrative
- [ ] 10+ profiles available at launch
- [ ] Zero required form fields that feel like config (slot, heartbeat, channel gone)
- [ ] "Agent Files project not configured" error never shown to users
- [ ] New agent is chat-ready immediately after creation

## Implementation Plan

1. #716: Backend create-agent payload contract (`displayName`-centric) with slug auto-generation and collision suffix behavior.
2. #717: Auto-provision Agent Files project/repo binding on first create, idempotently.
3. #718: Backend profile-template scaffolding with `{{name}}` substitution and custom SOUL/IDENTITY overrides.
4. #719: Frontend bundled profile dataset (10+) and reusable `AgentProfileCard`, `AgentCustomizer`, `AgentWelcome`.
5. #720: Full-page `/agents/new` flow (browse -> customize -> create -> welcome) wired to new API payload.
6. #721: Replace `AddAgentModal` entrypoint in `AgentsPage` with navigation to `/agents/new`.
7. #722: Cross-layer regression hardening and final validation gates for first-time workspace create flow.

## Execution Log
- [2026-02-10 19:11 MST] Issue #716 | Commit n/a | in-progress | Moved spec from 01-ready to 02-in-progress and created branch codex/spec-123-add-agent-flow from origin/main | Tests: n/a
- [2026-02-10 19:14 MST] Issue #716 | Commit n/a | created | Planned backend payload + slot slug generation micro-issue with explicit API test commands | Tests: n/a
- [2026-02-10 19:14 MST] Issue #717 | Commit n/a | created | Planned Agent Files auto-provisioning micro-issue with idempotency and first-create test requirements | Tests: n/a
- [2026-02-10 19:14 MST] Issue #718 | Commit n/a | created | Planned backend profile-template scaffolding + config patch cleanup micro-issue with template precedence tests | Tests: n/a
- [2026-02-10 19:14 MST] Issue #719 | Commit n/a | created | Planned frontend profile dataset and reusable card/customizer/welcome components with explicit web tests | Tests: n/a
- [2026-02-10 19:14 MST] Issue #720 | Commit n/a | created | Planned `/agents/new` full-page flow + route integration micro-issue with page/route tests | Tests: n/a
- [2026-02-10 19:14 MST] Issue #721 | Commit n/a | created | Planned AgentsPage modal replacement micro-issue to route add-agent trigger to `/agents/new` | Tests: n/a
- [2026-02-10 19:14 MST] Issue #722 | Commit n/a | created | Planned cross-layer regression hardening micro-issue with explicit API/web regression commands | Tests: n/a
- [2026-02-10 19:15 MST] Issue #716 | Commit n/a | validated-failing | Added new slot helper tests and confirmed expected red state (undefined helper symbols before implementation) | Tests: go test ./internal/api -run 'Test(AgentSlotFromDisplayName|ResolveAvailableAgentSlot)' -count=1
- [2026-02-10 19:19 MST] Issue #716 | Commit 41aef02 | pushed | Landed displayName-first create payload parsing, slot auto-generation with collision suffixing, and helper/unit test coverage | Tests: go test ./internal/api -run 'Test(AgentSlotFromDisplayName|ResolveAvailableAgentSlot)' -count=1 && go test ./internal/api -run TestAdminAgentsCreate -count=1 -v && go test ./...
- [2026-02-10 19:19 MST] Issue #716 | Commit 41aef02 | closed | Closed GitHub issue with implementation summary and test evidence (including DB test skip note for missing OTTER_TEST_DATABASE_URL) | Tests: n/a
- [2026-02-10 19:20 MST] Issue #717 | Commit n/a | validated-failing | Added Agent Files working-repo helper tests and confirmed expected red state (undefined provisioning helpers before implementation) | Tests: go test ./internal/api -run 'Test(AgentFilesWorkingRepoPath|EnsureAgentFilesWorkingRepo)' -count=1
- [2026-02-10 19:22 MST] Issue #717 | Commit f1872c2 | pushed | Added create-path auto-provisioning for missing Agent Files project/binding plus local working-repo bootstrap and idempotency coverage | Tests: go test ./internal/api -run 'Test(AgentFilesWorkingRepoPath|EnsureAgentFilesWorkingRepo|AgentSlotFromDisplayName|ResolveAvailableAgentSlot)' -count=1 && go test ./internal/api -run TestAdminAgentsCreate -count=1 -v && go test ./...
- [2026-02-10 19:22 MST] Issue #717 | Commit f1872c2 | closed | Closed GitHub issue with commit/test evidence and explicit note that DB-backed create tests skip here without OTTER_TEST_DATABASE_URL | Tests: n/a
- [2026-02-10 19:24 MST] Issue #718 | Commit n/a | validated-failing | Added profile-template unit tests and confirmed expected red state (missing buildCreateAgentTemplateInput helper) before implementation | Tests: go test ./internal/api -run 'TestBuildCreateAgentTemplateInput|TestBuildCreateAgentConfigPatchOmitsChannelAndHeartbeat' -count=1
- [2026-02-10 19:26 MST] Issue #718 | Commit 5f92078 | pushed | Added backend profile-aware SOUL/IDENTITY template resolution with custom override precedence and unknown-profile validation | Tests: go test ./internal/api -run 'TestBuildCreateAgentTemplateInput|TestBuildCreateAgentConfigPatchOmitsChannelAndHeartbeat' -count=1 && go test ./internal/api -run 'Test(AgentFilesWorkingRepoPath|EnsureAgentFilesWorkingRepo|AgentSlotFromDisplayName|ResolveAvailableAgentSlot|BuildCreateAgentTemplateInput|BuildCreateAgentConfigPatchOmitsChannelAndHeartbeat)' -count=1 && go test ./internal/api -run TestAdminAgentsCreate -count=1 -v && go test ./...
- [2026-02-10 19:26 MST] Issue #718 | Commit 5f92078 | closed | Closed GitHub issue with commit/test evidence and DB-env skip note for integration-backed create tests | Tests: n/a
- [2026-02-10 19:27 MST] Issue #719 | Commit n/a | validated-failing | Added frontend dataset/component tests and confirmed expected red state from unresolved profile/index and component module imports | Tests: cd web && npm test -- src/data/agent-profiles/index.test.ts src/components/agents/AgentProfileCard.test.tsx src/components/agents/AgentCustomizer.test.tsx src/components/agents/AgentWelcome.test.tsx --run
- [2026-02-10 19:29 MST] Issue #719 | Commit 7631a86 | pushed | Added bundled starter profile data plus AgentProfileCard/AgentCustomizer/AgentWelcome components with focused interaction tests | Tests: cd web && npm test -- src/data/agent-profiles/index.test.ts src/components/agents/AgentProfileCard.test.tsx src/components/agents/AgentCustomizer.test.tsx src/components/agents/AgentWelcome.test.tsx --run
- [2026-02-10 19:29 MST] Issue #719 | Commit 7631a86 | closed | Closed GitHub issue with implementation summary and test evidence | Tests: n/a
- [2026-02-10 19:30 MST] Issue #720 | Commit n/a | validated-failing | Added `/agents/new` route and page tests and confirmed expected red state (missing route and missing AgentsNewPage module) | Tests: cd web && npm test -- src/router.test.tsx src/pages/AgentsNewPage.test.tsx --run
- [2026-02-10 19:31 MST] Issue #720 | Commit d942ed4 | pushed | Implemented full-page `/agents/new` hire flow with browse/customize/create/welcome states and router registration | Tests: cd web && npm test -- src/router.test.tsx src/pages/AgentsNewPage.test.tsx --run && cd web && npm test -- src/data/agent-profiles/index.test.ts src/components/agents/AgentProfileCard.test.tsx src/components/agents/AgentCustomizer.test.tsx src/components/agents/AgentWelcome.test.tsx src/router.test.tsx src/pages/AgentsNewPage.test.tsx --run
- [2026-02-10 19:31 MST] Issue #720 | Commit d942ed4 | closed | Closed GitHub issue with route/page implementation summary and focused web test evidence | Tests: n/a
- [2026-02-10 19:31 MST] Issue #721 | Commit n/a | validated-failing | Updated AgentsPage test to require `/agents/new` link and confirmed expected red state while AddAgentModal button flow was still present | Tests: cd web && npm test -- src/pages/AgentsPage.test.tsx --run
- [2026-02-10 19:33 MST] Issue #721 | Commit 223cfb5 | pushed | Removed AddAgentModal from AgentsPage and switched add-agent CTA to `/agents/new` navigation link with updated tests | Tests: cd web && npm test -- src/pages/AgentsPage.test.tsx --run && cd web && npm test -- src/data/agent-profiles/index.test.ts src/components/agents/AgentProfileCard.test.tsx src/components/agents/AgentCustomizer.test.tsx src/components/agents/AgentWelcome.test.tsx src/router.test.tsx src/pages/AgentsNewPage.test.tsx src/pages/AgentsPage.test.tsx --run
- [2026-02-10 19:33 MST] Issue #721 | Commit 223cfb5 | closed | Closed GitHub issue with navigation-entrypoint implementation summary and focused web test evidence | Tests: n/a
- [2026-02-10 19:34 MST] Issue #722 | Commit n/a | validated-failing | Added AgentsNewPage regressions for raw Agent Files error handling and welcome CTA routing; confirmed red before fix (raw backend error leaked and location.assign spy path failed) | Tests: cd web && npm test -- src/pages/AgentsNewPage.test.tsx --run
- [2026-02-10 19:36 MST] Issue #722 | Commit 5f741f7 | pushed | Mapped Agent Files provisioning errors to friendly copy and added injectable welcome CTA navigation callback for deterministic regression coverage | Tests: cd web && npm test -- src/pages/AgentsNewPage.test.tsx --run && cd web && npm test -- src/data/agent-profiles/index.test.ts src/components/agents/AgentProfileCard.test.tsx src/components/agents/AgentCustomizer.test.tsx src/components/agents/AgentWelcome.test.tsx src/router.test.tsx src/pages/AgentsNewPage.test.tsx src/pages/AgentsPage.test.tsx --run && go test ./internal/api -run 'TestAdminAgentsCreate.*' -count=1 && go test ./... && cd web && npm test -- src/pages/AgentsNewPage.test.tsx src/pages/AgentsPage.test.tsx --run && cd web && npm test -- --run (fails on unrelated pre-existing AuthContext/App tests)
- [2026-02-10 19:37 MST] Issue #722 | Commit 5f741f7 | closed | Closed GitHub issue with commit/test evidence and noted unrelated pre-existing full-web-suite failures outside agent-flow scope | Tests: n/a
- [2026-02-10 19:38 MST] Issue #722 | Commit 5f741f7 | moved | Moved spec from 02-in-progress to 03-needs-review after completing planned micro-issues #716-#722 | Tests: n/a
- [2026-02-10 19:39 MST] Issue #722 | Commit 5f741f7 | PR opened | Opened PR #723 for reviewer visibility on branch codex/spec-123-add-agent-flow and updated PR body with validation details | Tests: n/a
- [2026-02-10 19:46 MST] Issue #724 | Commit n/a | in-progress | Moved spec back from 01-ready to 02-in-progress to address reviewer-required fixes on branch codex/spec-123-add-agent-flow | Tests: n/a
- [2026-02-10 19:47 MST] Issue #727 | Commit n/a | created | Created follow-up micro-issue for missing Sloane/Rowan profile parity with explicit command-level tests | Tests: n/a
- [2026-02-10 19:47 MST] Issue #725 | Commit n/a | closed | Verified flagged script/build files already matched origin/main and closed isolation follow-up as validated no-op | Tests: git diff --name-status origin/main...HEAD -- scripts/uninstall.sh Makefile scripts/setup.sh scripts/setup_test.sh .gitignore && go test ./internal/api -run 'TestAdminAgentsCreate|TestBuildCreateAgentTemplateInput|TestResolveAvailableAgentSlot' -count=1 && cd web && npm test -- src/data/agent-profiles/index.test.ts src/pages/AgentsNewPage.test.tsx src/pages/AgentsPage.test.tsx --run
- [2026-02-10 19:49 MST] Issue #724 | Commit n/a | validated-failing | Added missing-profile backend tests first and confirmed red on unrecognized profileIds (kit/jules/avery) before implementation | Tests: go test ./internal/api -run 'TestBuildCreateAgentTemplateInput|TestAdminAgentsCreateAcceptsPreviouslyMissingBuiltInProfile' -count=1
- [2026-02-10 19:50 MST] Issue #724 | Commit 8b88eba | pushed | Added builtInAgentProfiles templates for kit/jules/harlow/quinn/blair/avery/morgan/reese/emery/finley plus create/template regression coverage | Tests: go test ./internal/api -run TestBuildCreateAgentTemplateInput -count=1 && go test ./internal/api -run TestAdminAgentsCreate -count=1 && go test ./internal/api -run 'TestBuildCreateAgentTemplateInput|TestAdminAgentsCreate' -count=1
- [2026-02-10 19:50 MST] Issue #724 | Commit 8b88eba | closed | Closed P0 follow-up after push with commit and test evidence on GitHub issue | Tests: n/a
- [2026-02-10 19:51 MST] Issue #726 | Commit n/a | validated-failing | Added occupied-through-limit slot test and confirmed prior unbounded resolver behavior (hang) before adding max-attempt guard | Tests: go test ./internal/api -run TestResolveAvailableAgentSlot -count=1
- [2026-02-10 19:51 MST] Issue #726 | Commit ebd7547 | pushed | Added resolveAvailableAgentSlotMaxAttempts guard and explicit exhaustion error with regression coverage | Tests: go test ./internal/api -run TestResolveAvailableAgentSlot -count=1 && go test ./internal/api -run 'TestAgentSlotFromDisplayName|TestResolveAvailableAgentSlot' -count=1
- [2026-02-10 19:51 MST] Issue #726 | Commit ebd7547 | closed | Closed P2 follow-up after push with command-level test evidence | Tests: n/a
- [2026-02-10 19:52 MST] Issue #727 | Commit n/a | validated-failing | Tightened frontend dataset test to 15 profiles and extended backend template/profile create coverage for sloane/rowan, confirming expected red state | Tests: cd web && npm test -- src/data/agent-profiles/index.test.ts --run && go test ./internal/api -run TestBuildCreateAgentTemplateInput -count=1
- [2026-02-10 19:53 MST] Issue #727 | Commit cb9c543 | pushed | Added Sloane/Rowan to frontend profile dataset and backend template map with updated cross-layer tests | Tests: cd web && npm test -- src/data/agent-profiles/index.test.ts --run && go test ./internal/api -run TestBuildCreateAgentTemplateInput -count=1 && go test ./internal/api -run TestAdminAgentsCreate -count=1 && go test ./internal/api -run 'TestBuildCreateAgentTemplateInput|TestAdminAgentsCreate|TestResolveAvailableAgentSlot' -count=1 && cd web && npm test -- src/data/agent-profiles/index.test.ts src/pages/AgentsNewPage.test.tsx src/pages/AgentsPage.test.tsx --run
- [2026-02-10 19:53 MST] Issue #727 | Commit cb9c543 | closed | Closed P3 follow-up with pushed commit and test evidence | Tests: n/a
- [2026-02-10 19:54 MST] Issue #723 | Commit cb9c543 | updated | Resolved reviewer-required changes (P0/P1/P2/P3), removed top-level reviewer block, and refreshed PR branch with final follow-up commits | Tests: go test ./... && cd web && npm test -- --run (known unrelated failures remain in AuthContext/App tests)
- [2026-02-10 19:54 MST] Issue #727 | Commit cb9c543 | moved | Moved spec from 02-in-progress to 03-needs-review after completing reviewer-required follow-up issues #724-#727 | Tests: n/a
