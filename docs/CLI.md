# Otter CLI

The `otter` CLI is the canonical way for agents to create projects and set Git remotes for OtterCamp work.

## Install (from repo)
```bash
# from repo root
make otter
# or

go build -o ./bin/otter ./cmd/otter
```

## Config
Config is stored at:
`~/.config/otter/config.json`

```json
{
  "apiBaseUrl": "https://api.otter.camp",
  "token": "oc_sess_...",
  "defaultOrg": "<org-uuid>"
}
```

## Auth
```bash
otter auth login --token <token> --org <org-uuid>
# optional override
otter auth login --token <token> --org <org-uuid> --api http://localhost:8080
```

## Create a project
```bash
otter project create "My Project" --description "Short description" --repo-url https://github.com/owner/repo
```

## Clone a project
```bash
otter clone my-project
# or
# otter clone "My Project" --path /custom/path
```

## Add a remote to an existing repo
```bash
cd /path/to/repo

otter remote add "My Project"
# if origin already exists:
otter remote add "My Project" --force
```

## Repo info
```bash
otter repo info "My Project"
```

## JSON output
Most commands support `--json` for scripting.

## Troubleshooting
- Missing org id? Pass `--org` or set `defaultOrg` in config.
- Missing token? Re-run `otter auth login`.
