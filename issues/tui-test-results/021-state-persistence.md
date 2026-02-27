# Test 021: State Persistence

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- Local state file: `~/.config/ottercamp/tui-state.json`
- Persists: last active view, active chat session, panel proportions
- Restored on next launch
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Read `~/.config/ottercamp/tui-state.json` before and after TUI session. Examined `state.go` and `cmd/ottercamp/tui.go` for save/load logic.

## State File Content

```json
{
    "last_active_view": "chat",
    "last_active_chat_session": "session-org-general",
    "panel_proportions": [0.22, 0.46, 0.32],
    "sidebar_visible": true
}
```

## Findings Per Item

**Local state file at `~/.config/ottercamp/tui-state.json`:**
- File exists at correct path ✓
- JSON format ✓
- Result: PASS

**Persists: last active view:**
- `last_active_view` stores which panel was focused (sidebar/main/chat) ✓
- Does NOT persist which sub-view within main panel was active (dashboard vs inbox etc.)
  - After navigating to `:dashboard`, `:inbox`, etc., the specific view is not persisted
  - `Model.State()` calls `viewFromPanel(focus)` — only stores panel name, not sub-view
- Result: PARTIAL (panel focus saved; specific view within panel not saved)

**Persists: active chat session:**
- `last_active_chat_session` stores session ID ✓
- Restored on next launch: `NewModelWithRuntime` uses `normalized.LastActiveChatSession` ✓
- Result: PASS

**Persists: panel proportions:**
- `panel_proportions: [0.22, 0.46, 0.32]` stored ✓
- But: layout proportions are hardcoded per size class in `computeLayout()`
  - The stored proportions are used as `weights` parameter but then overridden by fixed weights per size class
  - Custom user-resizable proportions: NOT implemented (no resize interaction)
- Result: PARTIAL (values stored but not user-adjustable; fixed layout per size class)

**Restored on next launch:**
- State loaded via `tuiapp.LoadState(statePath)` at startup ✓
- `normalizeState()` validates and defaults invalid values ✓
- Result: PASS

## Code References

- Save: `cmd/ottercamp/tui.go:157` — called on program exit
- Load: `cmd/ottercamp/tui.go:50` — called at startup
- State struct: `internal/tui/state.go:15` — `UIState`
