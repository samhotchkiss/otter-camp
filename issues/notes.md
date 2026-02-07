# Notes

## [2026-02-07 15:32:25 MST] CLI `--mine` identity model
- `otter issue list --mine` currently requires `OTTER_AGENT_ID` to be set in the environment.
- Reason: auth token identity (`/api/auth/validate`) does not currently expose a deterministic mapping to an agent row.
- Recommendation:
  1. Add server-side endpoint that returns "current agent id" for the authenticated token in workspace context, or
  2. Extend auth/session payload to include agent identity where applicable.
- Once that exists, CLI can resolve `--mine` automatically without env var setup.


## [2026-02-07 16:12:10 MST] Spec 103 implementation status
- `103-agent-management.md` currently contains a blocking banner: **"NOT READY FOR WORK"**.
- I am intentionally not implementing Spec 103 yet to follow the spec guardrail.
- Action needed from Sam: remove the banner (or add an explicit go-ahead note in the spec) when ready for implementation.
