# OtterCamp Infrastructure

> Current production environment and deployment guide.

## Overview

OtterCamp runs on **Railway** with two services:

| Service | URL | Purpose |
|---------|-----|---------|
| **API** | `api.otter.camp` | Go backend (REST API) |
| **Frontend** | `sam.otter.camp` | React app (Vite + nginx) |

Railway Project: https://railway.app/project/cd70f3b7-013a-4d2a-a391-adac7d855d95

---

## Domains

All `*.otter.camp` domains point to Railway:

- `sam.otter.camp` → Frontend (React dashboard)
- `api.otter.camp` → API (Go backend)
- `otter.camp` → Landing page (Cloudflare Pages, separate)

DNS is managed via Cloudflare.

---

## Services

### API Service

- **Runtime:** Go 1.24 (golang:1.24-alpine)
- **Port:** Dynamic via `$PORT`
- **Dockerfile:** `/Dockerfile` (root)
- **Health check:** `GET /health`

**Environment Variables:**
```
PORT=<dynamic>
DATABASE_URL=<railway postgres>
```

### Frontend Service

- **Runtime:** Node 20 → nginx:alpine
- **Port:** Dynamic via `$PORT` (nginx uses envsubst)
- **Dockerfile:** `/web/Dockerfile`
- **Build:** `npx vite build` (skips TypeScript errors)

**Key files:**
- `web/Dockerfile` — Multi-stage build (node → nginx)
- `web/nginx.conf` — Uses `${PORT}` for Railway's dynamic port
- `web/railway.json` — Railway-specific config

**Environment Variables:**
```
PORT=<dynamic>
VITE_API_URL=https://api.otter.camp
```

---

## Deployment

### From CLI (recommended)

```bash
# Deploy frontend
cd web
railway up

# Deploy API
cd ..
railway up
```

**Note:** `railway up` uploads the current directory. Use this when you need direct control.

### From GitHub

Railway can auto-deploy on push to `main`. Currently disabled — we use manual deploys via CLI for more control.

---

## Local Development

### Frontend
```bash
cd web
npm install
npm run dev
# → http://localhost:5173
```

### API
```bash
go run ./cmd/server
# → http://localhost:8080
```

### Full Stack
```bash
# Terminal 1: API
go run ./cmd/server

# Terminal 2: Frontend (pointing to local API)
cd web
VITE_API_URL=http://localhost:8080 npm run dev
```

---

## Railway CLI Setup

```bash
# Install
npm install -g @railway/cli

# Login (opens browser)
railway login

# Link to project (run from repo root)
railway link
```

**Note:** API tokens are currently unreliable. Use browser-based login via `railway login`.

---

## Troubleshooting

### Build Fails

1. **Go version mismatch:** Ensure `go.mod` and `Dockerfile` use same Go version (1.24)
2. **TypeScript errors:** Frontend uses `npx vite build` directly (skips tsc)
3. **Port issues:** Both services must use dynamic `$PORT` from Railway

### Old Content After Deploy

Railway deploys are fast, but browsers may cache:
- Hard refresh: `Cmd+Shift+R`
- Add cache buster: `?v=2`
- Check Railway logs for successful deploy

### Wrong Dockerfile Used

Railway might pick the wrong Dockerfile. Solutions:
1. Set `rootDirectory` in service settings
2. Use `railway up` from the correct directory
3. Create service without GitHub link, deploy via CLI

---

## Related Docs

- [ARCHITECTURE.md](./ARCHITECTURE.md) — System design
- [API-REFERENCE.md](./API-REFERENCE.md) — API endpoints
- [DESIGN-SPEC.md](./DESIGN-SPEC.md) — Frontend design system
