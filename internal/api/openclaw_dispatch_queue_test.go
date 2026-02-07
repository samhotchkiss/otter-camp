package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClawDispatchQueueLifecycle(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dispatch-queue-org")

	event := map[string]any{
		"type": "dm.message",
		"data": map[string]any{
			"message_id":  "msg-1",
			"session_key": "agent:stone:main",
			"content":     "Hello from queue",
		},
	}

	queued, err := enqueueOpenClawDispatchEvent(
		context.Background(),
		db,
		orgID,
		"dm.message",
		"dm.message:msg-1",
		event,
	)
	require.NoError(t, err)
	require.True(t, queued)

	jobs, err := claimOpenClawDispatchJobs(context.Background(), db, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "dm.message", jobs[0].EventType)
	require.NotEmpty(t, jobs[0].ClaimToken)
	require.Equal(t, 1, jobs[0].Attempts)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(jobs[0].Payload, &payload))
	require.Equal(t, "dm.message", payload["type"])

	acknowledged, err := ackOpenClawDispatchJob(
		context.Background(),
		db,
		jobs[0].ID,
		jobs[0].ClaimToken,
		false,
		"temporary failure",
	)
	require.NoError(t, err)
	require.True(t, acknowledged)

	jobs, err = claimOpenClawDispatchJobs(context.Background(), db, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 0, "failed jobs should be backoff-delayed")

	_, err = db.Exec(`UPDATE openclaw_dispatch_queue SET available_at = NOW() - INTERVAL '1 second'`)
	require.NoError(t, err)

	jobs, err = claimOpenClawDispatchJobs(context.Background(), db, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, 2, jobs[0].Attempts)

	acknowledged, err = ackOpenClawDispatchJob(
		context.Background(),
		db,
		jobs[0].ID,
		jobs[0].ClaimToken,
		true,
		"",
	)
	require.NoError(t, err)
	require.True(t, acknowledged)

	jobs, err = claimOpenClawDispatchJobs(context.Background(), db, 10)
	require.NoError(t, err)
	require.Empty(t, jobs)
}
