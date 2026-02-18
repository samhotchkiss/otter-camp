package memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

func TestEllieIngestionIsArtifactMessageHonorsSenderType(t *testing.T) {
	userWrapped := store.EllieIngestionMessage{
		SenderType: "user",
		Body:       "System: [2026-02-17 09:33:10 MST] Slack message in #engineering from Sam: ...",
	}
	require.False(t, ellieIngestionIsArtifactMessage(userWrapped))

	systemWrapped := store.EllieIngestionMessage{
		SenderType: "system",
		Body:       "System: [2026-02-17 09:33:10 MST] Bridge heartbeat ...",
	}
	require.True(t, ellieIngestionIsArtifactMessage(systemWrapped))

	toolResult := store.EllieIngestionMessage{
		SenderType: "system",
		Body:       "Tool exec result: {\"ok\":true}",
	}
	require.True(t, ellieIngestionIsArtifactMessage(toolResult))
}

func TestNormalizeEllieLLMExtractedCandidateKeepsReviewDecisions(t *testing.T) {
	room := store.EllieRoomIngestionCandidate{
		OrgID:  "ed52d6e0-a410-46f6-b3e7-0e7dd2e2eeaa",
		RoomID: "room-1",
	}
	window := []store.EllieIngestionMessage{
		{
			ID:         "m-1",
			OrgID:      room.OrgID,
			RoomID:     room.RoomID,
			SenderType: "agent",
			Body:       "Discussed options for the next step.",
			CreatedAt:  time.Date(2026, 2, 18, 20, 0, 0, 0, time.UTC),
		},
	}
	result := EllieIngestionLLMExtractionResult{
		Model:   "test-model",
		TraceID: "trace-1",
	}
	candidate := EllieIngestionLLMCandidate{
		Kind:       "fact",
		Title:      "General discussion note",
		Content:    "Discussed options and next steps.",
		Importance: 3,
		Confidence: 0.7,
	}

	normalized, ok := normalizeEllieLLMExtractedCandidate(room, window, result, candidate)
	require.True(t, ok)
	require.Equal(t, "fact", normalized.Kind)
	require.Equal(t, "General discussion note", normalized.Title)

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(normalized.Metadata, &metadata))
	require.Equal(t, "review", metadata["accept_decision"])
}

