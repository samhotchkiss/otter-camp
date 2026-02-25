//go:build integration

package eventbus

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestEventBus_Publish_OrderedBySeq(t *testing.T) {
	pool := testdb.New(t)
	org := createEventBusOrg079(t, pool, "eventbus-ordered")
	bus := New(pool, nil, Config{PollInterval: 25 * time.Millisecond})

	for i := 0; i < 5; i++ {
		if err := bus.Publish(context.Background(), nil, DomainEvent{
			OrganizationID: org.ID,
			EventType:      "event.ordered",
			ActorType:      "system",
			Payload:        []byte(fmt.Sprintf(`{"i":%d}`, i)),
		}); err != nil {
			t.Fatalf("publish event %d: %v", i, err)
		}
	}

	rows, err := pool.Query(context.Background(), `
		SELECT seq
		FROM domain_event
		WHERE organization_id = $1
		ORDER BY seq ASC
	`, org.ID)
	if err != nil {
		t.Fatalf("query ordered events: %v", err)
	}
	defer rows.Close()

	seqs := make([]int64, 0, 5)
	for rows.Next() {
		var seq int64
		if scanErr := rows.Scan(&seq); scanErr != nil {
			t.Fatalf("scan seq: %v", scanErr)
		}
		seqs = append(seqs, seq)
	}
	if rows.Err() != nil {
		t.Fatalf("iterate seq rows: %v", rows.Err())
	}
	if len(seqs) != 5 {
		t.Fatalf("event count = %d, want 5", len(seqs))
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("non-monotonic sequence: %v", seqs)
		}
	}
}

func TestEventBus_ConsumerCursor_Tracking(t *testing.T) {
	pool := testdb.New(t)
	org := createEventBusOrg079(t, pool, "eventbus-cursor")

	for i := 0; i < 12; i++ {
		testdb.PublishEvent(t, pool, org.ID, "event.cursor", map[string]any{"n": i})
	}

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO consumer_cursor (consumer_name, organization_id, last_seq)
		VALUES ('consumer-A', $1, 10)
		ON CONFLICT (consumer_name, organization_id) WHERE organization_id IS NOT NULL
		DO UPDATE SET last_seq = EXCLUDED.last_seq
	`, org.ID); err != nil {
		t.Fatalf("upsert consumer-A cursor: %v", err)
	}

	var countAfter10 int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND seq > 10
	`, org.ID).Scan(&countAfter10); err != nil {
		t.Fatalf("count events after seq=10: %v", err)
	}
	if countAfter10 != 2 {
		t.Fatalf("events after seq=10 = %d, want 2", countAfter10)
	}

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO consumer_cursor (consumer_name, organization_id, last_seq)
		VALUES ('consumer-B', $1, 4)
		ON CONFLICT (consumer_name, organization_id) WHERE organization_id IS NOT NULL
		DO UPDATE SET last_seq = EXCLUDED.last_seq
	`, org.ID); err != nil {
		t.Fatalf("upsert consumer-B cursor: %v", err)
	}

	var (
		cursorA int64
		cursorB int64
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT last_seq
		FROM consumer_cursor
		WHERE consumer_name = 'consumer-A' AND organization_id = $1
	`, org.ID).Scan(&cursorA); err != nil {
		t.Fatalf("select consumer-A cursor: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT last_seq
		FROM consumer_cursor
		WHERE consumer_name = 'consumer-B' AND organization_id = $1
	`, org.ID).Scan(&cursorB); err != nil {
		t.Fatalf("select consumer-B cursor: %v", err)
	}
	if cursorA != 10 || cursorB != 4 {
		t.Fatalf("cursor independence violated: consumer-A=%d consumer-B=%d", cursorA, cursorB)
	}
}

func TestEventBus_ListenNotify_Wakeup(t *testing.T) {
	pool := testdb.New(t)
	org := createEventBusOrg079(t, pool, "eventbus-listen-notify")
	bus := New(pool, nil, Config{PollInterval: 5 * time.Second})

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire listener connection: %v", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(context.Background(), `LISTEN `+domainEventsChannel); err != nil {
		t.Fatalf("LISTEN %s failed: %v", domainEventsChannel, err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = bus.Publish(context.Background(), nil, DomainEvent{
			OrganizationID: org.ID,
			EventType:      "event.notify",
			ActorType:      "system",
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
		t.Fatalf("wait for notify timed out/failed: %v", err)
	}
}

func TestEventBus_MultipleConsumers_Independent(t *testing.T) {
	pool := testdb.New(t)
	org := createEventBusOrg079(t, pool, "eventbus-multi-consumers")

	for i := 0; i < 10; i++ {
		testdb.PublishEvent(t, pool, org.ID, "event.multi", map[string]any{"n": i})
	}

	consumers := []string{"consumer-1", "consumer-2", "consumer-3"}
	for _, name := range consumers {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO consumer_cursor (consumer_name, organization_id, last_seq)
			VALUES ($1, $2, 0)
			ON CONFLICT (consumer_name, organization_id) WHERE organization_id IS NOT NULL
			DO UPDATE SET last_seq = EXCLUDED.last_seq
		`, name, org.ID); err != nil {
			t.Fatalf("upsert cursor %s: %v", name, err)
		}
	}

	for _, name := range consumers {
		count := countEventsAfterCursor079(t, pool, org.ID, name)
		if count != 10 {
			t.Fatalf("%s initial unread count = %d, want 10", name, count)
		}
	}

	setCursor079(t, pool, org.ID, "consumer-1", 10)
	setCursor079(t, pool, org.ID, "consumer-2", 6)
	setCursor079(t, pool, org.ID, "consumer-3", 3)

	if got := countEventsAfterCursor079(t, pool, org.ID, "consumer-1"); got != 0 {
		t.Fatalf("consumer-1 unread count = %d, want 0", got)
	}
	if got := countEventsAfterCursor079(t, pool, org.ID, "consumer-2"); got != 4 {
		t.Fatalf("consumer-2 unread count = %d, want 4", got)
	}
	if got := countEventsAfterCursor079(t, pool, org.ID, "consumer-3"); got != 7 {
		t.Fatalf("consumer-3 unread count = %d, want 7", got)
	}
}

func TestEventBus_GapDetection(t *testing.T) {
	pool := testdb.New(t)
	org := createEventBusOrg079(t, pool, "eventbus-gap")

	var lastSeq int64
	for i := 0; i < 5; i++ {
		lastSeq = testdb.PublishEvent(t, pool, org.ID, "event.gap", map[string]any{"n": i})
	}

	if _, err := pool.Exec(context.Background(), `
		SELECT setval(pg_get_serial_sequence('domain_event', 'seq'), $1, true)
	`, lastSeq+2); err != nil {
		t.Fatalf("set sequence gap: %v", err)
	}

	for i := 0; i < 3; i++ {
		testdb.PublishEvent(t, pool, org.ID, "event.gap", map[string]any{"post_gap": i})
	}

	rows, err := pool.Query(context.Background(), `
		SELECT seq
		FROM domain_event
		WHERE organization_id = $1
		  AND seq > $2
		ORDER BY seq ASC
	`, org.ID, lastSeq)
	if err != nil {
		t.Fatalf("query events after cursor: %v", err)
	}
	defer rows.Close()

	seqs := make([]int64, 0, 3)
	for rows.Next() {
		var seq int64
		if scanErr := rows.Scan(&seq); scanErr != nil {
			t.Fatalf("scan gap seq: %v", scanErr)
		}
		seqs = append(seqs, seq)
	}
	if len(seqs) != 3 {
		t.Fatalf("post-gap event count = %d, want 3", len(seqs))
	}
	if seqs[0] != lastSeq+3 {
		t.Fatalf("gap handling expected first seq %d, got %d", lastSeq+3, seqs[0])
	}
}

func TestEventBus_ActorType_Supervisor(t *testing.T) {
	pool := testdb.New(t)
	org := createEventBusOrg079(t, pool, "eventbus-supervisor")
	bus := New(pool, nil, Config{PollInterval: 25 * time.Millisecond})

	var (
		mu       sync.Mutex
		received []DomainEvent
		done     = make(chan struct{})
	)

	sub := bus.Subscribe("eventbus.supervisor.consumer", &org.ID, func(_ context.Context, event DomainEvent) error {
		mu.Lock()
		received = append(received, event)
		if event.ActorType == "supervisor" {
			select {
			case <-done:
			default:
				close(done)
			}
		}
		mu.Unlock()
		return nil
	})
	defer bus.Unsubscribe(sub)

	if err := bus.Publish(context.Background(), nil, DomainEvent{
		OrganizationID: org.ID,
		EventType:      "run.recovered",
		ActorType:      "supervisor",
	}); err != nil {
		t.Fatalf("publish supervisor event: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for supervisor event delivery")
	}

	var actorType string
	if err := pool.QueryRow(context.Background(), `
		SELECT actor_type
		FROM domain_event
		WHERE organization_id = $1
		ORDER BY seq DESC
		LIMIT 1
	`, org.ID).Scan(&actorType); err != nil {
		t.Fatalf("query stored actor_type: %v", err)
	}
	if actorType != "supervisor" {
		t.Fatalf("stored actor_type = %q, want supervisor", actorType)
	}
}

func createEventBusOrg079(t *testing.T, pool *pgxpool.Pool, slug string) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(context.Background(), repo.Organization{
		Slug:        slug,
		DisplayName: slug,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}

func setCursor079(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, consumer string, seq int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE consumer_cursor
		SET last_seq = $3
		WHERE consumer_name = $1
		  AND organization_id = $2
	`, consumer, orgID, seq); err != nil {
		t.Fatalf("update cursor for %s: %v", consumer, err)
	}
}

func countEventsAfterCursor079(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, consumer string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM domain_event de
		JOIN consumer_cursor cc
		  ON cc.consumer_name = $2
		 AND cc.organization_id = $1
		WHERE de.organization_id = $1
		  AND de.seq > cc.last_seq
	`, orgID, consumer).Scan(&count); err != nil {
		t.Fatalf("count events after cursor for %s: %v", consumer, err)
	}
	return count
}
