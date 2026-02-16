# Issue #9: Activity Feed Shows Empty + "Unknown" Agent Names

## Problem

There are three separate but related activity/feed bugs:

### 9.1 Feed Page Shows "No activity yet" Despite Dashboard Having Items

The dashboard (`/`) shows 11 feed items (git pushes, agent online events, code pushes, task comments, task assignments). But the dedicated Feed page (`/feed`) shows:

- **Realtime tab**: "No activity yet — Activity will appear here as it happens."
- **Agent Activity tab**: "No agent activity yet."

The dashboard and the feed page appear to pull from different data sources, or the feed page only shows real-time WebSocket events (not historical).

### 9.2 Git Push Events Show "Unknown" Agent

On both the dashboard feed and the project Activity tab, git push events show:

```
U  Unknown                    1d ago
   git.push: git.push
```

Instead of showing the actual user who pushed (e.g., "Sam" or "Frank"). The git push webhook/event doesn't resolve the pusher identity.

### 9.3 Activity Text Is Redundant

Git push activity entries show `git.push: **git.push**` — the event type is repeated as the event description. Should show something useful like:

```
git.push: pushed 3 commits to main
```

Or at minimum just the commit message summary.

## Acceptance Criteria

- [ ] Feed page (`/feed`) Realtime tab shows historical feed events (same data visible on dashboard), not just real-time WebSocket events
- [ ] Feed page Agent Activity tab shows agent status changes, task actions, etc.
- [ ] Git push events resolve the pusher's name from the webhook payload (git author, committer, or push sender)
- [ ] If pusher identity can't be resolved, show "System" (not "Unknown")
- [ ] Activity description for git push shows useful info (commit message, branch, file count) instead of repeating the event type
- [ ] Activity items on the project Activity tab match the same improvements

## Files to Investigate

- `web/src/pages/FeedPage.tsx` — Feed page component, check data fetching
- `web/src/pages/DashboardPage.tsx` — Dashboard feed, compare data source
- `internal/api/feed.go` or `internal/api/activity.go` — Feed API endpoint
- `internal/api/webhooks.go` or `internal/api/git_hooks.go` — Git push event handler that creates feed items
- `internal/store/feed_store.go` or similar — How feed items are stored and queried
- Search for "Unknown" in frontend components that render agent names in activity feeds

## Test Plan

```bash
# Backend
go test ./internal/api -run TestFeedEndpoint -count=1
go test ./internal/api -run TestGitPushWebhookCreatesActivityWithAuthor -count=1
go test ./internal/store -run TestFeedStoreHistorical -count=1

# Frontend
cd web && npm test -- --grep "FeedPage"
cd web && npm test -- --grep "activity"
```

## Execution Log
- [2026-02-08 14:57 MST] Issue spec #009 | Commit n/a | in-progress | Moved spec from `01-ready` to `02-in-progress`; next step is full micro-issue planning with explicit tests before coding | Tests: n/a
- [2026-02-08 15:02 MST] Issue #380,#381,#382,#383,#384 | Commit n/a | planned | Created full Spec009 micro-issue set with explicit tests and dependencies before coding | Tests: n/a
- [2026-02-08 15:04 MST] Issue #380 | Commit c86e007 | closed | Unified /api/feed to enriched handler and normalized unresolved actor names to System with router regression coverage | Tests: go test ./internal/api -run 'Test(RouterFeedEndpointUsesV2Handler|NormalizeFeedActorName|FeedHandlerV2(ResolvesGitPushActorFromMetadataUserID|UsesSystemWhenGitPushActorCannotBeResolved|NormalizesUnknownActorToSystem))' -count=1; go test ./internal/api -run TestRouterFeedEndpointUsesV2Handler -count=1 -v
- [2026-02-08 15:06 MST] Issue #381 | Commit 9e2b6ba | closed | Aligned ActivityPanel to env API host and added auth/org headers with regression coverage | Tests: cd web && npm test -- src/components/activity/__tests__/ActivityPanel.test.tsx src/pages/FeedPage.test.tsx --run
- [2026-02-08 15:07 MST] Issue #382 | Commit 5a72680 | closed | Broadened FeedPage agent fallback to retain historical feed items when /api/activity/recent is empty | Tests: cd web && npm test -- src/pages/FeedPage.test.tsx --run
- [2026-02-08 15:08 MST] Issue #383 | Commit a0882bb | closed | Updated activity description formatting to ignore type-echo summaries and prefer metadata-derived git.push text | Tests: cd web && npm test -- src/components/activity/activityFormat.test.ts --run
- [2026-02-08 15:10 MST] Issue #384 | Commit eed1f14 | closed | Added ProjectDetail git.push regression and normalized placeholder actor/summary rendering to System + metadata-derived text | Tests: cd web && npm test -- src/components/activity/activityFormat.test.ts src/pages/ProjectDetailPage.test.tsx --run; cd web && npm test -- src/pages/ProjectDetailPage.test.tsx --run
- [2026-02-08 15:12 MST] Spec #009 | Commit eed1f14 | moved-to-needs-review | All planned micro-issues (#380-#384) closed; branch pushed and PR opened at https://github.com/samhotchkiss/otter-camp/pull/385 | Tests: go test ./internal/api -run 'Test(RouterFeedEndpointUsesV2Handler|NormalizeFeedActorName|FeedHandlerV2(ResolvesGitPushActorFromMetadataUserID|UsesSystemWhenGitPushActorCannotBeResolved|NormalizesUnknownActorToSystem))' -count=1; cd web && npm test -- src/components/activity/__tests__/ActivityPanel.test.tsx src/pages/FeedPage.test.tsx src/components/activity/activityFormat.test.ts src/pages/ProjectDetailPage.test.tsx --run
