# Task 180: POST /v1/auth/logout returns empty body; POST /v1/auth/change-password returns 404

Layer: L1
Effort: XS
Depends on: none

## Context

Two auth endpoint issues:

### 1. Logout returns empty body
`POST /v1/auth/logout` returns HTTP 200 but with an empty body (no JSON). This causes JSON parse errors in clients that expect a response. Should return `{"data": {}, "meta": {...}}` consistent with other endpoints.

### 2. Change password endpoint missing
`POST /v1/auth/change-password` returns 404 Not Found. The route doesn't exist.

## Required Fix

1. **Logout**: Add JSON response body `{"data": null, "meta": {...}}` to the logout handler
2. **Change password**: Implement `POST /v1/auth/change-password` with `{current_password, new_password}` body; verify current password, update hash, invalidate existing sessions

## Acceptance Criteria

- [ ] `POST /v1/auth/logout` returns valid JSON response with 200 status
- [ ] `POST /v1/auth/change-password` accepts `{current_password, new_password}` and returns 200 on success, 400 on wrong current password
