# Issue 123: API key scopes are defined but never enforced

## Problem

API keys can be created with specific scopes (e.g. `["read:projects"]`), but these
scopes are **never enforced** at the route level. Any API key can perform any action
the issuing user is authorized for, regardless of its declared scopes.

```bash
# Create a read-only chat key
curl -X POST /v1/api-keys -d '{"display_name":"limited","scopes":["read:chat"]}'
# Token: otk_...

# Use it to create a project (should be forbidden by scope)
curl -X POST /v1/projects -H "Authorization: Bearer otk_..." \
  -d '{"display_name":"Attacker Project"}'
# → 201 Created  ← BUG: scope should block this
```

## Root Cause

`internal/middleware/rbac.go` has `RequireScope()` middleware that correctly
checks API key scopes. However, it is **never applied to any route**. All routes
use only `RequireRole("member")` or `RequireRole("admin")`, which check the
issuing user's role (not the key's declared scopes).

## Security Impact

API key scopes are misleading — users and documentation imply that creating a
`read:projects`-scoped key limits its capability, but it doesn't. A stolen or
leaked API key has full access to everything the issuing user can do.

## Fix

Apply `RequireScope` to routes. The scope naming convention should follow the
pattern `{action}:{resource}` where action is `read`, `write`, or `admin`.

Minimum enforcement needed for common scopes:
- `read:projects`, `write:projects` → project CRUD routes
- `read:chat`, `write:chat` → chat session routes
- `read:memory`, `write:memory` → memory routes
- `read:agents`, `write:agents` → agent routes
- `admin:*` → admin-only routes

## Files to Change

- `internal/server/project_handlers.go` — add RequireScope to project routes
- `internal/server/chat_handlers.go` — add RequireScope to chat routes
- `internal/server/agent_handlers.go` — add RequireScope to agent routes
- `internal/server/memory_handlers.go` — add RequireScope to memory routes
- All other route registrars
