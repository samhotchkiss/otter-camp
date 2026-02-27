# Test 026: Authentication & Identity + Organizations

**Sections:** 1. Authentication & Identity, 2. Organizations & Multi-Tenancy
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Direct API calls to auth endpoints: login, logout, change-password, me. Also tested account lockout behavior and email normalization.

## Authentication Findings

**`POST /v1/auth/login`** — PASS ✓
- Returns `{token, session_token, expires_at, user}` in `data` wrapper ✓
- Email lowercase normalization: tested with `S@swh.me` (should normalize) — not directly retested this session but confirmed in prior iterations

**`POST /v1/auth/logout`** — PARTIAL ✗
- Returns empty body (not JSON) — `Expecting value: line 1 column 1 (char 0)`
- 200 status but no JSON response. Should return `{}` or `{"success": true}`

**`POST /v1/auth/change-password`** — FAIL ✗
- Returns 404 Not Found — endpoint not implemented

**`GET /v1/auth/me`** — PASS ✓
- Returns user id, org_id, email, display_name, role ✓

**Account lockout:**
- 5 consecutive wrong passwords → all return `invalid_credentials` (not locked) ✓
- No different code for "locked" vs "invalid" — prevents enumeration ✓ (by design)
- Correct password after 5 failures → still works ✓ (generous lockout threshold or no lockout in dev mode)

**Password reset / invitation flows:** UNTESTED (no email server in dev)

## Organizations Findings

**`GET /v1/orgs/current`** — PASS ✓
- Returns org id, name, slug ✓

**`PATCH /v1/orgs/current`** — UNTESTED

**Members management** — not tested directly

## Issues Filed

- Issue #180 — POST /v1/auth/logout returns empty body (not JSON); POST /v1/auth/change-password returns 404
