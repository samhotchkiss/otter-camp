# Test 019: Keyboard Navigation

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- j/k: navigate lists up/down
- h/l: collapse/expand or navigate panes
- Enter: select / confirm / open
- `:` command palette (Superhuman-style fuzzy search across sessions, projects, tasks)
- `/`: in-pane search
- `?`: help / keybinding reference
- `[`/`]`: navigate chat scope levels
- Esc: cancel / go back / cancel agent turn
- Tab/Shift-Tab: cycle panel focus
- Alt-1/2/3: jump directly to sidebar/main/chat
- g/G: jump to top/bottom of list or viewport
- Vim-style keybindings throughout as default
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Tested each keybinding via `tmux send-keys`, captured TUI state, and examined code in model.go/workspace.go for handling.

## Findings Per Item

**j/k list navigation:**
- Dashboard: `j` moves cursor through task rows ✓
- Sidebar: `j`/`k` navigate session list ✓
- Inbox: `j`/`k` navigate inbox items ✓
- Result: PASS

**h/l collapse/expand / navigate panes:**
- Sidebar: `l` expands project node (shows children); `h` collapses ✓
- Main panel: `l` not mapped to pane navigation at main/chat level
- Between panes: Tab/Shift-Tab is the navigation; h/l do NOT switch panes
- Help text correctly says "h/l collapse/expand sidebar" (not "navigate panes")
- Result: PASS (matches help doc; spec wording is aspirational)

**Enter: select/confirm/open:**
- Dashboard: Enter opens task detail view ✓
- Task detail: Enter·open hint present — opens task (navigates to ViewTask)
- Inbox: Enter not mapped (uses `o` for open) — PARTIAL
- Sidebar: Enter selects session and switches chat pane ✓
- Result: PASS (most contexts work)

**`:` command palette:**
- Opens command bar with amber-colored input box ✓
- Available commands: frank, dashboard, project, task, inbox, focus, send, cancel-turn, quit ✓
- NOT fuzzy search across sessions/projects/tasks — just predefined command list
- No autocomplete/fuzzy matching across content
- Result: PARTIAL (command bar works; not a full fuzzy search palette as specced)

**`/` in-pane search:**
- No `/` binding found in code
- Result: FAIL — not implemented

**`?` help / keybinding reference:**
- Opens HELP view in main panel ✓
- Shows Navigation, Chat, Main Panel, Commands sections ✓
- Press `?` or Esc to close ✓
- Result: PASS

**`[`/`]` navigate chat scope levels:**
- `[` goes to narrower scope, `]` goes to broader scope
- But scope display only shows raw ID in header — see Issue #165
- Cycling works at code level; display is confusing
- Result: PARTIAL (functionality exists; UX broken per Issue #165)

**Esc: cancel / go back:**
- Cancel active agent turn ✓
- Close help view ✓
- Return to dashboard from task detail ✓
- Result: PASS

**Tab/Shift-Tab cycle panel focus:**
- Tab cycles forward through visible panels ✓
- Shift-Tab cycles backward ✓
- Result: PASS

**Alt-1/2/3 / 1/2/3 jump to panels:**
- `1` jumps to sidebar ✓, `2` to main ✓, `3` to chat ✓
- Note: implemented as plain `1/2/3` keys (not `Alt-1/2/3`)
- Works only when NOT in chat input with focus — when chat is focused, `1`/`2`/`3` type characters
- Actually code: focus panel switching is in `handleGlobalKey()`, called before chat runes ✓
- Result: PASS

**g/G jump to top/bottom:**
- g/G are not explicitly handled in model.go global key handler
- No evidence of top/bottom jumping in chat or list context
- Result: FAIL — not implemented

**Vim-style keybindings throughout:**
- j/k: lists ✓
- h/l: collapse/expand ✓
- g/G: NOT implemented ✗
- Remaining vim keys (w, b, e, 0, $, etc.): NOT implemented
- Result: PARTIAL (basic j/k/h/l yes; full vim set no)

## Issues Filed

- Issue #170 — `/` in-pane search not implemented
- Issue #171 — g/G jump to top/bottom not implemented; `:` command palette is not fuzzy search
