# OtterCamp Issue Model

This document defines the **issue state machine**, required telemetry, and auto‑kick rules.

## State Machine (Canonical)
1. **In Consideration** — still being scoped or debated
2. **To Be Done** — defined, queued for assignment
3. **Working On** — active development by an agent
4. **Needs Review (Agent)** — automated review/QC pending
5. **Needs Review (Human)** — waiting on human review
6. **Needs Human Input** — blocked specifically by human decision
7. **Blocked** — external dependency (another issue, external system)
8. **Failed** — agent errored or execution failed
9. **Completed** — shipped/merged/deployed
10. **Won’t Do** — explicitly rejected

> **Note:** “Needs Human Input” is distinct from “Blocked” to keep human‑action visibility explicit.

## Required Telemetry (Always Visible)
- **Current state**
- **Owner agent**
- **Time in state** (elapsed)
- **Last activity timestamp**
- **Blocker reason** (when in Blocked/Needs Human Input)

## Auto‑Kick Rules (MVP)
- **Working On idle > N minutes** → flag as “stalled” + notify owner
- **Agent session error** → move to **Failed** + notify owner
- **Blocked > N hours** → prompt for update

> Thresholds should be configurable per project.

## Transitions (Examples)
- In Consideration → To Be Done (scope finalized)
- To Be Done → Working On (agent assigned)
- Working On → Needs Review (Agent or Human)
- Needs Review → Completed (approved + merged)
- Any → Failed (execution error)
- Any → Blocked (external dependency discovered)

## Ownership & Participation
- **Owner** is responsible for progress updates and delivery.
- Additional agents can be added to the issue thread as collaborators.

## Activity Feed Integration
Issue state changes should emit activity events (e.g., “Issue moved to Working On”).
