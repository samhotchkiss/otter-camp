# Issue #3: Navigation Cleanup & Chat Fullscreen Mode

## Summary

Clean up the top navigation bar — move secondary pages into a user dropdown menu, make the logo a home link, and add a fullscreen mode for global chat.

## Current State

The topbar currently shows **8 nav links** in a flat horizontal row:
```
Dashboard | Inbox | Projects | Agents | Workflows | Knowledge | Connections | Feed
```

The "otter.camp" logo links to `/` but "Dashboard" is also a separate nav item linking to `/`. The avatar button (`S`) in the top-right has no dropdown — it's a dead button.

The global chat dock opens as a side panel; there's no way to expand it to full screen.

## Changes

### 1. Logo → Dashboard, Remove Dashboard Nav Item

**File:** `web/src/layouts/DashboardLayout.tsx`

- The 🦦 `otter.camp` logo already links to `/` — no change needed there.
- **Remove** the `{ id: "dashboard", label: "Dashboard", href: "/" }` entry from `NAV_ITEMS`.
- Update `getActiveNavId()` — when on `/` with no other match, don't highlight any nav item (or optionally add a subtle visual to the logo itself).

Remaining primary nav: **Inbox | Projects | Workflows | Knowledge**

### 2. User Avatar Dropdown Menu

**File:** `web/src/layouts/DashboardLayout.tsx`

Move **Agents**, **Connections**, and **Feed** out of the main nav and into a dropdown menu under the avatar button.

The avatar button currently renders as:
```tsx
<button type="button" className="avatar" aria-label="User menu">S</button>
```

Replace with a dropdown component containing:

| Menu Item | Route | Notes |
|-----------|-------|-------|
| Agents | `/agents` | Existing page |
| Connections | `/connections` | Existing page |
| Feed | `/feed` | Existing page |
| Settings | `/settings` | Stub — `SettingsPage` already imported in router.tsx |
| *divider* | — | — |
| Log Out | — | Clears auth cookie/localStorage, redirects to `/` or login |

**Behavior:**
- Click avatar → toggle dropdown
- Click outside or press Escape → close
- Click menu item → navigate + close
- Dropdown appears below-right of the avatar, doesn't overflow viewport

**Remove** from `NAV_ITEMS`: `agents`, `connections`, `feed`.

**Final primary nav:** `Inbox | Projects | Workflows | Knowledge`

### 3. Settings Page Stub

**File:** `web/src/pages/SettingsPage.tsx` (likely already exists — it's imported in router.tsx)

If the page exists, just make sure it's routed at `/settings`. If not, create a stub:
```tsx
export default function SettingsPage() {
  return (
    <div className="page-container">
      <h1>Settings</h1>
      <p>Settings page coming soon.</p>
    </div>
  );
}
```

Ensure `/settings` is in the router if not already.

### 4. Log Out Action

Implement a `logOut()` function:
1. Clear `otter_auth` cookie
2. Clear any localStorage keys (`otter-camp-org-id`, `otter-camp-token`, etc.)
3. Redirect to `/` (or a login page if one exists)

Wire this to the "Log Out" menu item in the avatar dropdown.

### 5. Global Chat Fullscreen Mode

**Files:** `web/src/components/chat/GlobalChatDock.tsx`, CSS

Add a fullscreen toggle button to the chat dock header (next to minimize/close). When activated:

- Chat panel expands to fill the entire viewport (or the main content area below the topbar)
- Nav and other content are hidden or overlaid
- Toggle button switches to "exit fullscreen" icon
- Escape key exits fullscreen
- Chat functionality (conversations list, message input, agent responses) works identically

**Implementation approach:** Add a `fullscreen` state to `GlobalChatDock`. When true, apply a CSS class that:
- Sets `position: fixed` (or `absolute` over `main`)
- `top: var(--topbar-height)` / `left: 0` / `right: 0` / `bottom: 0`
- `z-index` above main content but below modals
- Transitions smoothly

## Testing

- [ ] Clicking `otter.camp` logo navigates to dashboard
- [ ] "Dashboard" no longer appears in nav
- [ ] Primary nav shows only: Inbox, Projects, Workflows, Knowledge
- [ ] Avatar click opens dropdown with: Agents, Connections, Feed, Settings, Log Out
- [ ] Dropdown closes on outside click and Escape
- [ ] Each dropdown item navigates correctly
- [ ] Settings page renders (even if just a stub)
- [ ] Log Out clears auth state and redirects
- [ ] Chat fullscreen toggle expands chat to fill viewport
- [ ] Escape exits chat fullscreen
- [ ] Chat is fully functional in fullscreen mode
- [ ] Mobile menu reflects the same nav changes (remove Dashboard, Agents, Connections, Feed from mobile nav — or keep them in mobile since there's no avatar dropdown on mobile)

## Files to Modify

- `web/src/layouts/DashboardLayout.tsx` — nav items, avatar dropdown
- `web/src/components/chat/GlobalChatDock.tsx` — fullscreen toggle
- `web/src/pages/SettingsPage.tsx` — stub (if not exists)
- `web/src/router.tsx` — ensure `/settings` route exists
- CSS files — dropdown styles, chat fullscreen styles
