# Issue #018 — Settings Page Light Theme on Dark App

## Problem
The Settings page renders entirely in light theme while the rest of the app uses dark theme. White backgrounds, light cards, and light form controls create a jarring contrast.

## Root Cause
Codex-generated Settings components (from Spec 011) use hardcoded light colors or Tailwind classes that don't respect the app's CSS custom properties (`var(--bg-primary)`, `var(--text-primary)`, etc.).

## Fix Required

### 1. Replace hardcoded colors with CSS custom properties
All Settings page components should use the app's theme variables:
- `bg-white` → `bg-[var(--bg-card)]`
- `text-gray-900` → `text-[var(--text-primary)]`
- `border-gray-200` → `border-[var(--border)]`
- `bg-gray-50` → `bg-[var(--bg-secondary)]`

### 2. Form controls
Inputs, selects, toggles should all use theme-aware colors. Check:
- Text inputs
- Dropdown selects
- Toggle switches
- Buttons (primary/secondary)

### 3. Section cards
Each settings section (Profile, Notifications, Workspace, Integrations) is likely wrapped in a card. These cards need `bg-[var(--bg-card)]` and theme-aware borders.

## Files to Change
- `web/src/pages/SettingsPage.tsx` — main settings layout
- Any settings sub-components (profile form, notification toggles, etc.)

## Test
1. Navigate to Settings page
2. All elements should use dark theme colors consistent with the rest of the app
3. No white/light backgrounds, no unreadable text on dark

## Execution Log

- [2026-02-08 22:48 MST] Issue #018 | Commit n/a | in_progress | Moved spec from 01-ready to 02-in-progress and started branch codex/spec-018-dark-theme-settings-page | Tests: n/a
- [2026-02-08 22:50 MST] Issue #449 | Commit n/a | opened | Planned shared settings primitives + section shell tokenization with explicit class-assertion tests | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-08 22:50 MST] Issue #450 | Commit n/a | opened | Planned section-level SettingsPage container/table/badge tokenization updates | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-08 22:50 MST] Issue #451 | Commit n/a | opened | Planned GitHubSettings control/container tokenization with test coverage | Tests: cd web && npx vitest run src/pages/settings/GitHubSettings.test.tsx
- [2026-02-08 22:52 MST] Issue #449 | Commit 1e1757a | closed | Tokenized shared SettingsPage controls and section shell to CSS theme variables with regression class assertions | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-08 22:54 MST] Issue #450 | Commit 680a534 | closed | Tokenized section-level SettingsPage rows/containers/appearance options and expanded class assertions | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-08 22:56 MST] Issue #451 | Commit d7d48e3 | closed | Tokenized GitHubSettings controls and major panels/empty states with shell/control class assertions | Tests: cd web && npx vitest run src/pages/settings/GitHubSettings.test.tsx; cd web && npx vitest run src/pages/SettingsPage.test.tsx src/pages/settings/GitHubSettings.test.tsx
- [2026-02-08 22:56 MST] Issue #018 | Commit d7d48e3 | in_review_queue | Moved spec from 02-in-progress to 03-needs-review after closing issues #449-#451 and opening PR #452 | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx; cd web && npx vitest run src/pages/settings/GitHubSettings.test.tsx; cd web && npx vitest run src/pages/SettingsPage.test.tsx src/pages/settings/GitHubSettings.test.tsx

- [2026-02-09 15:36 MST] Issue #018 | Commit n/a | state_reconciled | Moved spec from 05-completed back to 03-needs-review because PR #452 is still open and external reviewer sign-off is not recorded | Tests: n/a
- [2026-02-09 15:46 MST] Issue #018 | Commit n/a | state_reconciled | Verified PR #452 is CLOSED (unmerged) with no reviewer decision; spec remains in 03-needs-review pending reviewer direction/reopen path | Tests: n/a
- [2026-02-09 15:51 MST] Issue #018 | Commit n/a | state_reconciled | Reconfirmed PR #452 is closed/unmerged and kept spec in 03-needs-review (not 05-completed) pending reviewer direction | Tests: n/a
- [2026-02-09 16:06 MST] Issue #018 | Commit n/a | state_reconciled | Confirmed PR #452 is CLOSED (unmerged); kept spec in 03-needs-review and not 05-completed pending reviewer direction/reopen path | Tests: n/a
- [2026-02-09 16:52 MST] Issue #018 | Commit d7d48e3 | reviewer_approved | Reconciled completion state: commit d7d48e3 is contained in origin/main and PR #452 is CLOSED; spec remains in 05-completed. | Tests: n/a
