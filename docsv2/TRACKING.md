# V2 Spec Tracking

## Status Legend

| # | Status | Meaning |
|---|--------|---------|
| 1 | Stub | Placeholder — title and rough outline only |
| 2 | Initial Draft | First pass of real content, not yet reviewed |
| 3 | In Process with Sam | Actively being worked on in conversation |
| 4 | Proposed Final Pending | Draft complete, awaiting Sam's review |
| 5 | First Principles Review | Undergoing systematic review for gaps and contradictions |
| 6 | First Principles Review Completed | Review done, all issues resolved |
| 7 | Finished | Final spec — all sections complete, open questions resolved, schema defined |

## Spec Status

| Doc | Name | Lines | Status | Notes |
|-----|------|-------|--------|-------|
| 01 | Architecture and Domain | ~220 | 7 - Finished | 8 product principles, 8 domain boundaries, ~65 tables, entity map, cross-doc references, first-principles reviewed |
| 02 | Chat | 1107 | 7 - Finished | Full schema, all open questions resolved |
| 03 | Projects and Task Flow | 914 | 7 - Finished | 12-table schema, first-principles reviewed |
| 03a | Shipping and Delivery | 344 | 7 - Finished | 2 new tables, 18 resolved decisions, first-principles reviewed |
| 04 | Auth, Tenancy, and Identity | ~640 | 7 - Finished | DB-per-org isolation, no org_membership, RBAC on human_user, 22 resolved decisions, first-principles reviewed |
| 05 | Agents, Staff, and Temps | 892 | 7 - Finished | Project-scoped temps, 4 scope types, temps get project access, NDI, 230+ profile catalog, 43 resolved decisions, first-principles reviewed |
| 06 | Memory | 1072 | 7 - Finished | 9-table schema, 52 resolved decisions, first-principles reviewed |
| 07 | Models and Inference | 818 | 7 - Finished | Provider abstraction, model profiles, provider connections, cost tracking, 3 system profiles, fallback chains, priority queue, schema, first-principles reviewed |
| 08 | Deployment and Self-Hosting | ~945 | 7 - Finished | Three modes, Docker Compose, binary distribution, db-per-org managed mode, pgvector, catalog DB, per-org migration orchestration, OpenAI-compat bootstrap, provider connection model aligned with doc 07, 8 deferred items, first-principles reviewed |
| 09 | MCP Integration | 659 | 7 - Finished | Connection management, tool discovery, security, schema, first-principles reviewed |
| 10 | Skills Integration | 545 | 7 - Finished | Skill format, storage, activation, catalog, schema, first-principles reviewed |
| 11 | System Integration (CLI and Browser) | 740 | 7 - Finished | CLI sandboxing, browser actions, human handoff, artifacts, first-principles reviewed |
| 12 | API, Events, and Realtime | ~1440 | 7 - Finished | REST + SSE + CLI, event system, ~140 event types, endpoint catalog, 4-table schema, API-first principle, CLI client, first-principles reviewed |
| 13 | Security, Observability, Costs | ~790 | 7 - Finished | 6-layer defense-in-depth, 9 threats, 1-table schema, token-based budgets (fail closed), Usage Explorer with 5 drill-down dimensions, 20 resolved decisions, first-principles reviewed |
| 14 | Open Questions and Phasing | ~850 | 7 - Finished | 4-phase build plan, bootstrap dataset, risk areas, all 48 open questions resolved, agent upgrade mechanism, tiered retention, operator_instructions field, first-principles reviewed |
| 15 | Migration and Backward Compat | ~386 | 7 - Finished | Clean-room rebuild, CLI-only JSONL import (permanent), 10-step bootstrap, validation checklist, 10 resolved decisions, first-principles reviewed |
| 16 | Agent Control Plane | ~1200 | 7 - Finished | Binary policy (allow/deny), 7-table schema, capability templates, 8-step execution lifecycle, human review is a flow node not a policy outcome, first-principles reviewed |
| 17 | TUI | 813 | 2 - Initial Draft | Bubble Tea, layout, keyboard nav, command palette |
| 18 | Web UI | 884 | 2 - Initial Draft | React SPA, three-panel layout, command bar, design system |
| 19 | Mobile UI | 459 | 2 - Initial Draft | React Native, push notifications, quick actions |
| 20 | Tools and Tool Policy | ~636 | 7 - Finished | Four categories (native/system/browser/external), two-tier execution, ~55 native tools, 4-stage resolution pipeline, per-session caching, capability gate, 20+ resolved decisions, first-principles reviewed |
| 21 | Testing | ~495 | 7 - Finished | Test/production mode flag, 3-layer test architecture, E2E via CLI+API, deterministic models, state reset, coverage gates, 12 resolved decisions, first-principles reviewed |
| — | UI Spec for Figma | 583 | 1 - Stub | Updated alongside finished specs but not independently reviewed |
| — | README | 61 | — | Index document, not a spec |

## Deferred Cross-Spec Items

Items discovered during review that belong to a spec not yet under active review. Each item is resolved when the target spec reaches its own review phase.

| # | Source | Target | Item | Resolve When |
|---|--------|--------|------|--------------|
| D1 | 05 review | 04 | Add `max_concurrent_temps` to org settings shape example | Doc 04 next reviewed |
| D2 | 05 review | 16 | Verify doc 05 cross-references to control plane match doc 16 schema (policy evaluation, capability templates, audit trail) | Doc 16 reviewed |
| D3 | 05 review | all | Standardize system principal convention — some specs use `created_by_type = 'system'`, others don't define system as a valid principal | Each spec's review |
| D4 | 05 review | 04 | Confirm `system` is a recognized principal type in doc 04's auth model | Doc 04 next reviewed |
| D5 | 05 review | 02 | Add explicit Ellie auto-join rule (currently implied by enumeration across scope types but never stated as a rule) | Doc 02 next reviewed |
| D6 | 05 review | 02 | Clarify temp agent participation pattern in chat — doc 05 says temps don't chat with the human, enforcement is behavioral (PM controls assignment) not schema-level. Worth a note in doc 02's participant model | Doc 02 next reviewed |
| D7 | 08 review | 16 | Clarify how Worker processes interact with control plane broker for Tier 2 tool execution and RunStep tracking — is it in-process or separate? | Doc 16 reviewed |
| D8 | 08 review | 12 | ~~Verify doc 12's domain_event table design matches doc 08's description of durable table + LISTEN/NOTIFY fanout for event bus~~ **Resolved**: doc 12 now documents LISTEN/NOTIFY wake-up in event dispatch section, matching doc 08's description | Resolved |
| D9 | 20 review | 05 | Add `tool_allow_list` and `tool_deny_list` seed data values for the starter trio — doc 20 specifies the allow lists (Frank: project/task/session/etc, Lori: agent/read-only, Ellie: memory/file-read) but doc 05's seed data omits these columns | Doc 05 next reviewed |
| D10 | 20 review | 03 | Add optional `tool_domains` jsonb column to `flow_node` table — doc 20 declares it as a new field for stage 3 flow node filter, doc 03 does not have it | Doc 03 next reviewed |
| D11 | 21 review | 08 | Add `OTTERCAMP_MODE` (`test`/`production`, default `production`) to doc 08's environment variable table | Doc 08 next reviewed |
| D12 | 21 review | 12 | Add test-mode-only endpoints to doc 12's API catalog: `POST /test/reset`, `POST /test/time/advance`, `POST /test/seed/*` (gated by `OTTERCAMP_MODE=test`) | Doc 12 next reviewed |
| D13 | 14 review | 05 | Rename `budget_cap_cents` to `budget_cap_tokens` on agent table — all budget levels now use tokens as the unit (resolved in doc 14 Q24) | Doc 05 next reviewed |
| D14 | 14 review | 05 | Add `operator_instructions` text field to agent table — custom operator additions that are never overwritten by system upgrades. Effective prompt = `system_prompt` + `operator_instructions` (resolved in doc 14 Q9) | Doc 05 next reviewed |
| D15 | 14 review | 08 | Add `OTTERCAMP_MODE=local` as a third mode (alongside `test` and `production`) — auto-authenticates as bootstrap user, skips credential prompt (resolved in doc 14 Q5) | Doc 08 next reviewed |
| D16 | 14 review | 05 | Add `system_profile_version` field to agent table — tracks which shipped version the system_prompt was last updated from. On startup, compare with shipped version and auto-apply if newer (resolved in doc 14 Q9) | Doc 05 next reviewed |
| D17 | 14 review | 13 | Add tiered retention policy defaults to doc 13: memories forever, chat messages 1yr, model invocations 90d (rollups forever), domain events 90d, audit events 1yr, tool executions 90d. Configurable per org, expired data archived to object storage (resolved in doc 14 Q22) | Doc 13 next reviewed |
| D18 | 14 review | 09 | Add 3-5 starter connection templates for popular MCP servers (GitHub, Slack, Postgres, filesystem, web search) — pre-filled configs used by Frank to streamline setup (resolved in doc 14 Q16) | Doc 09 next reviewed |

## Summary

- **Finished:** 19 specs (01, 02, 03, 03a, 04, 05, 06, 07, 08, 09, 10, 11, 12, 13, 14, 15, 16, 20, 21)
- **Initial Draft:** 3 specs (17, 18, 19)
- **Stubs:** 1 (UI Spec for Figma)
- **Total:** 22 specs (+ README)
- **Total lines:** ~16,700
