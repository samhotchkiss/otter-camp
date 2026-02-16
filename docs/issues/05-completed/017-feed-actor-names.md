# Issue #017 — Feed Actor Names Show "System" for Git Pushes

## Problem
On the Dashboard feed, git push events show "System pushed to Technonymous" instead of the actual pusher's name (e.g., "Sam pushed to Technonymous" or "Derek pushed to Otter Camp").

## Root Cause
The `ActivityPanel.tsx` component extracts actor name from `record.actor`, `record.user`, or `record.agentName`, falling back to "System". Git push webhook payloads from GitHub don't include these fields — the pusher info is in different fields (e.g., `pusher.name`, `sender.login`).

## Fix Required

### 1. Backend: Normalize actor on git push events
In `internal/api/webhooks.go` (or wherever git push webhooks are processed), extract the pusher name from the GitHub webhook payload:
```go
pusherName := payload.Pusher.Name // or payload.Sender.Login
```
Store this as the `actor` field in the activity record.

### 2. Frontend: Expand actor field resolution
In `web/src/components/ActivityPanel.tsx`, add fallbacks:
```tsx
const actorName =
  (record.actor as string) ??
  (record.user as string) ??
  (record.agentName as string) ??
  (record.pusher_name as string) ??
  (record.sender_login as string) ??
  "System";
```

### 3. Also fix: "Git Push" summary text is redundant
Current: "System pushed to Technonymous" + "Git Push • 1d ago"
The "pushed to X" already says it's a push. Consider: "Sam • Technonymous" + "Git Push • 1d ago"

## Files to Change
- `internal/api/webhooks.go` — extract pusher name from webhook payload
- `web/src/components/ActivityPanel.tsx` — expand actor resolution

## Test
1. Push a commit to an Otter Camp project repo
2. Dashboard feed should show the actual pusher name, not "System"

## Execution Log

- [2026-02-08 22:34 MST] Issue #017 | Commit n/a | in_progress | Moved spec from 01-ready to 02-in-progress and started execution on branch codex/spec-017-feed-actor-names | Tests: n/a
- [2026-02-08 22:39 MST] Issue #445 | Commit n/a | opened | Planned backend webhook actor metadata normalization for GitHub push events | Tests: go test ./internal/api -run TestGitHubWebhookPushIngestsCommitsAndUpdatesBranchCheckpoint -count=1; go test ./internal/api -run TestGitHubWebhookEnqueueAndReplayProtection -count=1
- [2026-02-08 22:39 MST] Issue #446 | Commit n/a | opened | Planned feed actor fallback SQL updates for sender_login/push metadata resolution | Tests: go test ./internal/api -run 'TestFeedHandlerV2(ResolvesGitPushActorFromMetadataUserID|ResolvesGitPushActorFromSenderLogin|UsesSystemWhenGitPushActorCannotBeResolved|NormalizesUnknownActorToSystem)' -count=1
- [2026-02-08 22:39 MST] Issue #447 | Commit n/a | opened | Planned frontend Dashboard/activity actor fallback and git.push description de-dup updates | Tests: cd web && npx vitest run src/pages/Dashboard.test.tsx; cd web && npx vitest run src/components/activity/activityFormat.test.ts; cd web && npx vitest run src/components/activity/__tests__/ActivityPanel.test.tsx
- [2026-02-08 22:42 MST] Issue #445 | Commit c3b301d | closed | Added GitHub push actor metadata normalization/persistence for webhook + commit-ingested activity rows | Tests: go test ./internal/api -run TestGitHubWebhookPushIngestsCommitsAndUpdatesBranchCheckpoint -count=1 (blocked by settings_test compile failure); go test ./internal/api -run TestGitHubWebhookEnqueueAndReplayProtection -count=1 (blocked by settings_test compile failure); go build ./internal/api
- [2026-02-08 22:44 MST] Issue #446 | Commit 44b9455 | closed | Extended feed actor fallback SQL (sender_login/sender_name) and added git.push sender_login regression test | Tests: go test ./internal/api -run 'TestFeedHandlerV2(ResolvesGitPushActorFromMetadataUserID|ResolvesGitPushActorFromSenderLogin|UsesSystemWhenGitPushActorCannotBeResolved|NormalizesUnknownActorToSystem)' -count=1 (blocked by settings_test compile failure); go build ./internal/api
- [2026-02-08 22:47 MST] Issue #447 | Commit c68db9c | closed | Updated Dashboard + activity panels actor fallback resolution and de-duplicated git.push wording | Tests: cd web && npx vitest run src/pages/Dashboard.test.tsx; cd web && npx vitest run src/components/activity/activityFormat.test.ts; cd web && npx vitest run src/components/activity/__tests__/ActivityPanel.test.tsx src/components/__tests__/ActivityPanel.test.tsx; cd web && npx vitest run src/pages/Dashboard.test.tsx src/components/activity/activityFormat.test.ts src/components/activity/__tests__/ActivityPanel.test.tsx src/components/__tests__/ActivityPanel.test.tsx
- [2026-02-08 22:48 MST] Issue #017 | Commit c68db9c | in_review_queue | Moved spec from 02-in-progress to 03-needs-review after closing issues #445-#447 and opening PR #448 | Tests: see per-issue entries
