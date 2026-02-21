# Agents: Runtime Modes

> Summary: How agent execution differs between local-only and hosted+bridge environments, and what must stay environment-agnostic.
> Last updated: 2026-02-16
> Audience: Agents writing automation that must run in both modes.

## Local Runtime

- `otter init --mode local` bootstraps org/user/session locally.
- Local auth shortcuts can exist for localhost flows.
- Bridge is optional but common when local OpenClaw is present.

## Hosted + Bridge Runtime

- CLI supports hosted handoff (`otter init --mode hosted`).
- Bridge process handles sync and command delivery to hosted API.
- WS and sync secrets must match between API and bridge runtime.

## Required Cross-Mode Discipline

- Use config/env for API base URLs; do not hardcode hosted endpoints.
- Keep org/workspace scoping explicit in all requests.
- Treat bridge connectivity as a first-class health dependency in hosted mode.

## Change Log

- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
