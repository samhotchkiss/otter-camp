# Notes

## [2026-02-07 15:32:25 MST] CLI `--mine` identity model
- `otter issue list --mine` currently requires `OTTER_AGENT_ID` to be set in the environment.
- Reason: auth token identity (`/api/auth/validate`) does not currently expose a deterministic mapping to an agent row.
- Recommendation:
  1. Add server-side endpoint that returns "current agent id" for the authenticated token in workspace context, or
  2. Extend auth/session payload to include agent identity where applicable.
- Once that exists, CLI can resolve `--mine` automatically without env var setup.

