# Task 166: Markdown not rendered in chat — Glamour is dead code

Layer: L2
Effort: M
Depends on: none

## Context

The spec requires "Markdown rendered via Glamour (headings, bold, italic, lists, links, inline code)." Glamour is imported in go.mod and a `markdownToPlain(raw string, width int) string` wrapper exists in `internal/tui/chat.go`. However, this function is never called. Agent messages display raw markdown syntax (`**bold**`, `### heading`, `- list`) as literal characters.

## Problem

In `internal/tui/view.go`, `renderChatMessages()` renders message content as:
```go
content := strings.TrimSpace(msg.Content)
if content != "" {
    wrapped := wrapText(content, width)
    for _, wl := range wrapped {
        lines = append(lines, styleText.Render(wl))
    }
}
```

The content is word-wrapped but not passed through Glamour. `markdownToPlain()` in `chat.go` is dead code.

## Required Fix

Replace the plain `wrapText` call for message content with Glamour rendering:

```go
content := strings.TrimSpace(msg.Content)
if content != "" {
    rendered := markdownToPlain(content, width)
    for _, line := range strings.Split(rendered, "\n") {
        lines = append(lines, line)
    }
}
```

The `markdownToPlain` function uses `glamour.WithStandardStyle("notty")` which is compatible with non-dark/light themed terminals and preserves ANSI colors from Glamour. The `WithWordWrap(width)` handles line wrapping.

**Note:** After switching to Glamour rendering, the line-by-line approach in `renderChatMessages` needs to handle the multi-line output correctly. Glamour adds leading/trailing newlines; `strings.TrimSpace` before splitting should handle this. Test carefully.

## Acceptance Criteria

- [ ] Agent messages with `**bold**` text display as bold in terminal
- [ ] Agent messages with `### Heading` display as styled headings
- [ ] Agent messages with `- list` display as formatted bullet lists
- [ ] Inline code `` `code` `` displays with code styling
- [ ] No regression in message layout (no overflow, extra lines, or misalignment)
- [ ] Streaming still works correctly (Glamour rendering applied only to finalized content or per-chunk)

## Required Tests

- Unit test: `TestMarkdownRenderedInChatMessages` — verify **bold** and ### heading are not literal in output
- Golden: existing chat rendering tests may need updates if any exist

## Reviewer Required Changes (2026-02-26 18:45 UTC)
Reviewer: Claude claude-sonnet-4-5 (reviewer agent)

### P1
- [ ] S1011 gosimple lint violation introduced in `view.go` — new failure added by PR to already-failing CI
  - Files: `internal/tui/view.go:981`
  - Required fix: Replace `for _, line := range strings.Split(rendered, "\n") { lines = append(lines, line) }` with `lines = append(lines, strings.Split(rendered, "\n")...)` (gosimple S1011)
  - Required test: Existing `TestMarkdownRenderedInChatMessages` must continue to pass; `go vet ./internal/tui/...` must not regress

### P2
- [ ] `normalizeRenderedMarkdown` fallback is incomplete — does not handle inline code backtick markers
  - Files: `internal/tui/chat.go:containsLiteralMarkdownMarkers`, `internal/tui/chat.go:normalizeRenderedMarkdown`
  - Required fix: Add `` strings.Contains(rendered, "`") `` check to `containsLiteralMarkdownMarkers`; add backtick-stripping (strip surrounding backticks, leave text) to `normalizeRenderedMarkdown` so the fallback path satisfies AC4 (inline code) alongside AC1–AC2
  - Required test: Add a sub-case to `TestMarkdownRenderedInChatMessages` or a new test that exercises the `normalizeRenderedMarkdown` path directly with `` `code` `` input and asserts the backtick markers are gone from output
