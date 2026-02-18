package memory

import "github.com/samhotchkiss/otter-camp/internal/store"

// SplitEllieIngestionWindowForPromptBudget splits a contiguous message window into
// smaller contiguous chunks that should each fit within the OpenClaw extraction
// prompt budget (maxPromptChars) when formatted with maxMessageChars.
//
// This is used by the ingestion worker to avoid silently dropping messages when
// the prompt exceeds transport limits, and by debug tooling to render all
// selected messages across multiple OpenClaw requests.
func SplitEllieIngestionWindowForPromptBudget(
	orgID string,
	roomID string,
	window []store.EllieIngestionMessage,
	maxPromptChars int,
	maxMessageChars int,
) [][]store.EllieIngestionMessage {
	return splitEllieIngestionWindowByPromptBudget(orgID, roomID, window, maxPromptChars, maxMessageChars)
}

