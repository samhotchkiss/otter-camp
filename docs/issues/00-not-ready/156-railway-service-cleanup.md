# 156 — Railway Service Cleanup

**Priority:** P2
**Dependencies:** None
**Status:** Not Ready

## Problem

Railway project `ottercamp` has 8 services but only 3 are needed. The extras trigger unnecessary builds on every commit, waste build minutes, and caused a 16-hour outage on `www` and `frontend` due to Dockerfile resolution bugs.

## Root Cause (Fixed)

Railway resolves `dockerfilePath: "Dockerfile"` relative to the **repo root**, not the service's `rootDirectory`. Both `www` and `frontend` were picking up the root Dockerfile (Go + Node multi-stage) instead of their own Dockerfiles.

**Fix applied:** Changed `dockerfilePath` to explicit paths (`www/Dockerfile`, `web/Dockerfile`) in both `railway.json` files and via Railway API. Both services deploying successfully now.

## Current Service Inventory

| Service | Root Dir | Dockerfile | Domain | Status | Verdict |
|---|---|---|---|---|---|
| **api** | `/` | `Dockerfile` (root) | `api.otter.camp` | ✅ Working | **KEEP** — Go backend |
| **frontend** | `/web` | `web/Dockerfile` | `sam.otter.camp` | ✅ Fixed | **KEEP** — Vite/React app |
| **www** | `www` | `www/Dockerfile` | `otter.camp` | ✅ Fixed | **KEEP** — Marketing site (nginx) |
| **dashboard** | `/` | `Dockerfile` (root) | *(none)* | ❌ Failing | **DELETE** — No domain, duplicate of api |
| **web** | — | — | `app.otter.camp` | ❓ No config | **DELETE** or reassign domain |
| **app** | — | — | `*.otter.camp` | ❓ Wildcard | **DELETE** or reassign wildcard |
| **Otter Camp** | — | — | *(none)* | ❓ Empty | **DELETE** |
| **otter-holt** | — | — | *(none)* | ❓ Empty | **DELETE** |

## Tasks

### 1. Delete Orphan Services
- [ ] Delete `dashboard` service (no domain, duplicate root Dockerfile build)
- [ ] Delete `Otter Camp` service (empty, no config)
- [ ] Delete `otter-holt` service (empty, no config)

### 2. Evaluate Domain Services
- [ ] Decide: does `app.otter.camp` need to exist? If so, point it at `frontend`
- [ ] Decide: does `*.otter.camp` wildcard need to exist? If so, move to `frontend` or `api`
- [ ] Delete `web` and `app` services after domain decisions

### 3. Add Watch Patterns to API
- [ ] Add watch patterns to `api` service so it doesn't rebuild on `www/` or `web/` only changes
- [ ] Suggested: `["*.go", "go.*", "cmd/**", "internal/**", "migrations/**", "web/**", "Dockerfile", "railway.json"]`

### 4. Clean Up Duplicate Dockerfiles
- [ ] Remove numbered duplicates: `Dockerfile 2`, `Dockerfile 3`, `Dockerfile 4`, `web/Dockerfile 2-5`, `www/Dockerfile 2-5`
- [ ] Remove `Dockerfile.web` if unused

## Railway Config Reference
- **Project ID:** `cd70f3b7-013a-4d2a-a391-adac7d855d95`
- **Environment:** `28210c73-ccd2-47fd-9e0f-64df5c3baf67` (production)
- **Service IDs:**
  - api: `4f30fa36-a8e8-474f-8572-be2220a69823`
  - frontend: `9a28a38b-4862-4029-b818-f912c7bd1cfc`
  - www: `143b774d-f049-4664-b9ec-ac516769062b`
  - dashboard: `14ac6788-6431-44c4-8442-d21f9f114df2`
  - web: `ae4cf14e-b667-49ac-8c35-1be3362ef079`
  - app: `dbc5a6dc-0c4d-4719-9b24-248abb38d5e4`
  - Otter Camp: `c563b18f-e4f9-4fd4-853d-03cb25e0dbc8`
  - otter-holt: `ea802cd7-4be8-4c2b-8ce6-3349ed4eae3b`

## Acceptance Criteria
- [ ] Only 3 services remain: api, frontend, www
- [ ] All custom domains resolve correctly
- [ ] No unnecessary builds triggered on commits
- [ ] No duplicate Dockerfiles in repo
