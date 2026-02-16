# Memories: Secret Management

> Summary: Secrets and environment settings required for memory + bridge paths, with separation between local and hosted usage.
> Last updated: 2026-02-16
> Audience: Agents/operators configuring secure runtime.

## Bridge/Auth Secrets

Required for bridge-connected operation:
- `OPENCLAW_WS_SECRET` (for `/ws/openclaw`)
- `OPENCLAW_SYNC_SECRET` (or legacy aliases)
- `OPENCLAW_AUTH_SECRET` (auth integration path)

Reference source (before reorg): `docs/OPENCLAW-SECRETS.md` (now folded into this file).

## Embedding Secrets

If using OpenAI-compatible embedder:
- `CONVERSATION_EMBEDDER_OPENAI_API_KEY`

## Local Safety Notes

- Avoid committing `.env` with live secrets.
- Keep bridge `.env` values aligned with API runtime values.
- Local-only shortcuts (like insecure magic auth) should remain disabled outside local dev.

## Change Log

- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
