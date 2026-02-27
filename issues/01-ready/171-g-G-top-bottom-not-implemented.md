# Task 171: g/G jump to top/bottom not implemented; command palette is not fuzzy search

Layer: L2
Effort: S
Depends on: none

## Context

The spec requires two features:
1. `g`/`G` keys to jump to top/bottom of list or viewport in any panel
2. `:` command palette with Superhuman-style fuzzy search across sessions, projects, tasks

### g/G not implemented
In `model.go` global key handler and chat key handler, there is no `g` or `G` mapping. The help screen shows "g / G  jump to top/bottom" but pressing g/G does nothing in main/sidebar panels (in chat panel, `g`/`G` would type the character if chat is focused).

### Command palette is not fuzzy search
The `:` command bar accepts a fixed command vocabulary (frank, dashboard, project, task, inbox, focus, send, cancel-turn, quit). It does NOT:
- Fuzzy search across session names
- Search across project/task titles
- Autocomplete/suggest items from content

## Required Fix

1. **g/G**: In `handleGlobalKey()` or panel-specific key handlers:
   - `g` → scroll to top of current list/viewport
   - `G` → scroll to bottom of current list/viewport
   - For chat panel: same as Home/End keys
   - For sidebar/main panels: jump to first/last item in list

2. **Command palette fuzzy search** (larger scope): Extend `:` command bar to fuzzy-match across session labels, project names, and task titles. Show suggestions as user types.

## Acceptance Criteria

- [ ] `g` jumps to top in sidebar list, main panel list, and chat viewport
- [ ] `G` jumps to bottom in sidebar list, main panel list, and chat viewport
- [ ] Command palette shows fuzzy-matched suggestions for sessions/projects/tasks as user types

## Reviewer Required Changes (2026-02-26 19:00 UTC)
Reviewer: Claude claude-sonnet-4-5 (reviewer agent)

### P1
- [ ] S1029 gosimple lint violation in `fuzzyMatch` function — new failure added by PR to already-failing CI
  - Files: `internal/tui/view.go:1261` (approximately)
  - Required fix: Change `for _, r := range []rune(candidate)` to `for _, r := range candidate` — ranging over a string in Go already yields runes, no conversion needed. `qr := []rune(query)` for slicing is fine; only the range loop needs fixing.
  - Required test: Existing `TestCommandPaletteShowsFuzzySuggestions` must continue to pass after fix; `go vet ./internal/tui/...` must pass
