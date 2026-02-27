# Task 178: GET/POST /v1/projects/:id/agents returns 404 — project agent assignment not implemented

Layer: L2
Effort: S
Depends on: none

## Context

The spec requires:
- `GET /v1/projects/:id/agents` — list agents assigned to project
- `POST /v1/projects/:id/agents` — assign agent to project
- `DELETE /v1/projects/:id/agents/:agent_id` — unassign agent from project

All three return 404, meaning the routes are not registered.

Currently, agents are assigned to tasks via `assigned_agent_id` field on tasks. But project-level agent assignment (which agents can participate in this project) is not implemented.

## Required Fix

1. Add route handler for `GET /v1/projects/:id/agents`
2. Add route handler for `POST /v1/projects/:id/agents`
3. Add route handler for `DELETE /v1/projects/:id/agents/:agent_id`
4. Use a `project_agents` join table (or similar) to track project membership

## Acceptance Criteria

- [ ] `GET /v1/projects/:id/agents` returns list of agents assigned to project
- [ ] `POST /v1/projects/:id/agents` with agent_id assigns agent to project
- [ ] `DELETE /v1/projects/:id/agents/:agent_id` removes agent from project
