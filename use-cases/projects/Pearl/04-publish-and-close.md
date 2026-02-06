# Pearl — Publish to GitHub + Close Issues

## Objective
When work is complete in OtterCamp, push changes to GitHub **main** and close linked GitHub issues.

## Publish Flow
1. **Human triggers publish** from OtterCamp.
2. OtterCamp pushes commits to **GitHub main**.
3. For each linked GitHub issue:
   - Post a comment with commit link(s).
   - Close the GitHub issue.

## Rules
- **No PR creation** in MVP — OtterCamp pushes directly to `main`.
- Publishing is **explicitly human‑initiated**.

## Comment Format (Suggested)
```
Resolved in OtterCamp.
Commit: <link>
OtterCamp issue: <id>
```

## Failure Paths (MVP)
- **Push fails:** show error, leave OtterCamp issue open, and log failure in activity feed.
- **Conflict on push:** prompt user to re‑sync (GitHub → OtterCamp) and retry.

## Acceptance Criteria
- Push succeeds to GitHub main.
- Linked GitHub issues receive a comment + closure event.
- OtterCamp records a publish event in activity log.
