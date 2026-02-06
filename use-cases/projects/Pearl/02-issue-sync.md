# Pearl — Issue & PR Sync

## Objective
Mirror GitHub issues/PRs into OtterCamp as native issues, with linkage back to GitHub.

## Sync Direction (Issues)
- **Inbound (GitHub → OtterCamp):** yes
- **Outbound (OtterCamp → GitHub):** *not yet*, except for **comment + close on deploy**

## Import Scope
- Import **all issues and PRs**, including closed.
- Tag origin: `origin = github`.
- Store GitHub metadata: repo, issue number, URL, author, labels, timestamps.

## Linkage Model
Create a link record:
- `otter_issue_id`
- `github_issue_id`
- `github_repo`
- `github_url`

OtterCamp issue stores link + origin.
GitHub issue receives a **comment** with OtterCamp issue ID.

## Lifecycle Rules
- **GitHub issue opened** → OtterCamp issue created.
- **GitHub issue updated** → OtterCamp issue updated.
- **OtterCamp issue resolved + publish** → GitHub issue is **commented + closed**.
- **OtterCamp does NOT create new GitHub issues** in MVP.

## External PR Workflow (MVP)
- **Inbound:** GitHub PRs are imported and linked to OtterCamp issues.
- **Review:** PRs are reviewed **inside OtterCamp** (summary, diff, discussion).
- **Decision:** Human chooses to **merge in GitHub** or **close** with comment.
- **Mirror:** OtterCamp records the decision + links the merge commit back to the issue.

*(We are not generating PRs from OtterCamp in MVP.)*

## Webhooks vs Polling
- **Webhook**: `issues`, `issue_comment`, `pull_request`, `push` events.
- **Polling**: periodic sync to catch missed events.

## Minimum API Surface (MVP)
- `POST /api/github/webhook` — ingest issue/PR events
- `POST /api/projects/:id/issues/import` — manual import
- `GET /api/projects/:id/issues/status` — counts + last sync

## Acceptance Criteria
- GitHub issues/PRs appear in OtterCamp with origin flags.
- Linkback comment exists on GitHub issue.
- Closing in OtterCamp (via publish) closes GitHub issue.
