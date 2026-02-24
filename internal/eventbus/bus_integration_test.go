//go:build integration

package eventbus

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestEventBusPublishSubscribeAndCursorAdvance(t *testing.T) {
	pool := testdb.New(t)
	org := createOrgForEventBus(t, pool, "eventbus-order-org")

	bus := New(pool, nil, Config{PollInterval: 200 * time.Millisecond})

	var (
		mu     sync.Mutex
		recv   []DomainEvent
		doneCh = make(chan struct{})
	)

	sub := bus.Subscribe("eventbus.order.consumer", &org.ID, func(_ context.Context, event DomainEvent) error {
		mu.Lock()
		recv = append(recv, event)
		if len(recv) == 3 {
			select {
			case <-doneCh:
			default:
				close(doneCh)
			}
		}
		mu.Unlock()
		return nil
	})
	defer bus.Unsubscribe(sub)

	for i := 0; i < 3; i++ {
		err := bus.Publish(context.Background(), nil, DomainEvent{
			OrganizationID: org.ID,
			EventType:      "task.completed",
			ActorType:      "system",
			Payload:        []byte(`{"index":1}`),
		})
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}
	}

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for consumer events")
	}

	mu.Lock()
	if len(recv) != 3 {
		t.Fatalf("received %d events, want 3", len(recv))
	}
	if recv[0].Seq >= recv[1].Seq || recv[1].Seq >= recv[2].Seq {
		t.Fatalf("event sequence is not strictly increasing: %d, %d, %d", recv[0].Seq, recv[1].Seq, recv[2].Seq)
	}
	lastSeq := recv[2].Seq
	mu.Unlock()

	var cursorSeq int64
	err := pool.QueryRow(context.Background(), `
		SELECT last_seq
		FROM consumer_cursor
		WHERE consumer_name = $1
		  AND organization_id = $2
	`, "eventbus.order.consumer", org.ID).Scan(&cursorSeq)
	if err != nil {
		t.Fatalf("query cursor failed: %v", err)
	}
	if cursorSeq != lastSeq {
		t.Fatalf("cursor last_seq = %d, want %d", cursorSeq, lastSeq)
	}
}

func TestEventBusAtLeastOnceRedeliveryAfterRestart(t *testing.T) {
	pool := testdb.New(t)
	org := createOrgForEventBus(t, pool, "eventbus-redelivery-org")

	busA := New(pool, nil, Config{PollInterval: 30 * time.Second})
	firstAttempt := make(chan struct{}, 1)

	subA := busA.Subscribe("eventbus.redelivery.consumer", &org.ID, func(context.Context, DomainEvent) error {
		select {
		case firstAttempt <- struct{}{}:
		default:
		}
		return errors.New("fail first")
	})

	if err := busA.Publish(context.Background(), nil, DomainEvent{
		OrganizationID: org.ID,
		EventType:      "agent.activated",
		ActorType:      "system",
	}); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case <-firstAttempt:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first failed delivery")
	}

	busA.Unsubscribe(subA)

	busB := New(pool, nil, Config{PollInterval: 200 * time.Millisecond})
	replayed := make(chan DomainEvent, 1)
	subB := busB.Subscribe("eventbus.redelivery.consumer", &org.ID, func(_ context.Context, event DomainEvent) error {
		select {
		case replayed <- event:
		default:
		}
		return nil
	})
	defer busB.Unsubscribe(subB)

	var event DomainEvent
	select {
	case event = <-replayed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for replayed event")
	}

	var cursorSeq int64
	err := pool.QueryRow(context.Background(), `
		SELECT last_seq
		FROM consumer_cursor
		WHERE consumer_name = $1
		  AND organization_id = $2
	`, "eventbus.redelivery.consumer", org.ID).Scan(&cursorSeq)
	if err != nil {
		t.Fatalf("query cursor failed: %v", err)
	}
	if cursorSeq != event.Seq {
		t.Fatalf("cursor last_seq = %d, want %d", cursorSeq, event.Seq)
	}
}

func createOrgForEventBus(t *testing.T, pool *pgxpool.Pool, slug string) repo.Organization {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.Create(context.Background(), repo.Organization{Slug: slug, DisplayName: slug})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}
	return org
}
