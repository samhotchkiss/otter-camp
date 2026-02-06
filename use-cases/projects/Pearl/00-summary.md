# Pearl — Use Case Summary

**Project type:** Open‑source software development (team‑owned)

**Core model:** OtterCamp is the work‑product layer. GitHub is a public mirror.

## Goals
- Ingest Pearl’s full Git history + code into OtterCamp.
- Ingest all GitHub issues/PRs into OtterCamp as native issues.
- Preserve bi‑directional sync with guardrails (code + issues).
- Provide a **commit‑first** Code Browser MVP (commit → description → diff).
- Push finished work back to GitHub **main** and close linked issues.

## Key Decisions (from Sam)
1. **Repo sync scope:** default branch + active feature branches only.
2. **Issue import:** full history (open + closed).
3. **Sync cadence:** webhooks + periodic poll + manual re‑sync button.
4. **Code conflicts:** ask every time (keep GitHub or keep OtterCamp).
5. **Issue closure:** auto‑comment on GitHub with commit link, then close.
6. **Repo mapping:** one OtterCamp project ↔ zero or one GitHub repo (for now).
7. **Push permissions:** human‑initiated push to GitHub.

## What Users See
- **Project interface** shows code, commits, issues, and activity.
- **Code browser** emphasizes commit descriptions; diffs are available but not primary.
- **Activity feed** shows commits as the canonical activity stream.

## Activity Feed Source (Pearl)
- GitHub **push** events and periodic syncs write commit entries into `activity_log`.
- These entries drive the **Dashboard Activity Feed**.

## Files in this Use Case
- `01-repo-ingest.md` — repo sync model + branch handling
- `02-issue-sync.md` — GitHub issue/PR ingest + linkage
- `03-code-browser-mvp.md` — commit‑first UI spec
- `04-publish-and-close.md` — push to GitHub + issue closure
- `05-auth-permissions.md` — GitHub App auth model
- `06-resync-and-polling.md` — webhook + polling + manual resync
