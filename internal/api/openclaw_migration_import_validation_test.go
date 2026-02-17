package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenClawMigrationImportEndpointsRejectOversizedPayload(t *testing.T) {
	oversizedAgentBody := `{"identities":[{"id":"main","identity":"` +
		strings.Repeat("x", int(maxOpenClawMigrationImportBodyBytes)) +
		`"}]}`
	agentReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/agents", strings.NewReader(oversizedAgentBody))
	agentRec := httptest.NewRecorder()
	var agentPayload openClawMigrationImportAgentsRequest
	status, err := decodeOpenClawMigrationImportRequest(agentRec, agentReq, &agentPayload)
	require.Error(t, err)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)

	oversizedHistoryBody := `{"user_id":"00000000-0000-0000-0000-000000000001","batch":{"id":"b","index":1,"total":1},"events":[{"agent_slug":"main","role":"user","body":"` +
		strings.Repeat("y", int(maxOpenClawMigrationImportBodyBytes)) +
		`","created_at":"2026-01-01T10:00:00Z"}]}`
	historyReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/history/batch", strings.NewReader(oversizedHistoryBody))
	historyRec := httptest.NewRecorder()
	var historyPayload openClawMigrationImportHistoryBatchRequest
	status, err = decodeOpenClawMigrationImportRequest(historyRec, historyReq, &historyPayload)
	require.Error(t, err)
	require.Equal(t, http.StatusRequestEntityTooLarge, status)
}

func TestOpenClawMigrationImportEndpointsValidateRequiredFields(t *testing.T) {
	t.Run("agents request requires identities and ids", func(t *testing.T) {
		require.ErrorContains(
			t,
			validateOpenClawMigrationImportAgentsRequest(openClawMigrationImportAgentsRequest{}),
			"identities must include at least one item",
		)
		require.ErrorContains(
			t,
			validateOpenClawMigrationImportAgentsRequest(openClawMigrationImportAgentsRequest{
				Identities: []openClawMigrationImportAgentIdentityPayload{{Name: "No ID"}},
			}),
			"identities[0].id is required",
		)
	})

	t.Run("history request validates user batch and events", func(t *testing.T) {
		base := openClawMigrationImportHistoryBatchRequest{
			UserID: "00000000-0000-0000-0000-000000000001",
			Batch: openClawMigrationImportBatchPayload{
				ID:    "batch-1",
				Index: 1,
				Total: 1,
			},
			Events: []openClawMigrationImportHistoryEventPayload{
				{
					AgentSlug: "main",
					Role:      "assistant",
					Body:      "hello",
					CreatedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
				},
			},
		}

		invalidUser := base
		invalidUser.UserID = "not-a-uuid"
		require.ErrorContains(t, validateOpenClawMigrationImportHistoryBatchRequest(invalidUser), "user_id must be a UUID")

		missingBatchID := base
		missingBatchID.Batch.ID = ""
		require.ErrorContains(t, validateOpenClawMigrationImportHistoryBatchRequest(missingBatchID), "batch.id is required")

		missingEvents := base
		missingEvents.Events = nil
		require.ErrorContains(t, validateOpenClawMigrationImportHistoryBatchRequest(missingEvents), "events must include at least one item")

		missingEventFields := base
		missingEventFields.Events = []openClawMigrationImportHistoryEventPayload{
			{AgentSlug: "", Role: "assistant", Body: "x", CreatedAt: time.Now().UTC()},
		}
		require.ErrorContains(t, validateOpenClawMigrationImportHistoryBatchRequest(missingEventFields), "events[0].agent_slug is required")
	})
}
