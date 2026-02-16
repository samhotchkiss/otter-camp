# Issue #15: Visual & Theme Inconsistencies Across the App

## Problem

Multiple visual bugs where components don't match the active dark theme:

### 15.1 "Inbox0" Nav Badge — No Space Between Text and Count

The navigation bar shows `Inbox0` with the badge count "0" directly concatenated to "Inbox" with no visual separation. Should be `Inbox 0` with the count in a separate badge pill (matching how other counts are displayed, e.g., feed "11", chat "4").

**Location:** Top navigation bar, `<link "Inbox0">` is a single text node.

### 15.2 Agent Activity Tab — White Background on Dark Theme

The "Agent Activity" tab on the Feed page and the agent timeline page (`/agents/{slot}`) show "No agent activity yet." and "No activity events for this agent yet." inside a white/light background card.

This is a stark contrast to the dark brown/green theme used everywhere else. The empty state card should use the same dark card styling as other empty states (e.g., Inbox "No pending items" card, which is correctly dark).

### 15.3 Settings Page — Light Form Cards on Dark Theme

The entire Settings page (`/settings`) uses light-colored card backgrounds (near-white) for all sections (Profile, Notifications, Workspace, Integrations, GitHub, Appearance, Data Management). Every other page in the app uses dark-themed cards.

The Settings page appears to not have been themed at all — it looks like the default light UI framework styling was left in place.

### 15.4 List View — No Status Column

The List view on the project page shows tasks with checkbox, title, assignee, and priority — but no column indicating which status/column the task is in (Queued, In Progress, Review, Done). Without this, you can't tell the state of a task in list view.

Should show a status badge or group tasks by status with section headers.

### 15.5 Archived Project — No Visual Differentiation

"Three Stones" shows `Status: archived` on its project card, but the card looks identical to active projects. Archived projects should be visually distinct:
- Dimmed/grayed out appearance
- Moved to a separate "Archived" section, or
- Hidden by default with a "Show archived" toggle

### 15.6 Progress Bars Not Visible

Project cards have a "Progress" section with task counts (e.g., "0/8 tasks") but the progress bar beneath is invisible or barely visible. Projects with 0% progress show no bar at all, and even projects with tasks don't render a visible bar.

### 15.7 Dashboard Project Sidebar Inconsistency

The dashboard sidebar shows "No tasks yet" for most projects, while the Projects page shows "0/0 tasks". These should use the same phrasing. "No tasks yet" is friendlier; "0/0 tasks" is more precise. Pick one and use it consistently.

## Acceptance Criteria

- [ ] "Inbox" and its badge count are visually separated (badge in a pill, not concatenated text)
- [ ] All empty state cards use dark-themed styling consistent with the rest of the app
- [ ] Settings page cards use the app's dark theme (not default light framework styles)
- [ ] List view includes a status indicator for each task (badge, icon, or grouping)
- [ ] Archived projects are visually distinct from active projects (dimmed, labeled, or separated)
- [ ] Progress bars are visible and correctly filled based on completed/total task ratio
- [ ] Dashboard sidebar and Projects page use consistent task count phrasing

## Files to Investigate

- `web/src/components/nav/` or `web/src/components/layout/Header.tsx` — Nav bar with Inbox badge
- `web/src/pages/FeedPage.tsx` — Agent Activity tab styling
- `web/src/pages/AgentTimelinePage.tsx` — Empty state card styling
- `web/src/pages/SettingsPage.tsx` — All settings cards need theme fix
- `web/src/components/projects/ProjectList.tsx` — List view missing status column
- `web/src/components/projects/ProjectCard.tsx` — Archived styling, progress bar
- `web/src/pages/DashboardPage.tsx` — Sidebar project list phrasing
- `web/src/styles/` or `web/src/index.css` — Global theme variables

## Test Plan

```bash
# Visual tests — manual verification recommended, but component tests for:
cd web && npm test -- --grep "InboxBadge"
cd web && npm test -- --grep "SettingsPage theme"
cd web && npm test -- --grep "ProjectCard archived"
cd web && npm test -- --grep "TaskList status"
```

## Execution Log
- [2026-02-08 15:42 MST] Issue #362/#363/#364/#365/#366/#367 | Commit f9c03cb,d189be6,3104b58,c850fac,a6c3988,a270aea | reconciled | Verified existing Spec015 micro-issues were already implemented/closed in GitHub history | Tests: n/a
- [2026-02-08 15:42 MST] Issue spec #015 | Commit 04c1790 | reconciled | Confirmed merged implementation PR #372 and validated targeted visual/theme regressions locally | Tests: cd web && npm test -- src/layouts/DashboardLayout.test.tsx src/components/agents/AgentActivityTimeline.test.tsx src/pages/FeedPage.test.tsx src/pages/AgentDetailPage.test.tsx src/pages/SettingsPage.test.tsx src/pages/ProjectDetailPage.test.tsx src/pages/ProjectsPage.test.tsx src/lib/projectTaskSummary.test.ts --run
- [2026-02-08 20:00 MST] Issue spec local-state | Commit n/a | reconciled-folder-state | Reconciled local queue state: spec already implemented/reconciled in execution log; moved from `01-ready` to `03-needs-review` for review tracking | Tests: n/a
- [2026-02-08 21:01 MST] Issue spec local-state | Commit n/a | reconciled-folder-state | Re-ran preflight reconciliation and moved spec from `01-ready` to `03-needs-review` to match closed GitHub issues (#362-#367) and merged implementation history. | Tests: n/a
- [2026-02-08 21:18 MST] Issue spec #015 | Commit n/a | APPROVED | Josh S (Opus) review: All 7 acceptance criteria verified on main. 15.1: Inbox badge uses separate `<span className="nav-badge">` (not concatenated) ✅. 15.2: AgentActivityTimeline uses CSS vars (`var(--surface)`) not hardcoded white ✅. 15.3: SettingsPage has 51 `dark:` Tailwind classes ✅. 15.4: `LIST_STATUS_BADGE` provides status badges in list view ✅. 15.5: Archived projects get `opacity-70` ✅. 15.6: Progress bars implemented with `role="progressbar"` ✅. 15.7: Consistent task count phrasing ✅. No isolated branch (merged via PR #372). | Tests: `cd web && npx vitest run src/layouts/DashboardLayout.test.tsx src/pages/SettingsPage.test.tsx src/pages/ProjectsPage.test.tsx` — all pass
- [2026-02-08 21:06 MST] Issue spec local-state | Commit n/a | moved-to-completed | Reconciled local folder state after verification that associated GitHub work is merged/closed and implementation commits are present on main. | Tests: n/a
