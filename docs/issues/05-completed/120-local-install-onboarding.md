# Issue #120 — Local Install & Onboarding

## Summary

Complete local install experience: from `git clone` to running Otter Camp with agents imported, in one smooth flow. The install should be EASY — a friendly terminal wizard, not a developer README.

## The Flow

```
git clone https://github.com/samhotchkiss/otter-camp && cd otter-camp
make setup        # or ./scripts/setup.sh
otter init        # interactive wizard
open localhost:4200
```

## 1. Dependency Installation (`scripts/setup.sh`)

The setup script must detect missing dependencies and **offer to install them** — not just error out.

### Required Dependencies
- **Homebrew** (macOS only) — if missing, offer to install
- **Go** — `brew install go` (macOS) / distro package manager (Linux)
- **Node + npm** — `brew install node` (macOS) / distro package manager (Linux)  
- **Docker** (with compose) — or detect existing local Postgres
- **Git** — `brew install git` / distro package manager
- **Ollama** — `brew install ollama && ollama pull nomic-embed-text` (for memory system embeddings)

### Behavior
- Detect OS (macOS / Linux / WSL)
- For each missing dep: explain what it is in plain language, ask "Install? (Y/n)", run the install
- Green ✅ checkmark on success, clear error message on failure
- No jargon — "Setting up the database..." not "Running Postgres migrations..."
- If ALL deps present, skip straight through with checkmarks

### After Dependencies
- Start Postgres (Docker or detect local)
- Run migrations
- Install frontend dependencies (`cd web && npm ci`)
- Run seed
- Build frontend + server (`make prod-local` style)
- Write `.env` with port `4200`
- Write bridge `.env`

## 2. Interactive Init (`otter init`)

### Step 1: Local or Hosted?
```
Welcome to Otter Camp! 🦦

How are you setting up?
  [1] Local install (run everything on this machine)
  [2] Hosted (connect to otter.camp)
> 1
```

If hosted: print "Visit otter.camp/setup to get started" and exit. (Future — #119)

### Step 2: Create Account
```
Let's set up your account.

Your name: Sam
Email: s@swh.me  
Organization name: My Team
```

### Step 3: Create Org + Admin User + Auth Token
- Create org with display name + slug (auto-generated from name)
- Create admin user
- Auto-generate git token
- Save token to CLI config (`~/Library/Application Support/otter/config.json` on macOS, `~/.config/otter/config.json` on Linux)
- Print token once: "Your auth token (saved to CLI config): oc_local_..."

### Step 4: Default Project
- Auto-create a "Getting Started" project with a welcome issue explaining how to use Otter Camp

### Step 5: OpenClaw Detection + Agent Import
```
🔍 Detecting OpenClaw installation...
✅ Found OpenClaw at ~/.openclaw with 13 agents

Import agents into Otter Camp? (Y/n)
```

If yes:
- Read each agent's workspace directory
- Import identity files: SOUL.md, IDENTITY.md, MEMORY.md, TOOLS.md
- Create agent records in Otter Camp with their identity data
- **Do NOT modify openclaw.json or any OpenClaw config**
- **Do NOT restart OpenClaw or change agent slots**
- Otter Camp becomes system of record for identities; OpenClaw keeps running as-is

```
✅ Imported 13 agents:
   Frank (main) — Chief of Staff
   Derek (2b) — Engineering Lead
   ...
```

### Step 6: OpenClaw Project Import
```
📂 Scanning agent workspaces for active projects...
Found 6 potential projects. Import? (Y/n)
```

- Scan agent workspaces, session logs, memory files for project references
- Infer projects from repos, topic clusters, active work
- Auto-create projects and seed issues in Otter Camp
- "Looks like you've been working on 6 projects — I've set them up for you"

### Step 7: Bridge Setup
```
🌉 Setting up the bridge (connects OpenClaw ↔ Otter Camp)...
```

- Detect OpenClaw gateway port and token from `~/.openclaw/openclaw.json`
- Write bridge config automatically
- Offer to start the bridge

### Done
```
🦦 Otter Camp is ready!

Dashboard: http://localhost:4200
CLI:       otter whoami

Your agents are imported and the bridge is connecting.
Happy building!
```

## 3. Server Configuration

- **Default port: 4200** (not 8080 — avoids common conflicts)
- **Bind `0.0.0.0`** — allows access from other machines (e.g., via Tailscale)
- Update `cmd/server/main.go`, `.env` template, `docker-compose.yml`, all references to port 8080
- `VITE_API_URL` empty for prod-local (relative URLs) — works across Tailscale without config

## 4. Docker Compose Updates

- Change `postgres` image from `postgres:16-alpine` to `pgvector/pgvector:pg16` (needed for #111 memory system)
- Update exposed port references from 8080 → 4200
- Ensure API service binds `0.0.0.0:4200`

## Files to Create/Modify

### Modified
- `scripts/setup.sh` — full rewrite with dependency detection + auto-install
- `cmd/otter/main.go` — add `otter init` command with interactive wizard
- `cmd/server/main.go` — default port 4200, bind 0.0.0.0
- `docker-compose.yml` — pgvector image, port 4200
- `Makefile` — update port references
- `.env` template — port 4200

### New
- `cmd/otter/init.go` — init wizard logic (account creation, agent import, project import, bridge setup)
- `cmd/otter/init_test.go`
- `internal/api/onboarding.go` — API endpoints for account bootstrapping
- `internal/api/onboarding_test.go`
- `internal/import/openclaw.go` — OpenClaw detection + agent/project import logic
- `internal/import/openclaw_test.go`

## Dependencies

- None — this is foundational and should be built on current main

## Acceptance Criteria

- [ ] Fresh machine with only macOS/Linux: `git clone` + `make setup` + `otter init` gets to working dashboard
- [ ] Missing dependencies are detected and installed with user permission
- [ ] Agent import reads identity files without modifying OpenClaw
- [ ] Project import creates reasonable projects from existing OpenClaw state  
- [ ] Dashboard accessible at `localhost:4200` after setup
- [ ] Dashboard accessible via Tailscale from another machine
- [ ] Port 4200 used everywhere (no 8080 references remain)
- [ ] Docker compose uses pgvector/pgvector:pg16

## Execution Log
- [2026-02-10 09:42 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Moved spec 120 from 01-ready to 02-in-progress and created branch codex/spec-120-local-install-onboarding from origin/main | Tests: n/a
- [2026-02-10 09:44 MST] Issue #623 | Commit n/a | created | Port 4200 + pgvector compose foundation issue opened with explicit runtime/config grep checks | Tests: go test ./internal/config -count=1; go test ./cmd/server -count=1; rg "8080" .env.example docker-compose.yml Makefile scripts/setup.sh cmd/server
- [2026-02-10 09:44 MST] Issue #624 | Commit n/a | created | Setup dependency detection/install prompt issue opened with shell syntax and setup script tests | Tests: bash -n scripts/setup.sh; bash scripts/setup_test.sh
- [2026-02-10 09:44 MST] Issue #625 | Commit n/a | created | Setup bootstrap flow/env/build/migrations issue opened with setup script test commands | Tests: bash -n scripts/setup.sh; bash scripts/setup_test.sh
- [2026-02-10 09:44 MST] Issue #626 | Commit n/a | created | Onboarding bootstrap API endpoint issue opened with onboarding handler test command | Tests: go test ./internal/api -run TestOnboarding -count=1
- [2026-02-10 09:44 MST] Issue #627 | Commit n/a | created | OpenClaw detection + agent identity import library issue opened with import package tests | Tests: go test ./internal/import -run TestOpenClawAgentImport -count=1
- [2026-02-10 09:44 MST] Issue #628 | Commit n/a | created | OpenClaw project inference/import heuristics issue opened with focused import tests | Tests: go test ./internal/import -run TestOpenClawProjectImport -count=1
- [2026-02-10 09:44 MST] Issue #629 | Commit n/a | created | otter init scaffold + local/hosted selection issue opened with CLI handler test command | Tests: go test ./cmd/otter -run TestHandleInit -count=1
- [2026-02-10 09:44 MST] Issue #630 | Commit n/a | created | otter init account bootstrap + config persistence issue opened with bootstrap test command | Tests: go test ./cmd/otter -run TestInitBootstrap -count=1
- [2026-02-10 09:44 MST] Issue #631 | Commit n/a | created | otter init OpenClaw import + bridge setup issue opened with integration-branch CLI tests | Tests: go test ./cmd/otter -run TestInitImportAndBridge -count=1
- [2026-02-10 09:44 MST] Issue #632 | Commit n/a | created | Docs + smoke coverage issue opened to lock clone→setup→init path validation | Tests: bash scripts/setup_test.sh; go test ./cmd/otter -run TestInit -count=1
- [2026-02-10 09:47 MST] Issue #623 | Commit 635930d | committed | Standardized local runtime defaults to port 4200, switched compose Postgres image to pgvector, and added runtime default contract tests | Tests: go test ./internal/config -count=1; go test ./cmd/server -count=1; ! rg -n "8080" .env.example docker-compose.yml Makefile scripts/setup.sh cmd/server internal/config
- [2026-02-10 09:47 MST] Issue #623 | Commit 635930d | pushed | Pushed local runtime default foundation slice to origin branch codex/spec-120-local-install-onboarding | Tests: n/a
- [2026-02-10 09:47 MST] Issue #623 | Commit 635930d | closed | Closed GitHub issue with commit hash and passing config/runtime checks | Tests: go test ./internal/config -count=1; go test ./cmd/server -count=1; ! rg -n "8080" .env.example docker-compose.yml Makefile scripts/setup.sh cmd/server internal/config
- [2026-02-10 09:50 MST] Issue #624 | Commit 4a87a51 | committed | Refactored setup script with OS-aware dependency detection, install prompts, and new setup dependency test harness | Tests: bash -n scripts/setup.sh; bash scripts/setup_test.sh
- [2026-02-10 09:50 MST] Issue #624 | Commit 4a87a51 | pushed | Pushed setup dependency-detection slice to origin branch | Tests: n/a
- [2026-02-10 09:50 MST] Issue #624 | Commit 4a87a51 | closed | Closed GitHub issue with commit hash and setup dependency test evidence | Tests: bash -n scripts/setup.sh; bash scripts/setup_test.sh
- [2026-02-10 09:52 MST] Issue #625 | Commit 642e630 | committed | Added setup bootstrap-flow test coverage for env/bridge defaults and dry-run migrate/npm/seed/build command path assertions | Tests: bash -n scripts/setup.sh; bash scripts/setup_test.sh
- [2026-02-10 09:52 MST] Issue #625 | Commit 642e630 | pushed | Pushed setup bootstrap-flow validation slice to origin branch | Tests: n/a
- [2026-02-10 09:52 MST] Issue #625 | Commit 642e630 | closed | Closed GitHub issue with commit hash and setup bootstrap test evidence (bootstrap logic shipped in 4a87a51 + 642e630) | Tests: bash -n scripts/setup.sh; bash scripts/setup_test.sh
- [2026-02-10 09:57 MST] Issue #626 | Commit 2d3606e | committed | Added POST /api/onboarding/bootstrap to create/reuse org+owner user, mint session token, and ensure default Getting Started project + welcome issue with onboarding API tests | Tests: go test ./internal/api -run TestOnboarding -count=1; go test ./internal/api -count=1
- [2026-02-10 09:57 MST] Issue #626 | Commit 2d3606e | pushed | Pushed onboarding bootstrap API slice to origin branch codex/spec-120-local-install-onboarding | Tests: n/a
- [2026-02-10 09:57 MST] Issue #626 | Commit 2d3606e | closed | Closed GitHub issue with commit hash and passing onboarding API test evidence | Tests: go test ./internal/api -run TestOnboarding -count=1; go test ./internal/api -count=1
- [2026-02-10 10:01 MST] Issue #627 | Commit 282b5f5 | committed | Added internal/import OpenClaw detection + read-only identity import library for SOUL/IDENTITY/MEMORY/TOOLS with fallback workspace discovery and safe non-regular file skipping | Tests: go test ./internal/import -run TestOpenClawAgentImport -count=1; go test ./internal/import -count=1
- [2026-02-10 10:01 MST] Issue #627 | Commit 282b5f5 | pushed | Pushed OpenClaw identity import library slice to origin branch codex/spec-120-local-install-onboarding | Tests: n/a
- [2026-02-10 10:01 MST] Issue #627 | Commit 282b5f5 | closed | Closed GitHub issue with commit hash and passing import package test evidence | Tests: go test ./internal/import -run TestOpenClawAgentImport -count=1; go test ./internal/import -count=1
- [2026-02-10 10:03 MST] Issue #628 | Commit 11d693e | committed | Added OpenClaw project signal extraction + deterministic candidate inference with conservative confidence filtering and deduped issue hints | Tests: go test ./internal/import -run TestOpenClawProjectImport -count=1; go test ./internal/import -count=1
- [2026-02-10 10:03 MST] Issue #628 | Commit 11d693e | pushed | Pushed OpenClaw project inference heuristics slice to origin branch codex/spec-120-local-install-onboarding | Tests: n/a
- [2026-02-10 10:03 MST] Issue #628 | Commit 11d693e | closed | Closed GitHub issue with commit hash and passing project-import heuristic test evidence | Tests: go test ./internal/import -run TestOpenClawProjectImport -count=1; go test ./internal/import -count=1
- [2026-02-10 10:05 MST] Issue #629 | Commit acdbcec | committed | Added `otter init` command scaffold with local/hosted mode selection, interactive prompt flow, and hosted handoff path plus CLI parse/branch tests | Tests: go test ./cmd/otter -run TestHandleInit -count=1; go test ./cmd/otter -count=1
- [2026-02-10 10:05 MST] Issue #629 | Commit acdbcec | pushed | Pushed CLI init scaffold slice to origin branch codex/spec-120-local-install-onboarding | Tests: n/a
- [2026-02-10 10:05 MST] Issue #629 | Commit acdbcec | closed | Closed GitHub issue with commit hash and passing init command test evidence | Tests: go test ./cmd/otter -run TestHandleInit -count=1; go test ./cmd/otter -count=1
- [2026-02-10 10:09 MST] Issue #630 | Commit f7a2370 | committed | Extended `otter init` local mode to collect account prompts, call onboarding bootstrap API, persist CLI config (api/token/org), and print one-time token summary with follow-up guidance; added onboarding client API support/tests | Tests: go test ./cmd/otter -run TestInitBootstrap -count=1; go test ./cmd/otter -run TestHandleInit -count=1; go test ./internal/ottercli -run TestClientOnboardingBootstrapUsesExpectedPathAndPayload -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 10:09 MST] Issue #630 | Commit f7a2370 | pushed | Pushed init bootstrap + config persistence slice to origin branch codex/spec-120-local-install-onboarding | Tests: n/a
- [2026-02-10 10:09 MST] Issue #630 | Commit f7a2370 | closed | Closed GitHub issue with commit hash and passing init bootstrap/client test evidence | Tests: go test ./cmd/otter -run TestInitBootstrap -count=1; go test ./cmd/otter -run TestHandleInit -count=1; go test ./internal/ottercli -run TestClientOnboardingBootstrapUsesExpectedPathAndPayload -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 10:15 MST] Issue #631 | Commit d8ceb91 | committed | Extended local `otter init` to detect OpenClaw, optionally import agents/projects via API calls, generate bridge/.env from detected gateway settings, and optionally start bridge process; added import/bridge branch tests | Tests: go test ./cmd/otter -run TestInitImportAndBridge -count=1; go test ./cmd/otter -run TestInitBootstrap -count=1; go test ./cmd/otter -run TestHandleInit -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 10:15 MST] Issue #631 | Commit d8ceb91 | pushed | Pushed init OpenClaw import + bridge setup slice to origin branch codex/spec-120-local-install-onboarding | Tests: n/a
- [2026-02-10 10:15 MST] Issue #631 | Commit d8ceb91 | closed | Closed GitHub issue with commit hash and passing init import/bridge test evidence | Tests: go test ./cmd/otter -run TestInitImportAndBridge -count=1; go test ./cmd/otter -run TestInitBootstrap -count=1; go test ./cmd/otter -run TestHandleInit -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 10:19 MST] Issue #632 | Commit f66a7ac | committed | Updated README/CLI docs for clone→setup→init local flow on port 4200 and added setup completion smoke assertion for `otter init` + dashboard URL | Tests: bash scripts/setup_test.sh; go test ./cmd/otter -run TestInit -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 10:19 MST] Issue #632 | Commit f66a7ac | pushed | Pushed docs + smoke coverage slice to origin branch codex/spec-120-local-install-onboarding | Tests: n/a
- [2026-02-10 10:19 MST] Issue #632 | Commit f66a7ac | closed | Closed GitHub issue with commit hash and passing setup/init smoke test evidence | Tests: bash scripts/setup_test.sh; go test ./cmd/otter -run TestInit -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 10:19 MST] Issue #n/a | Commit n/a | moved-to-needs-review | All planned spec-120 micro-issues (#623-#632) implemented, pushed, and closed; moved spec from 02-in-progress to 03-needs-review for external validation | Tests: n/a
- [2026-02-10 10:52 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Prioritized reviewer-required follow-up: moved spec 120 from 01-ready to 02-in-progress | Tests: n/a
- [2026-02-10 10:52 MST] Issue #n/a | Commit n/a | branch-created | Created branch codex/spec-120-local-install-onboarding-r2 from codex/spec-120-local-install-onboarding for reviewer fixes | Tests: n/a
- [2026-02-10 10:53 MST] Issue #645 | Commit n/a | created | Planned onboarding bootstrap security hardening fixes (setup lock, role-preserving upsert, body limit, generic DB errors) with explicit API tests | Tests: go test ./internal/api -run TestOnboarding -count=1; go test ./internal/api -run TestOnboardingBootstrapRejectsSecondSetup -count=1; go test ./internal/api -run TestOnboardingBootstrapExistingUserRoleUnchanged -count=1; go test ./internal/api -run TestOnboardingBootstrapOversizedPayload -count=1
- [2026-02-10 10:53 MST] Issue #646 | Commit n/a | created | Planned CI/Dockerfile port-4200 consistency fixes and runtime-default contract assertions | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1
- [2026-02-10 10:53 MST] Issue #647 | Commit n/a | created | Planned local-dev port-reference cleanup (vite proxy, e2e health, bridge env example, docs, assertion mismatch) | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1; cd web && npm run test -- --run web/e2e/app.spec.ts
- [2026-02-10 10:53 MST] Issue #648 | Commit n/a | created | Planned docker-compose frontend build arg relative API base fix | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1
- [2026-02-10 10:53 MST] Issue #649 | Commit n/a | created | Planned setup.sh security/reliability hardening (secret fallback, safe env parsing, dry-run no network) with shell tests | Tests: bash scripts/setup_test.sh; bash -n scripts/setup.sh
- [2026-02-10 10:53 MST] Issue #645,#646,#647,#648,#649 | Commit n/a | planned-set-verified | Verified reviewer-required change set for spec-120 is fully represented by open micro-issues before coding | Tests: n/a
- [2026-02-10 11:08 MST] Issue #659 | Commit n/a | created | Planned canonical onboarding org field follow-up to remove alias keys and enforce organization_name-only payloads with API tests | Tests: go test ./internal/api -run TestOnboardingBootstrapCanonicalOrganizationFieldOnly -count=1; go test ./internal/api -run TestOnboardingBootstrapValidationFailures -count=1
- [2026-02-10 11:08 MST] Issue #660 | Commit n/a | created | Planned onboarding client expires_at typing alignment to time.Time with parse regression coverage | Tests: go test ./internal/ottercli -run TestClientOnboardingBootstrapParsesExpiresAtTime -count=1; go test ./internal/ottercli -run TestClientOnboardingBootstrapUsesExpectedPathAndPayload -count=1
- [2026-02-10 11:08 MST] Issue #661 | Commit n/a | created | Planned cleanup of remaining cosmetic 8080 fixture references in internal test files | Tests: go test ./internal/ottercli -run TestSaveAndLoadConfig -count=1; go test ./internal/ws -run TestWebSocketOriginValidation -count=1
- [2026-02-10 11:08 MST] Issue #662 | Commit n/a | created | Planned importer guard test coverage for path traversal and symlinked identity files | Tests: go test ./internal/import -run TestIsWithinDir -count=1; go test ./internal/import -run TestReadIdentityFileRejectsSymlinks -count=1
- [2026-02-10 11:08 MST] Issue #659,#660,#661,#662 | Commit n/a | planned-set-verified | Verified remaining reviewer-required P3 items are fully represented by open micro-issues before continuing implementation | Tests: n/a
- [2026-02-10 11:12 MST] Issue #645 | Commit 42f087b | committed | Hardened onboarding bootstrap with setup lock, body cap, role-preserving upsert, and generic DB-unavailable response plus security regression tests | Tests: go test ./internal/api -run TestOnboarding -count=1; go test ./internal/api -run TestOnboardingBootstrapRejectsSecondSetup -count=1; go test ./internal/api -run TestOnboardingBootstrapExistingUserRoleUnchanged -count=1; go test ./internal/api -run TestOnboardingBootstrapOversizedPayload -count=1; go test ./internal/api -run TestOnboardingBootstrapDatabaseUnavailableDoesNotLeakDetails -count=1; go test ./internal/api -count=1
- [2026-02-10 11:12 MST] Issue #645 | Commit 42f087b | pushed | Pushed onboarding hardening slice to origin branch codex/spec-120-local-install-onboarding-r2 | Tests: n/a
- [2026-02-10 11:12 MST] Issue #645 | Commit 42f087b | closed | Closed GitHub issue with commit hash and onboarding security test evidence | Tests: go test ./internal/api -run TestOnboarding -count=1; go test ./internal/api -run TestOnboardingBootstrapRejectsSecondSetup -count=1; go test ./internal/api -run TestOnboardingBootstrapExistingUserRoleUnchanged -count=1; go test ./internal/api -run TestOnboardingBootstrapOversizedPayload -count=1; go test ./internal/api -run TestOnboardingBootstrapDatabaseUnavailableDoesNotLeakDetails -count=1; go test ./internal/api -count=1
- [2026-02-10 11:12 MST] Issue #646 | Commit 9840236 | committed | Set docker-compose frontend API build arg to relative empty value and expanded runtime-default checks to lock that contract | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1
- [2026-02-10 11:12 MST] Issue #646 | Commit 9840236 | pushed | Pushed docker-compose relative API-base follow-up slice | Tests: n/a
- [2026-02-10 11:12 MST] Issue #646 | Commit 9840236 | closed | Closed GitHub issue with commit hash and runtime-default contract evidence | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1
- [2026-02-10 11:12 MST] Issue #647 | Commit c232298 | committed | Removed local-dev 8080 references in Vite proxy, e2e health check, bridge env example, and docs; expanded runtime-default coverage for these files | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1; cd web && npm run test:e2e -- --list e2e/app.spec.ts
- [2026-02-10 11:12 MST] Issue #647 | Commit c232298 | pushed | Pushed local-dev port-consistency follow-up slice | Tests: n/a
- [2026-02-10 11:12 MST] Issue #647 | Commit c232298 | closed | Closed GitHub issue with commit hash and targeted runtime/e2e listing checks | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1; cd web && npm run test:e2e -- --list e2e/app.spec.ts
- [2026-02-10 11:12 MST] Issue #648 | Commit ca30513 | committed | Enforced port 4200 in CI backend env/frontend API URL/health wait and Dockerfile run hint/EXPOSE/HEALTHCHECK with contract assertions | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1
- [2026-02-10 11:12 MST] Issue #648 | Commit ca30513 | pushed | Pushed CI and Dockerfile runtime-port follow-up slice | Tests: n/a
- [2026-02-10 11:12 MST] Issue #648 | Commit ca30513 | closed | Closed GitHub issue with commit hash and runtime-default verification evidence | Tests: go test ./cmd/server -run TestLocalRuntimeDefaults -count=1
- [2026-02-10 11:12 MST] Issue #649 | Commit 61f29eb | committed | Hardened setup script env/secret/dry-run behavior and added regression tests (dry-run network avoidance, literal env parsing, fallback secrets, malformed env rejection, temp cleanup) | Tests: bash -n scripts/setup.sh; bash scripts/setup_test.sh
- [2026-02-10 11:12 MST] Issue #649 | Commit 61f29eb | pushed | Pushed setup hardening follow-up slice | Tests: n/a
- [2026-02-10 11:12 MST] Issue #649 | Commit 61f29eb | closed | Closed GitHub issue with commit hash and setup script test evidence | Tests: bash -n scripts/setup.sh; bash scripts/setup_test.sh
- [2026-02-10 11:12 MST] Issue #659 | Commit 1059c9a | committed | Removed onboarding alias request fields and enforced canonical organization_name payload with regression coverage | Tests: go test ./internal/api -run TestOnboardingBootstrapCanonicalOrganizationFieldOnly -count=1; go test ./internal/api -run TestOnboardingBootstrapValidationFailures -count=1
- [2026-02-10 11:12 MST] Issue #659 | Commit 1059c9a | pushed | Pushed canonical onboarding org-field follow-up slice | Tests: n/a
- [2026-02-10 11:12 MST] Issue #659 | Commit 1059c9a | closed | Closed GitHub issue with commit hash and API validation tests | Tests: go test ./internal/api -run TestOnboardingBootstrapCanonicalOrganizationFieldOnly -count=1; go test ./internal/api -run TestOnboardingBootstrapValidationFailures -count=1
- [2026-02-10 11:12 MST] Issue #660 | Commit 8b615e6 | committed | Aligned onboarding client expires_at type to time.Time and added parse regression test | Tests: go test ./internal/ottercli -run TestClientOnboardingBootstrapParsesExpiresAtTime -count=1; go test ./internal/ottercli -run TestClientOnboardingBootstrapUsesExpectedPathAndPayload -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 11:12 MST] Issue #660 | Commit 8b615e6 | pushed | Pushed onboarding expires_at type-alignment follow-up slice | Tests: n/a
- [2026-02-10 11:12 MST] Issue #660 | Commit 8b615e6 | closed | Closed GitHub issue with commit hash and client parse/path test evidence | Tests: go test ./internal/ottercli -run TestClientOnboardingBootstrapParsesExpiresAtTime -count=1; go test ./internal/ottercli -run TestClientOnboardingBootstrapUsesExpectedPathAndPayload -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 11:12 MST] Issue #661 | Commit 1991aa6 | committed | Updated remaining internal test fixture URLs from 8080 to 4200 for local-default consistency | Tests: go test ./internal/ottercli -run TestSaveLoadConfig -count=1; go test ./internal/ws -run TestIsWebSocketOriginAllowed_LoopbackAliasAllowed -count=1; rg -n "8080" internal/ottercli/config_test.go internal/ws/handler_test.go
- [2026-02-10 11:12 MST] Issue #661 | Commit 1991aa6 | pushed | Pushed cosmetic fixture consistency follow-up slice | Tests: n/a
- [2026-02-10 11:12 MST] Issue #661 | Commit 1991aa6 | closed | Closed GitHub issue with commit hash and focused package checks | Tests: go test ./internal/ottercli -run TestSaveLoadConfig -count=1; go test ./internal/ws -run TestIsWebSocketOriginAllowed_LoopbackAliasAllowed -count=1; rg -n "8080" internal/ottercli/config_test.go internal/ws/handler_test.go
- [2026-02-10 11:12 MST] Issue #662 | Commit 8341c9c | committed | Added importer guard tests for within-dir traversal handling and symlinked identity-file rejection | Tests: go test ./internal/import -run TestIsWithinDir -count=1; go test ./internal/import -run TestReadIdentityFileRejectsSymlinks -count=1; go test ./internal/import -count=1
- [2026-02-10 11:12 MST] Issue #662 | Commit 8341c9c | pushed | Pushed importer safety-test follow-up slice | Tests: n/a
- [2026-02-10 11:12 MST] Issue #662 | Commit 8341c9c | closed | Closed GitHub issue with commit hash and importer safety-test evidence | Tests: go test ./internal/import -run TestIsWithinDir -count=1; go test ./internal/import -run TestReadIdentityFileRejectsSymlinks -count=1; go test ./internal/import -count=1
- [2026-02-10 11:12 MST] Issue #640,#641,#642 | Commit n/a | closed | Closed superseded umbrella reviewer issues after shipping/closing replacement micro-issues #645-#649 and #659-#662 | Tests: n/a
- [2026-02-10 11:12 MST] Issue #n/a | Commit n/a | reviewer-block-removed | Removed fully resolved top-level Reviewer Required Changes block; detailed closure evidence preserved in execution log entries above | Tests: n/a
