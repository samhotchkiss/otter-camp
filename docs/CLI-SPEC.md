# Otter CLI v1 — Specification

> **Status:** Draft · **Issue:** #219 · **Version:** 1.0.0

## Overview

The Otter CLI (`otter`) is the command-line interface for [Otter Camp](https://otter.camp). It manages authentication, projects, remotes, and repository metadata from the terminal. Designed for both human users and AI agents.

---

## Installation

```bash
npm install -g @otter-camp/cli
# or
brew install otter-camp/tap/otter
```

---

## Global Flags

| Flag | Short | Description |
|------|-------|-------------|
| `--json` | `-j` | Output structured JSON (see [JSON Output](#json-output)) |
| `--quiet` | `-q` | Suppress non-essential output |
| `--verbose` | `-v` | Enable debug logging |
| `--config <path>` | `-c` | Override config file path |
| `--no-color` | | Disable ANSI colors |
| `--version` | `-V` | Print version and exit |
| `--help` | `-h` | Show help for any command |

---

## Commands

### `otter auth login`

Authenticate with Otter Camp.

```bash
# Interactive (opens browser)
otter auth login

# Token-based (for CI/agents)
otter auth login --token <TOKEN>
echo "$OTTER_TOKEN" | otter auth login --token -
```

**Flags:**
| Flag | Description |
|------|-------------|
| `--token <value>` | API token; use `-` to read from stdin |
| `--host <url>` | Custom Otter Camp instance (default: `https://otter.camp`) |

**Exit codes:** `0` success, `1` invalid/expired token, `2` network error.

Stores credentials in `~/.config/otter/config.json` (see [Configuration](#configuration)).

---

### `otter auth whoami`

Print the authenticated user.

```bash
$ otter auth whoami
sam (sam@swh.me) on https://otter.camp

$ otter auth whoami --json
{"username":"sam","email":"sam@swh.me","host":"https://otter.camp"}
```

**Exit codes:** `0` authenticated, `1` not logged in.

---

### `otter project create`

Create a new Otter Camp project.

```bash
otter project create "My Project"
otter project create "My Project" --desc "A cool project" --private
```

**Flags:**
| Flag | Description |
|------|-------------|
| `--desc <text>` | Project description |
| `--private` | Make project private (default: public within org) |
| `--template <name>` | Initialize from a project template |

**Exit codes:** `0` created, `1` name conflict, `2` auth error.

---

### `otter clone`

Clone an Otter Camp project to a local directory.

```bash
otter clone my-project
otter clone my-project ./local-dir
otter clone sam/my-project          # explicit owner
```

**Behavior:**
1. Creates `<project-name>/` (or specified dir)
2. Initializes git repo with `otter.camp` as the `origin` remote
3. Pulls all project files

**Flags:**
| Flag | Description |
|------|-------------|
| `--branch <name>` | Check out a specific branch after clone |
| `--depth <n>` | Shallow clone with history depth `n` |

---

### `otter remote add`

Add an Otter Camp remote to an existing git repository.

```bash
otter remote add my-project
otter remote add my-project --name upstream
```

**Flags:**
| Flag | Description |
|------|-------------|
| `--name <remote>` | Git remote name (default: `otter`) |

This writes the remote URL as `https://otter.camp/git/<owner>/<project>.git` (or SSH variant based on config).

---

### `otter repo info`

Display metadata for the current project/repository.

```bash
$ otter repo info
Project:  my-project
Owner:    sam
Remote:   https://otter.camp/git/sam/my-project.git
Branch:   main
Status:   active
Created:  2026-01-15T08:30:00Z

$ otter repo info --json
{
  "project": "my-project",
  "owner": "sam",
  "remote": "https://otter.camp/git/sam/my-project.git",
  "branch": "main",
  "status": "active",
  "created": "2026-01-15T08:30:00Z"
}
```

**Flags:**
| Flag | Description |
|------|-------------|
| `--project <name>` | Query a specific project (default: detect from cwd) |

---

## Configuration

Config lives at `~/.config/otter/config.json`. Respects `XDG_CONFIG_HOME`.

```jsonc
{
  "version": 1,
  "hosts": {
    "https://otter.camp": {
      "username": "sam",
      "token": "otr_abc123...",
      "protocol": "ssh"        // "ssh" | "https"
    }
  },
  "defaults": {
    "owner": "sam",            // default owner for new projects
    "remote_name": "otter",    // default git remote name
    "json": false              // always output JSON
  }
}
```

### Environment Variables

| Variable | Overrides |
|----------|-----------|
| `OTTER_TOKEN` | Auth token (highest priority) |
| `OTTER_HOST` | Target host |
| `OTTER_CONFIG` | Config file path |
| `NO_COLOR` | Disables color output |

**Precedence:** env vars → CLI flags → config file → defaults.

---

## JSON Output

All commands support `--json` for structured, machine-readable output. The envelope:

```json
{
  "ok": true,
  "data": { ... },
  "errors": []
}
```

On failure:

```json
{
  "ok": false,
  "data": null,
  "errors": [{"code": "AUTH_EXPIRED", "message": "Token has expired"}]
}
```

Error codes are stable strings suitable for programmatic matching. Human-readable messages may change between versions.

---

## Agent Examples

AI agents (e.g., OpenClaw agents) can drive the Otter CLI non-interactively using token auth and `--json`.

### Authenticate

```bash
echo "$OTTER_TOKEN" | otter auth login --token -
```

### Create a project and clone it

```bash
otter project create "sprint-42-docs" --desc "Sprint 42 documentation" --json
otter clone sprint-42-docs
cd sprint-42-docs
```

### Agent workflow: edit, commit, push

```bash
cd sprint-42-docs
echo "# Status Report" > status.md
git add -A
git commit -m "Add status report"
git push origin main
```

### Check identity in a script

```bash
WHOAMI=$(otter auth whoami --json)
USER=$(echo "$WHOAMI" | jq -r '.data.username')
echo "Running as $USER"
```

### Add Otter remote to existing repo

```bash
cd ~/existing-repo
otter remote add my-project --name otter
git push otter main
```

---

## Exit Codes (Summary)

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Command/validation error |
| `2` | Authentication error |
| `3` | Network/server error |
| `127` | Command not found |

---

## Future Commands (Out of Scope for v1)

- `otter task create / list / update` — task management
- `otter deploy` — deploy project artifacts
- `otter config set/get` — CLI config management
- `otter search` — full-text search across projects
