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
| 01 | Architecture and Domain | 95 | 3 - In Process with Sam | Has product principles section |
| 02 | Chat | 1107 | 7 - Finished | Full schema, all open questions resolved |
| 03 | Projects and Task Flow | 914 | 7 - Finished | 12-table schema, first-principles reviewed |
| 03a | Shipping and Delivery | 344 | 7 - Finished | 2 new tables, 18 resolved decisions, first-principles reviewed |
| 04 | Auth, Tenancy, and Identity | ~640 | 7 - Finished | DB-per-org isolation, no org_membership, RBAC on human_user, 22 resolved decisions, first-principles reviewed |
| 05 | Agents, Staff, and Temps | 892 | 7 - Finished | Project-scoped temps, 4 scope types, temps get project access, NDI, 230+ profile catalog, 43 resolved decisions, first-principles reviewed |
| 06 | Memory | 1072 | 7 - Finished | 9-table schema, 52 resolved decisions, first-principles reviewed |
| 07 | Models and Inference | 714 | 2 - Initial Draft | Provider abstraction, model profiles, cost tracking, schema |
| 08 | Deployment and Self-Hosting | 906 | 3 - In Process with Sam | Three modes, Docker Compose, binary distribution, db-per-org managed mode, pgvector, catalog DB, per-org migration orchestration, all open questions resolved |
| 09 | MCP Integration | 659 | 2 - Initial Draft | Connection management, tool discovery, security, schema |
| 10 | Skills Integration | 545 | 2 - Initial Draft | Skill format, storage, activation, catalog, schema |
| 11 | System Integration (CLI and Browser) | 740 | 2 - Initial Draft | CLI sandboxing, browser actions, human handoff, artifacts |
| 12 | API, Events, and Realtime | 1093 | 2 - Initial Draft | REST + SSE, event system, endpoint catalog, schema |
| 13 | Security, Observability, Costs | 789 | 2 - Initial Draft | Threat model, secrets, retention, metrics, cost controls |
| 14 | Open Questions and Phasing | 784 | 2 - Initial Draft | Expanded build phases, bootstrap dataset, risk areas |
| 15 | Migration and Backward Compat | 368 | 2 - Initial Draft | Clean-room principles, JSONL import bridge, validation |
| 16 | Agent Control Plane | 1371 | 2 - Initial Draft | Full schema, capability templates, execution lifecycle |
| 17 | TUI | 813 | 2 - Initial Draft | Bubble Tea, layout, keyboard nav, command palette |
| 18 | Web UI | 884 | 2 - Initial Draft | React SPA, three-panel layout, command bar, design system |
| 19 | Mobile UI | 459 | 2 - Initial Draft | React Native, push notifications, quick actions |
| 20 | Tools and Tool Policy | 600 | 2 - Initial Draft | Tool taxonomy, two-tier execution, native catalog, policy |
| — | UI Spec for Figma | 494 | 1 - Stub | Updated alongside finished specs but not independently reviewed |
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
| D8 | 08 review | 12 | Verify doc 12's domain_event table design matches doc 08's description of durable table + LISTEN/NOTIFY fanout for event bus | Doc 12 reviewed |

## Summary

- **Finished:** 6 specs (02, 03, 03a, 04, 05, 06)
- **In Process:** 1 spec (01)
- **Initial Draft:** 14 specs (07, 08, 09, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20)
- **Stubs:** 1 (UI Spec for Figma)
- **Total:** 22 specs (+ README)
- **Total lines:** ~16,600
