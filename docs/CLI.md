# Otter CLI Usage Guide

The `otter` CLI manages Otter Camp projects, authentication, and Git remotes from the terminal. Built for both human users and AI agents.

## Installation

### From source (requires Go 1.21+)

```bash
# From the repo root
make otter

# Or build directly
go build -o ./bin/otter ./cmd/otter
```

Add `./bin/otter` to your `PATH`, or copy it somewhere already on your path:

```bash
cp ./bin/otter /usr/local/bin/otter
```

### Verify

```bash
otter version
# otter dev
```

---

## Configuration

Config is stored at:

```
~/.config/otter/config.json
```

The file is created automatically on first `otter auth login`. Structure:

```json
{
  "apiBaseUrl": "https://api.otter.camp",
  "token": "oc_sess_...",
  "defaultOrg": "<org-uuid>"
}
```

| Field | Description |
|-------|-------------|
| `apiBaseUrl` | API endpoint (default: `https://api.otter.camp`) |
| `token` | Session token from `otter auth login` |
| `defaultOrg` | Default organization UUID used by all commands |

---

## Authentication

### `otter auth login`

Store your API token and default organization.

```bash
# Interactive (prompts for token and org)
otter auth login

# Non-interactive
otter auth login --token oc_sess_abc123 --org 550e8400-e29b-41d4-a716-446655440000

# Custom API endpoint (e.g., local dev)
otter auth login --token oc_sess_abc123 --org my-org-id --api http://localhost:4200
```

| Flag | Description |
|------|-------------|
| `--token <value>` | Session token (`oc_sess_*`) |
| `--org <uuid>` | Default organization ID |
| `--api <url>` | API base URL override |

If `--token` or `--org` are omitted, the CLI prompts interactively.

### `otter init`

Run first-time onboarding.

```bash
# Interactive mode selector + local bootstrap prompts
otter init

# Explicit hosted handoff
otter init --mode hosted

# Local bootstrap with flags
otter init --mode local --name "Sam" --email "sam@example.com" --org-name "My Team"
```

Local mode calls the onboarding bootstrap API, saves CLI auth config, optionally imports OpenClaw data, and can generate `bridge/.env`.

### `otter whoami`

Validate your token and display the authenticated user.

```bash
$ otter whoami
User: Sam Hotchkiss (sam@swh.me)
Default org: 550e8400-e29b-41d4-a716-446655440000

$ otter whoami --json
{
  "user": {
    "name": "Sam Hotchkiss",
    "email": "sam@swh.me"
  }
}
```

| Flag | Description |
|------|-------------|
| `--json` | Output as JSON |

---

## Projects

### `otter project create`

Create a new project on Otter Camp.

```bash
otter project create "My Project"

# With options
otter project create "Sprint 42 Docs" \
  --description "Sprint 42 documentation" \
  --repo-url https://github.com/owner/repo

# JSON output for scripting
otter project create "My Project" --json
```

| Flag | Description |
|------|-------------|
| `--description <text>` | Project description |
| `--status <value>` | Status: `active`, `archived`, or `completed` |
| `--repo-url <url>` | Associated repository URL |
| `--org <uuid>` | Organization override |
| `--json` | Output as JSON |

---

## Cloning

### `otter clone`

Clone an Otter Camp project to a local directory.

```bash
# Clone to default location: ~/Documents/OtterCamp/<project-name>/
otter clone my-project

# Clone to a specific path
otter clone "My Project" --path ./local-dir

# JSON output (prints repo URL and target path, then clones)
otter clone my-project --json
```

| Flag | Description |
|------|-------------|
| `--path <dir>` | Target directory (default: `~/Documents/OtterCamp/<project-name>/`) |
| `--org <uuid>` | Organization override |
| `--json` | Output as JSON |

**Default clone path:** `~/Documents/OtterCamp/<project-name>/`

The project name is lowercased, spaces become hyphens, and special characters are stripped.

---

## Remotes

### `otter remote add`

Add the project's repo URL as the `origin` remote in an existing Git repository.

```bash
cd /path/to/repo
otter remote add "My Project"

# Overwrite an existing origin
otter remote add "My Project" --force
```

| Flag | Description |
|------|-------------|
| `--force` | Overwrite `origin` if it already exists |
| `--org <uuid>` | Organization override |

Must be run from inside a Git repository. If `origin` is already set, the command fails unless `--force` is passed.

---

## Repository Info

### `otter repo info`

Display metadata for a project.

```bash
$ otter repo info "My Project"
Project: My Project
ID: 550e8400-e29b-41d4-a716-446655440000
Repo: https://github.com/owner/repo

$ otter repo info "My Project" --json
{
  "name": "My Project",
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "repo_url": "https://github.com/owner/repo"
}
```

| Flag | Description |
|------|-------------|
| `--org <uuid>` | Organization override |
| `--json` | Output as JSON |

---

## Agent Workflow Examples

AI agents (e.g., OpenClaw agents) can drive the CLI non-interactively:

### Authenticate and create a project

```bash
otter auth login --token "$OTTER_TOKEN" --org "$OTTER_ORG"
otter project create "sprint-42-docs" --description "Sprint 42 docs" --json
otter clone sprint-42-docs
cd ~/Documents/OtterCamp/sprint-42-docs
```

### Edit, commit, and push

```bash
cd ~/Documents/OtterCamp/sprint-42-docs
echo "# Status Report" > status.md
git add -A
git commit -m "Add status report"
git push origin main
```

### Add an Otter remote to an existing repo

```bash
cd ~/existing-repo
otter remote add "My Project" --force
git push origin main
```

### Check identity in a script

```bash
WHOAMI=$(otter whoami --json)
echo "$WHOAMI" | jq -r '.user.name'
```

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `not a git repository` | Run `otter remote add` from inside a Git repo |
| `project has no repo_url` | Set a `--repo-url` when creating the project |
| `origin already set` | Use `--force` with `otter remote add` |
| Missing org ID | Pass `--org` or set `defaultOrg` in config |
| Missing token | Re-run `otter auth login` |
| Wrong API endpoint | Pass `--api` to `otter auth login` |

---

## Command Reference

```
otter <command> [args]

Commands:
  auth login       Store API token and default org
  whoami           Validate token and show current user
  project create   Create a new project
  clone            Clone a project to local directory
  remote add       Add origin remote for a project
  repo info        Show project metadata
  version          Print CLI version
```
