# Test 003: Focus cycles — Tab/Shift-Tab, Alt-1/2/3

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Focus cycles: Tab/Shift-Tab or Alt-1/2/3 jumps directly
**Tested:** 2026-02-26 16:15
**Result:** PASS

## How I Tested

1. Started with focus on main (M/main in status bar)
2. Pressed Tab — verified focus moved to chat (M/chat)
3. Pressed Tab again — verified focus moved to sidebar (M/sidebar)
4. Pressed Tab again — verified focus returned to main (M/main)
5. Pressed Shift-Tab (BTab in tmux) — verified reverse cycle to sidebar
6. Pressed Alt-2 — verified direct jump to main
7. Pressed Alt-3 — verified direct jump to chat
8. Pressed Alt-1 — verified direct jump to sidebar

## TUI Screen State

Status bar shows focus progression:
- `M/main · Focus: main`
- `M/chat · Focus: chat`
- `M/sidebar · Focus: sidebar`
- (cycle repeats)

Alt keys work:
- Alt-1 → `M/sidebar · Focus: sidebar`
- Alt-2 → `M/main · Focus: main`
- Alt-3 → `M/chat · Focus: chat`

## Observed Behavior

Tab cycles main→chat→sidebar→main at M size. Shift-Tab reverses. Alt-1/2/3 jump directly. Status bar accurately shows current focus panel. Panel borders change color (blue = focused, gray = unfocused) in addition to status bar update.

## Expected Behavior

Exactly as observed.

## Notes

The focused panel has a blue border (colFocus color). The status bar appends `· Focus: panel` when not on main — useful feedback. Cycle order at M is sidebar→main→chat which matches focusOrderForLayout.

## Issue Filed

None.
