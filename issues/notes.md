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

## [2026-02-07 16:32:20 MST] Bridge command actions validation note
- I added bridge-side handling for `admin.command` actions (`gateway.restart`, `agent.ping`, `agent.reset`).
- Backend tests cover API dispatch + queue fallback, but there is currently no automated TypeScript/unit harness for `bridge/openclaw-bridge.ts` in this repo.
- Recommended manual validation once online:
  1. Start bridge in continuous mode.
  2. Call `POST /api/admin/gateway/restart` with `org_id`.
  3. Confirm bridge logs command receipt/execution and queue acks.

## [2026-02-07 16:59:07 MST] Spec102 Phase3 bridge test harness note
- `bridge/openclaw-bridge.ts` now includes cron/process admin command handling and snapshot collection attempts.
- Repo still has no dedicated TypeScript/unit harness for `bridge/`; bridge behavior is validated via API dispatch tests + manual runtime validation.
- Action commands intentionally use fallback CLI patterns to tolerate OpenClaw CLI variant drift:
  - cron run: `cron run --id` then `cron trigger --id`
  - cron toggle: `cron enable/disable --id` then `cron update --id --enabled ...`
  - process kill: `process kill --id` then `exec kill --id`
