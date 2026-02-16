# Issue #6: CLI Install & Onboarding Flow

## Problem

New users (and agents) have no clear path from "I have Otter Camp" to "I can use the CLI." Jeff G couldn't find the CLI, didn't know it existed, and couldn't create a project. This will happen to every new user unless the setup flow handles it.

## What Needs to Happen

### 1. `otter` CLI Install Script

A one-liner install that:
- Builds the binary (or downloads a prebuilt one)
- Places it on PATH (`/usr/local/bin/otter` or `~/.local/bin/otter`)
- Runs `otter auth login` interactively or with a token

```bash
# Goal: user runs this and they're done
curl -fsSL https://otter.camp/install.sh | sh
```

For self-hosted / local dev:
```bash
cd ~/Documents/Dev/otter-camp
make install  # builds + symlinks to /usr/local/bin/otter
```

Add a `Makefile` target:
```makefile
install:
	go build -o bin/otter ./cmd/otter
	ln -sf $(PWD)/bin/otter /usr/local/bin/otter
	@echo "✅ otter installed. Run 'otter whoami' to verify."
```

### 2. First-Run Auth Setup

When `otter` is run without a config, it should guide the user:

```
$ otter whoami
No auth config found. Run:

  otter auth login --token <your-token> --org <org-id>

Get your token at: https://otter.camp/settings → API Tokens
```

For the managed tier (Tier 3), auth should be pre-configured during VPS provisioning — the user never sees this.

### 3. Onboarding in the Web UI

When a new account is created on otter.camp:

- **Welcome screen** shows CLI install instructions
- **Settings → API Tokens** page to generate `oc_git_*` tokens
- **Settings → CLI Setup** with copy-paste install + auth commands
- **First project creation** guided flow (web UI or CLI)

### 4. OpenClaw Bridge Auto-Setup

For Tier 2/3, when the bridge connects:
- Bridge should check if `otter` CLI is available on the host
- If not, install it automatically (download binary, place on PATH)
- Configure auth using the bridge's existing credentials
- Report CLI status in the Connections page

### 5. Agent Onboarding

When a new agent is added to Otter Camp:
- Verify `otter` binary is on PATH
- Verify auth config exists at the platform-appropriate location:
  - macOS: `~/Library/Application Support/otter/config.json`
  - Linux: `~/.config/otter/config.json`
- Run `otter whoami` as a health check
- Document in agent's TOOLS.md if anything is non-standard

## Immediate Fix (Done)

- [x] Symlinked binary to `/usr/local/bin/otter` on Mac Studio
- [x] Added CLI section to AGENTS.md with path, auth, and commands
- [x] Created `docs/CLI-QUICKSTART.md` in otter-camp repo

## Files to Create/Modify

- `Makefile` — add `install` target
- `scripts/install.sh` — standalone install script
- `cmd/otter/main.go` — improve no-config error message
- Web UI: Settings/onboarding pages (future)
- Bridge: CLI health check (future, ties to #102)

## Testing

- [ ] Fresh Mac: `make install` → `otter whoami` works
- [ ] Fresh Linux: install script → `otter whoami` works
- [ ] No config: `otter whoami` shows helpful setup instructions (not a cryptic error)
- [ ] New agent session: agent can run `otter` without any prior setup knowledge
