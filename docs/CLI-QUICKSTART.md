# Otter CLI — Quick Start

## Binary Location

```
~/Documents/Dev/otter-camp/bin/otter
```

Set an alias:
```bash
alias otter="~/Documents/Dev/otter-camp/bin/otter"
```

## Auth

Auth config is shared across all agents at:
```
~/Library/Application Support/otter/config.json
```

**Do not overwrite this file** — it's already configured. To verify:
```bash
otter whoami
```

Expected output:
```
User: Sam (s@swh.me)
Default org: a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11
```

If auth is broken, generate a new token:
```bash
curl -s -X POST https://api.otter.camp/api/auth/magic \
  -H "Content-Type: application/json" \
  -d '{"name": "Sam", "email": "s@swh.me"}'
```

Then update the config:
```json
{
  "apiBaseUrl": "https://api.otter.camp",
  "token": "<token from response>",
  "defaultOrg": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
}
```

## Commands

### Projects
```bash
otter project list                    # List all projects
otter project create "Name"           # Create a project
otter clone <project-name>            # Clone a project repo
```

### Issues
```bash
otter issue list --project <name>                              # List issues
otter issue create --project <name> "Title" --body "..." --priority P1  # Create
otter issue view --project <name> <number>                     # View details
otter issue comment --project <name> <number> "text"           # Add comment
otter issue assign --project <name> <number> --agent <name>    # Assign
otter issue close --project <name> <number>                    # Close
otter issue reopen --project <name> <number>                   # Reopen
otter issue list --project <name> --mine                       # Your issues (needs OTTER_AGENT_ID)
```

### Other
```bash
otter whoami          # Check auth
otter version         # Show version
otter repo info       # Repo info (run inside a cloned project)
otter remote add      # Add otter remote to existing repo
```

## Building from Source

If the binary is missing or outdated:
```bash
cd ~/Documents/Dev/otter-camp
go build -o bin/otter ./cmd/otter
```

## API

Base URL: `https://api.otter.camp`

All API calls use Bearer auth:
```bash
curl -H "Authorization: Bearer <token>" \
     -H "X-Org-ID: a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11" \
     https://api.otter.camp/api/projects
```
