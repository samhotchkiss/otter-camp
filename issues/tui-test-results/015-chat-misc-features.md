# Test 015: Chat pane — system messages, spinner, auto-scroll, escape cancel, jump-to-bottom

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- System messages: de-emphasized, centered
- Interjection messages: visually distinct with "(interjected)" label
- Code blocks syntax-highlighted; copy to clipboard
- Image blocks `[Image: name.png]`; open in system viewer
- File reference blocks; Enter opens in main pane or $EDITOR
- Artifact blocks; Enter opens with system handler
- Auto-scroll follows streaming; operator can scroll up during stream
- `[jump to bottom]` indicator when scrolled up during active streaming
- Spinner/animation indicates agent working during streaming
- Escape cancels current agent turn
**Tested:** 2026-02-26 16:43
**Result:** MIXED

## How I Tested

Examined rendering code in view.go and model.go, combined with live TUI observation.

## Findings Per Item

**System messages (de-emphasized, centered):**
- Code: `case "system": roleStr = styleMuted; roleLabel = "System"` — de-emphasized ✓
- Not centered — shown with same header format as other messages. No center alignment.
- Result: PARTIAL

**Interjection messages:**
- No code found for interjection message type in renderChatMessages
- `ChatMessage.Role` has no "interjection" case
- Result: FAIL — not implemented

**Code blocks syntax-highlighted + copy:**
- No code found for code block detection or syntax highlighting
- Glamour (if enabled) would handle this, but Glamour not called — see Issue #166
- Result: FAIL — not implemented (blocked by Issue #166)

**Image blocks `[Image: name.png]`:**
- No code found for image block rendering
- Result: FAIL — not implemented

**File reference blocks:**
- No code found for file reference block rendering
- Result: FAIL — not implemented

**Artifact blocks:**
- No code found for artifact block rendering
- Result: FAIL — not implemented

**Auto-scroll (follows streaming when at bottom):**
- When `chatScrollOffset == 0`, viewport shows most recent N lines
- New delta messages append to lines; viewport re-renders from bottom → auto-follows ✓
- Result: PASS

**`[jump to bottom]` indicator when scrolled up:**
- EX-004: "↓ N more · PgDn to scroll" indicator ✓
- End key scrolls to bottom: `case tea.KeyEnd: m.chatScrollOffset = 0` ✓
- But dedicated "jump to bottom" keybinding not obvious to new users — just End key
- Result: PASS (partially — indicator is "↓ N more" not "[jump to bottom]" literal)

**Spinner during streaming:**
- Code: `spinner := styleReconnecting.Render("◌")` shows "◌ waiting for response..." ✓
- ▌ cursor in input also indicates active turn
- Result: PASS

**Escape cancels agent turn:**
- Code: `case tea.KeyEsc: if m.activeTurn { m.activeTurn = false; requestChatCancelCmd() }` ✓
- Sends cancel request to server
- Result: PASS

## Issues Filed

- Issue #166 covers markdown/code block rendering (blocks auto-scroll fix too)
- Issue #168 — Interjection messages not implemented
- Image/file/artifact blocks not implemented (Phase 3 scope likely)
