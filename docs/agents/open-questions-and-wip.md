# Agents: Open Questions and WIP

> Summary: Agent lifecycle and runtime control areas that still need product/engineering decisions.
> Last updated: 2026-02-16
> Audience: Agents planning lifecycle/ops improvements.

## WIP

- Better surfaced lifecycle audit trail for retire/reactivate operations.
- Stronger separation between persisted agent metadata and transient sync state.
- Cleanup of stale test/duplicate directories impacting agent-facing dev workflows.

## Decisions Needed

- Should `retired` be terminal or allow richer suspended/quarantined states?
- Should protected-agent policy be configurable per org?
- Should hosted bridge-reconnect alerts escalate differently by role/capability?

## Change Log

- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
