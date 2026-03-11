package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/security"
)

const domainEventsChannel = "domain_events"

var errHandlerFailed = errors.New("event handler failed")

type EventBus interface {
	Publish(ctx context.Context, tx pgx.Tx, event DomainEvent) error
	Subscribe(consumerName string, orgID *uuid.UUID, handler EventHandler) Subscription
	Unsubscribe(sub Subscription)
}

type EventHandler func(ctx context.Context, event DomainEvent) error

type DomainEvent struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Seq            int64
	EventType      string
	ActorType      string
	ActorID        *uuid.UUID
	Payload        json.RawMessage
	CreatedAt      time.Time
}

type Subscription struct {
	id uuid.UUID
}

type Config struct {
	PollInterval         time.Duration
	ListenReconnectDelay time.Duration
	BatchSize            int
}

type Bus struct {
	pool                 *pgxpool.Pool
	logger               *slog.Logger
	pollInterval         time.Duration
	listenReconnectDelay time.Duration
	batchSize            int

	mu            sync.Mutex
	subscriptions map[uuid.UUID]*subscriptionControl
	scrubber      *security.SecretScrubber
}

type subscriptionControl struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type queryExec interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func New(pool *pgxpool.Pool, logger *slog.Logger, cfg Config) *Bus {
	if logger == nil {
		logger = slog.Default()
	}

	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.ListenReconnectDelay <= 0 {
		cfg.ListenReconnectDelay = time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}

	return &Bus{
		pool:                 pool,
		logger:               logger,
		pollInterval:         cfg.PollInterval,
		listenReconnectDelay: cfg.ListenReconnectDelay,
		batchSize:            cfg.BatchSize,
		subscriptions:        make(map[uuid.UUID]*subscriptionControl),
		scrubber:             security.NewSecretScrubber(),
	}
}

func (b *Bus) Publish(ctx context.Context, tx pgx.Tx, event DomainEvent) error {
	if b == nil || b.pool == nil {
		return fmt.Errorf("event bus is not configured")
	}

	if tx != nil {
		_, err := b.publishWithExecutor(ctx, tx, event)
		return err
	}

	started, err := b.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin publish transaction: %w", err)
	}
	defer func() {
		_ = started.Rollback(ctx)
	}()

	if _, err := b.publishWithExecutor(ctx, started, event); err != nil {
		return err
	}

	if err := started.Commit(ctx); err != nil {
		return fmt.Errorf("commit publish transaction: %w", err)
	}

	return nil
}

func (b *Bus) Subscribe(consumerName string, orgID *uuid.UUID, handler EventHandler) Subscription {
	id := Subscription{id: uuid.New()}
	name := strings.TrimSpace(consumerName)
	if name == "" || handler == nil {
		b.logger.Error("invalid subscription", "consumer_name", consumerName)
		return id
	}

	ctx, cancel := context.WithCancel(context.Background())
	control := &subscriptionControl{cancel: cancel, done: make(chan struct{})}

	b.mu.Lock()
	b.subscriptions[id.id] = control
	b.mu.Unlock()

	go b.runConsumer(ctx, name, orgID, handler, control.done)
	return id
}

func (b *Bus) Unsubscribe(sub Subscription) {
	b.mu.Lock()
	control, ok := b.subscriptions[sub.id]
	if ok {
		delete(b.subscriptions, sub.id)
	}
	b.mu.Unlock()

	if !ok {
		return
	}

	control.cancel()
	<-control.done
}

func (b *Bus) publishWithExecutor(ctx context.Context, executor queryExec, event DomainEvent) (int64, error) {
	if strings.TrimSpace(event.EventType) == "" {
		return 0, fmt.Errorf("event_type is required")
	}
	if event.OrganizationID == uuid.Nil {
		return 0, fmt.Errorf("organization_id is required")
	}
	if strings.TrimSpace(event.ActorType) == "" {
		return 0, fmt.Errorf("actor_type is required")
	}

	payload := event.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if b.scrubber != nil {
		payload = b.scrubber.ScrubJSON(payload)
	}
	if !json.Valid(payload) {
		return 0, fmt.Errorf("invalid event payload json")
	}

	var seq int64
	err := executor.QueryRow(ctx, `
		INSERT INTO domain_event (organization_id, event_type, actor_type, actor_id, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		RETURNING seq
	`, event.OrganizationID, event.EventType, event.ActorType, event.ActorID, payload).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("insert domain_event: %w", err)
	}

	if _, err := executor.Exec(ctx, `SELECT pg_notify($1, $2)`, domainEventsChannel, fmt.Sprintf("%d", seq)); err != nil {
		return 0, fmt.Errorf("notify domain event: %w", err)
	}

	return seq, nil
}

func (b *Bus) runConsumer(ctx context.Context, consumerName string, orgID *uuid.UUID, handler EventHandler, done chan<- struct{}) {
	defer close(done)

	lastSeq, err := b.ensureCursor(ctx, consumerName, orgID)
	if err != nil {
		b.logger.Error("failed to initialize consumer cursor", "consumer_name", consumerName, "error", err)
		return
	}
	b.logger.Info("consumer started", "consumer_name", consumerName, "last_seq", lastSeq)

	notifyCh := make(chan struct{}, 1)
	go b.listenNotifications(ctx, notifyCh)
	signal(notifyCh)

	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-notifyCh:
			if err := b.processAvailable(ctx, consumerName, orgID, handler, &lastSeq); err != nil && !errors.Is(err, errHandlerFailed) {
				b.logger.Error("consumer processing error", "consumer_name", consumerName, "error", err)
			}
		case <-ticker.C:
			if err := b.processAvailable(ctx, consumerName, orgID, handler, &lastSeq); err != nil && !errors.Is(err, errHandlerFailed) {
				b.logger.Error("consumer poll processing error", "consumer_name", consumerName, "error", err)
			}
		}
	}
}

func (b *Bus) listenNotifications(ctx context.Context, notifyCh chan<- struct{}) {
	for {
		if ctx.Err() != nil {
			return
		}

		conn, err := b.pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() == nil {
				b.logger.Error("acquire listen connection failed", "channel", domainEventsChannel, "error", err)
				sleepContext(ctx, b.listenReconnectDelay)
			}
			continue
		}

		pgConn := conn.Conn()
		if _, err := pgConn.Exec(ctx, `LISTEN `+domainEventsChannel); err != nil {
			conn.Release()
			if ctx.Err() == nil {
				b.logger.Error("listen failed", "channel", domainEventsChannel, "error", err)
				sleepContext(ctx, b.listenReconnectDelay)
			}
			continue
		}
		// Drain any events that landed before LISTEN was fully attached.
		signal(notifyCh)

		for {
			_, err := pgConn.WaitForNotification(ctx)
			if err != nil {
				break
			}
			signal(notifyCh)
		}

		conn.Release()
		if ctx.Err() == nil {
			sleepContext(ctx, b.listenReconnectDelay)
		}
	}
}

func (b *Bus) processAvailable(
	ctx context.Context,
	consumerName string,
	orgID *uuid.UUID,
	handler EventHandler,
	lastSeq *int64,
) error {
	for {
		events, err := b.fetchEvents(ctx, orgID, *lastSeq, b.batchSize)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}

		for _, event := range events {
			advanced, err := b.handleEvent(ctx, consumerName, orgID, handler, event)
			if err != nil {
				if errors.Is(err, errHandlerFailed) {
					return err
				}
				return fmt.Errorf("handle event %d: %w", event.Seq, err)
			}
			if advanced {
				*lastSeq = event.Seq
			}
		}
	}
}

func (b *Bus) handleEvent(
	ctx context.Context,
	consumerName string,
	orgID *uuid.UUID,
	handler EventHandler,
	event DomainEvent,
) (bool, error) {
	panicked, err := callHandler(ctx, handler, event)
	if panicked {
		b.logger.Error(
			"consumer handler panicked",
			"consumer_name", consumerName,
			"seq", event.Seq,
			"error", err,
			"stack", string(debug.Stack()),
		)
		if advanceErr := b.advanceCursor(ctx, consumerName, orgID, event.Seq); advanceErr != nil {
			return false, advanceErr
		}
		return true, nil
	}
	advanced, advanceErr := advanceOnHandlerResult(err, func() error {
		return b.advanceCursor(ctx, consumerName, orgID, event.Seq)
	})
	if err != nil {
		b.logger.Warn("consumer handler failed", "consumer_name", consumerName, "seq", event.Seq, "error", err)
	}
	return advanced, advanceErr
}

func (b *Bus) fetchEvents(ctx context.Context, orgID *uuid.UUID, lastSeq int64, limit int) ([]DomainEvent, error) {
	rows, err := b.pool.Query(ctx, `
		SELECT id, organization_id, seq, event_type, actor_type, actor_id, payload, created_at
		FROM domain_event
		WHERE seq > $1
		  AND ($2::uuid IS NULL OR organization_id = $2)
		ORDER BY seq ASC
		LIMIT $3
	`, lastSeq, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events := make([]DomainEvent, 0)
	for rows.Next() {
		var event DomainEvent
		if err := rows.Scan(
			&event.ID,
			&event.OrganizationID,
			&event.Seq,
			&event.EventType,
			&event.ActorType,
			&event.ActorID,
			&event.Payload,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, event)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("iterate events: %w", rows.Err())
	}

	return events, nil
}

func (b *Bus) ensureCursor(ctx context.Context, consumerName string, orgID *uuid.UUID) (int64, error) {
	if orgID == nil {
		var lastSeq int64
		err := b.pool.QueryRow(ctx, `
			INSERT INTO consumer_cursor (consumer_name, organization_id, last_seq)
			VALUES ($1, NULL, 0)
			ON CONFLICT (consumer_name) WHERE organization_id IS NULL
			DO UPDATE SET consumer_name = EXCLUDED.consumer_name
			RETURNING last_seq
		`, consumerName).Scan(&lastSeq)
		if err != nil {
			return 0, fmt.Errorf("ensure global cursor: %w", err)
		}
		return lastSeq, nil
	}

	var lastSeq int64
	err := b.pool.QueryRow(ctx, `
		INSERT INTO consumer_cursor (consumer_name, organization_id, last_seq)
		VALUES ($1, $2, 0)
		ON CONFLICT (consumer_name, organization_id) WHERE organization_id IS NOT NULL
		DO UPDATE SET consumer_name = EXCLUDED.consumer_name
		RETURNING last_seq
	`, consumerName, orgID).Scan(&lastSeq)
	if err != nil {
		return 0, fmt.Errorf("ensure org cursor: %w", err)
	}
	return lastSeq, nil
}

func (b *Bus) advanceCursor(ctx context.Context, consumerName string, orgID *uuid.UUID, seq int64) error {
	var (
		ct  pgconn.CommandTag
		err error
	)

	if orgID == nil {
		ct, err = b.pool.Exec(ctx, `
			UPDATE consumer_cursor
			SET last_seq = GREATEST(last_seq, $2)
			WHERE consumer_name = $1
			  AND organization_id IS NULL
		`, consumerName, seq)
	} else {
		ct, err = b.pool.Exec(ctx, `
			UPDATE consumer_cursor
			SET last_seq = GREATEST(last_seq, $3)
			WHERE consumer_name = $1
			  AND organization_id = $2
		`, consumerName, orgID, seq)
	}
	if err != nil {
		return fmt.Errorf("update cursor: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("cursor not found for consumer %q", consumerName)
	}
	return nil
}

func callHandler(ctx context.Context, handler EventHandler, event DomainEvent) (panicked bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicked = true
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()

	err = handler(ctx, event)
	return false, err
}

func advanceOnHandlerResult(handlerErr error, advance func() error) (bool, error) {
	if handlerErr != nil {
		return false, errHandlerFailed
	}
	if err := advance(); err != nil {
		return false, err
	}
	return true, nil
}

func signal(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func sleepContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
