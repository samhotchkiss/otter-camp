# OtterCamp Agent Instructions

This document is the **source of truth** for how agents interact with OtterCamp. It applies to all agents (human or AI) producing work that OtterCamp should track.

## Core Model
- **Commits = activity.** The OtterCamp activity feed is driven by commits (and linked issues/PRs). If it matters, it must be committed.
- **OtterCamp = work product layer.** GitHub is the public mirror; OtterCamp is the canonical view of work.
- **No secrets in repos.** Never commit tokens, credentials, or private keys.

## Required Workflow
1. **Work in a repo.** All substantive work must live in a Git repo (code or content).
2. **Commit early and often.** Each meaningful change should be committed with a clear message.
3. **Push when done.** Push commits so OtterCamp can ingest them into the activity stream.

## Commit Message Format (Required)
Commit messages must include:

```
<short subject line (≈50 chars)>

<verbose description body>
```

**Examples:**
```
feat: add onboarding email sequence

Adds the first‑time user onboarding sequence (5 messages) with subject lines,
copy, and send schedule. Also includes a short rationale for the sequence order.
```

```
fix: normalize agent last active timestamps

Stores RFC3339 timestamps from OpenClaw sync and guards invalid values in the UI.
Prevents “Invalid Date” from appearing on the Agents page.
```

### Why the verbose body matters
OtterCamp’s Code Browser MVP surfaces the **commit body** as the expanded, human‑readable description of work. If the body is empty, the activity stream becomes low‑signal.

## What to Commit
- **Code changes** (features, fixes, refactors)
- **Content** (drafts, edits, research, assets)
- **Design** (mockups, assets, design docs)
- **Configuration** (non‑secret settings)
- **Documentation** (specs, notes, decisions)

## What NOT to Commit
- Secrets, API keys, tokens, private certs
- Personal data not explicitly approved

## Issue Linking (Preferred)
When possible, include the issue ID in the commit message subject (e.g., `fix(#123): …`).

## Sync Expectations
- OtterCamp will ingest commits and display them in the Activity Feed.
- GitHub issues may be imported and linked to OtterCamp issues (bi‑directional sync).
- Closing issues in OtterCamp should close linked GitHub issues when the fix is pushed.

## Manual Re‑Sync (Agent Trigger)
Agents may trigger a re‑sync via API when needed:

```
POST /api/projects/:id/repo/sync
POST /api/projects/:id/issues/import
```

Use these sparingly (after a batch of commits or when GitHub updated externally).

---
Questions or updates? Open a PR or issue in `otter-camp` and tag the maintainers.
