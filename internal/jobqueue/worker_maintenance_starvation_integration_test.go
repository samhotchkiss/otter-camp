//go:build integration

package jobqueue

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestJobWorkerReservesExecutionSlotWhenMaintenanceAlreadyInFlight(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            4,
		PollInterval:         20 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	maintenanceRelease := make(chan struct{})
	maintenanceStarted := make(chan struct{}, 8)
	agentStarted := make(chan struct{}, 1)

	worker.Register(rollupUpdateJobType, func(context.Context, Job) error {
		select {
		case maintenanceStarted <- struct{}{}:
		default:
		}
		<-maintenanceRelease
		return nil
	})
	worker.Register(agentTurnJobType, func(context.Context, Job) error {
		select {
		case agentStarted <- struct{}{}:
		default:
		}
		return nil
	})

	orgID := uuid.New()
	for i := 0; i < 6; i++ {
		if _, err := worker.Enqueue(context.Background(), nil, rollupUpdateJobType, 60, map[string]any{
			"org_id":      orgID,
			"rollup_date": fmt.Sprintf("2026-03-%02d", i+1),
		}, nil); err != nil {
			t.Fatalf("enqueue maintenance %d failed: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	maintenanceClosed := false
	defer func() {
		cancel()
		if !maintenanceClosed {
			close(maintenanceRelease)
		}
		_ = worker.Stop()
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-maintenanceStarted:
		case <-time.After(2 * time.Second):
			t.Fatalf("maintenance job %d did not start", i+1)
		}
	}

	select {
	case <-maintenanceStarted:
		t.Fatal("maintenance filled all worker slots before agent work arrived")
	default:
	}

	org, err := repo.NewOrgRepo(pool).Create(context.Background(), repo.Organization{
		Slug:        "maintenance-slot-reservation",
		DisplayName: "Maintenance Slot Reservation",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(context.Background(), repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(context.Background(), repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue task work",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}

	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, map[string]any{
		"session_id":  session.ID,
		"message_id":  message.ID,
		"retry_count": 0,
	}, nil); err != nil {
		t.Fatalf("enqueue agent_turn failed: %v", err)
	}

	select {
	case <-agentStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("agent_turn did not start while maintenance jobs were already in flight")
	}

	close(maintenanceRelease)
	maintenanceClosed = true
}
