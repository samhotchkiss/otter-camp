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
| 05 | Agents, Staff, and Temps | 691 | 2 - Initial Draft | Starter trio, prompt assembly, lifecycle, schema |
| 06 | Memory | 1072 | 7 - Finished | 9-table schema, 52 resolved decisions, first-principles reviewed |
| 07 | Models and Inference | 714 | 2 - Initial Draft | Provider abstraction, model profiles, cost tracking, schema |
| 08 | Deployment and Self-Hosting | 800 | 2 - Initial Draft | Three modes, Docker Compose, binary distribution, upgrades |
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

## Summary

- **Finished:** 5 specs (02, 03, 03a, 04, 06)
- **In Process:** 1 spec (01)
- **Initial Draft:** 15 specs (05, 07, 08, 09, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20)
- **Stubs:** 1 (UI Spec for Figma)
- **Total:** 22 specs (+ README)
- **Total lines:** ~16,600
