# Test 016: Message Input

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- Multi-line text area at bottom of chat pane
- Enter sends (single-line default)
- Shift-Enter / Alt-Enter inserts newline
- Up arrow (when empty) recalls previous message for editing
- Tab completion for @mentions (type `@`, agent names autocomplete)
- Draft persistence: unsent message preserved across panel focus changes
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Live TUI observation via `tmux capture-pane -p -t otter-test:0`, combined with code analysis of `model.go:handleChatControlKey()` and `handleChatRunes()`.

## Findings Per Item

**Multi-line text area:**
- Input box renders as `╭────╮ │ text │ ╰────╯` — styled box at bottom of chat pane ✓
- `chatInput` is a plain string with `\n` chars for newlines
- When multiline, the box grows to show multiple rows ✓
- Result: PASS

**Enter sends:**
- `case tea.KeyEnter: return true, m.sendOrQueueInput()` ✓
- History appended on send; `chatInput` cleared ✓
- Result: PASS

**Shift-Enter / Alt-Enter inserts newline:**
- Code: `if key.Alt { m.chatInput += "\n"; return true, nil }` — Alt-Enter works ✓
- Shift-Enter (`S-Enter` in tmux): treated same as plain Enter (sends) ✗
- Help screen says "Shift-Enter insert newline" — but actual implementation is Alt-Enter
- Bug: help text mismatch with implementation
- Result: PARTIAL (Alt-Enter works; Shift-Enter does NOT work; help text is wrong)

**Up arrow recalls previous message:**
- Code: `case tea.KeyUp: if strings.TrimSpace(m.chatInput) == "" { m.recallHistory() }` ✓
- Status: "Recalled previous message." shown in status bar ✓
- Down arrow with history index advances forward ✓
- Result: PASS

**Tab completion for @mentions:**
- `tryAutocompleteMention()` called after every rune typed ✓
- Typed `@Fra` → immediately autocompleted to `@frank ` ✓
- Status: "Mention autocomplete applied." ✓
- Autocomplete is reactive (triggers on any character), not Tab-key-driven
- Note: Tab key not used; completion is instant after each character
- Result: PASS (autocomplete works; triggered on character input not Tab key)

**Draft persistence across focus changes:**
- Switched focus: Chat (3) → Main (2) → Chat (3)
- Input content `@frank ` was preserved after focus change ✓
- Note: single `chatInput` field per model — no per-session draft storage
- Result: PASS

## Issues Filed

- Issue #168 — Help screen says "Shift-Enter" but actual newline key is Alt-Enter
