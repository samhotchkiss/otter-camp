# Pearl — GitHub Auth & Permissions

## Objective
Use a GitHub App for secure, installation‑scoped access.

## Auth Model (MVP)
- **GitHub App** installed via OtterCamp UI.
- User clicks **Connect GitHub** → GitHub App install → callback to OtterCamp.
- **Install scope:** per‑repo (not org‑wide) for MVP.
- Store installation ID per OtterCamp org/project.

## Required Permissions (MVP)
- **Contents:** Read & write (for pull/push)
- **Issues:** Read (import) + write (comment/close)
- **Pull requests:** Read (import PRs)
- **Metadata:** Read
- **Webhooks:** Receive push + issues + PR events

## Token Handling
- Use installation tokens (short‑lived, rotated automatically).
- Store only installation IDs + webhook secret in OtterCamp.

## Local Dev Fallback
- PAT may be used in dev for local testing only.

## Acceptance Criteria
- App installation flow works end‑to‑end.
- Webhook signature verified.
- Installation tokens scoped to repo(s).
