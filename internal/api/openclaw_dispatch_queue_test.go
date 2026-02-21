package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

	ackResult, err := ackOpenClawDispatchJob(
		context.Background(),
		db,
		jobs[0].ID,
		jobs[0].ClaimToken,
		false,
		"temporary failure",
	)
	require.NoError(t, err)
	require.True(t, ackResult.Acknowledged)

	jobs, err = claimOpenClawDispatchJobs(context.Background(), db, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 0, "failed jobs should be backoff-delayed")

	_, err = db.Exec(`UPDATE openclaw_dispatch_queue SET available_at = NOW() - INTERVAL '1 second'`)
	require.NoError(t, err)

	jobs, err = claimOpenClawDispatchJobs(context.Background(), db, 10)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, 2, jobs[0].Attempts)

	ackResult, err = ackOpenClawDispatchJob(
		context.Background(),
		db,
		jobs[0].ID,
		jobs[0].ClaimToken,
		true,
		"",
	)
	require.NoError(t, err)
	require.True(t, ackResult.Acknowledged)

	jobs, err = claimOpenClawDispatchJobs(context.Background(), db, 10)
	require.NoError(t, err)
	require.Empty(t, jobs)
}

func TestOpenClawDispatchQueueAckFailureBackoffSchedule(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dispatch-queue-backoff-org")

	queued, err := enqueueOpenClawDispatchEvent(
		context.Background(),
		db,
		orgID,
		"dm.message",
		"dm.message:msg-backoff",
		map[string]any{
			"type": "dm.message",
			"data": map[string]any{"message_id": "msg-backoff"},
		},
	)
	require.NoError(t, err)
	require.True(t, queued)

	expectedRetrySeconds := []int{5, 15, 30, 60, 60, 60}
	for idx, expected := range expectedRetrySeconds {
		jobs, claimErr := claimOpenClawDispatchJobs(context.Background(), db, 1)
		require.NoError(t, claimErr)
		require.Len(t, jobs, 1, "expected one claimed job at attempt index %d", idx)

		ackResult, ackErr := ackOpenClawDispatchJob(
			context.Background(),
			db,
			jobs[0].ID,
			jobs[0].ClaimToken,
			false,
			"temporary failure",
		)
		require.NoError(t, ackErr)
		require.True(t, ackResult.Acknowledged)

		var status string
		var availableAt time.Time
		require.NoError(t, db.QueryRow(
			`SELECT status, available_at FROM openclaw_dispatch_queue WHERE id = $1`,
			jobs[0].ID,
		).Scan(&status, &availableAt))
		require.Equal(t, "pending", status)

		retryDelay := time.Until(availableAt)
		minDelay := time.Duration(expected-1) * time.Second
		maxDelay := time.Duration(expected+4) * time.Second
		require.GreaterOrEqual(t, retryDelay, minDelay)
		require.LessOrEqual(t, retryDelay, maxDelay)

		if idx < len(expectedRetrySeconds)-1 {
			_, err = db.Exec(`UPDATE openclaw_dispatch_queue SET available_at = NOW() - INTERVAL '1 second' WHERE id = $1`, jobs[0].ID)
			require.NoError(t, err)
		}
	}
}

func TestOpenClawDispatchQueueAckFailureMarksFailedAtMaxAttempts(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dispatch-queue-max-retry-org")

	queued, err := enqueueOpenClawDispatchEvent(
		context.Background(),
		db,
		orgID,
		"dm.message",
		"dm.message:msg-max",
		map[string]any{
			"type": "dm.message",
			"data": map[string]any{"message_id": "msg-max"},
		},
	)
	require.NoError(t, err)
	require.True(t, queued)

	for attempt := 1; attempt <= 20; attempt++ {
		jobs, claimErr := claimOpenClawDispatchJobs(context.Background(), db, 1)
		require.NoError(t, claimErr)
		require.Len(t, jobs, 1, "expected one claimed job at attempt %d", attempt)

		ackResult, ackErr := ackOpenClawDispatchJob(
			context.Background(),
			db,
			jobs[0].ID,
			jobs[0].ClaimToken,
			false,
			"bridge unavailable",
		)
		require.NoError(t, ackErr)
		require.True(t, ackResult.Acknowledged)

		var status string
		var attempts int
		require.NoError(t, db.QueryRow(
			`SELECT status, attempts FROM openclaw_dispatch_queue WHERE id = $1`,
			jobs[0].ID,
		).Scan(&status, &attempts))
		require.Equal(t, attempt, attempts)
		if attempt < 20 {
			require.Equal(t, "pending", status)
			_, err = db.Exec(`UPDATE openclaw_dispatch_queue SET available_at = NOW() - INTERVAL '1 second' WHERE id = $1`, jobs[0].ID)
			require.NoError(t, err)
		} else {
			require.Equal(t, "failed", status)
		}
	}

	jobs, err := claimOpenClawDispatchJobs(context.Background(), db, 1)
	require.NoError(t, err)
	require.Empty(t, jobs, "failed dispatch jobs must not be claimable")
}
