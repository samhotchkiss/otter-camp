# Test 008: Notification count and inbox count visible at sidebar bottom

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Notification count and inbox count visible at sidebar bottom
**Tested:** 2026-02-26 16:24
**Result:** FAIL

## How I Tested

1. Examined the sidebar panel top-to-bottom
2. Looked for any notification count or inbox count display at the bottom of the sidebar
3. Checked the SESSIONS title for count badges

## TUI Screen State

```
│ SESSIONS        │  ← no count badge
│ ─────────────── │
│   General / …   │
│ ▾ Project Al…   │
│   › Task 1… ✓ ○ │
│   › Task 2 /… ◌ │
│                 │  ← empty rows, no notification/inbox count
│                 │
...
╰─────────────────╯  ← bottom of sidebar, no counts shown
```

## Observed Behavior

The sidebar shows only:
- "SESSIONS" title (with optional unread total badge if any sessions have unreads — EX-012 feature)
- Session/project tree

There is NO notification count or inbox count displayed at the sidebar bottom. The dashboard main panel shows "Inbox 2" as a section, and the inbox count appears in the main panel title when on inbox view, but neither appears in the sidebar itself.

## Expected Behavior

Per spec: "Notification count and inbox count visible at sidebar bottom"

A persistent footer in the sidebar showing something like:
```
│ 📥 2 inbox  🔔 0 notif │
╰─────────────────────────╯
```

## Notes

The inbox count IS surfaced in the dashboard (Inbox section divider) and in the main panel title when on inbox view (EX-015). But the spec wants it in the sidebar. This is missing.

## Issue Filed

Issue #164 — Sidebar missing notification and inbox count at bottom
