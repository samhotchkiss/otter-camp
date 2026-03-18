//go:build integration

package eventbus

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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

	waitForConsumerCursor(t, pool, "eventbus.order.consumer", org.ID, lastSeq, 2*time.Second)
}

func TestEventBusNewConsumerStartsAtCurrentSeq(t *testing.T) {
	pool := testdb.New(t)
	org := createOrgForEventBus(t, pool, "eventbus-start-current-org")
	bus := New(pool, nil, Config{PollInterval: 200 * time.Millisecond})

	if err := bus.Publish(context.Background(), nil, DomainEvent{
		OrganizationID: org.ID,
		EventType:      "event.preexisting",
		ActorType:      "system",
		Payload:        []byte(`{"phase":"before"}`),
	}); err != nil {
		t.Fatalf("publish preexisting event: %v", err)
	}

	received := make(chan DomainEvent, 2)
	sub := bus.Subscribe("eventbus.start-current.consumer", &org.ID, func(_ context.Context, event DomainEvent) error {
		select {
		case received <- event:
		default:
		}
		return nil
	})
	defer bus.Unsubscribe(sub)

	select {
	case event := <-received:
		t.Fatalf("unexpected replayed event on new consumer start: seq=%d type=%s", event.Seq, event.EventType)
	case <-time.After(300 * time.Millisecond):
	}

	if err := bus.Publish(context.Background(), nil, DomainEvent{
		OrganizationID: org.ID,
		EventType:      "event.after-subscribe",
		ActorType:      "system",
		Payload:        []byte(`{"phase":"after"}`),
	}); err != nil {
		t.Fatalf("publish post-subscribe event: %v", err)
	}

	var event DomainEvent
	select {
	case event = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for post-subscribe event")
	}

	if event.EventType != "event.after-subscribe" {
		t.Fatalf("event type = %q, want post-subscribe event", event.EventType)
	}
	waitForConsumerCursor(t, pool, "eventbus.start-current.consumer", org.ID, event.Seq, 2*time.Second)
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

	waitForConsumerCursor(t, pool, "eventbus.redelivery.consumer", org.ID, event.Seq, 2*time.Second)
}

func waitForConsumerCursor(t *testing.T, pool *pgxpool.Pool, consumerName string, orgID uuid.UUID, wantSeq int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		var cursorSeq int64
		err := pool.QueryRow(context.Background(), `
			SELECT last_seq
			FROM consumer_cursor
			WHERE consumer_name = $1
			  AND organization_id = $2
		`, consumerName, orgID).Scan(&cursorSeq)
		if err == nil && cursorSeq == wantSeq {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("query cursor failed before timeout: %v", err)
			}
			t.Fatalf("cursor last_seq = %d, want %d", cursorSeq, wantSeq)
		}
		time.Sleep(25 * time.Millisecond)
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
