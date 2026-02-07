package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const (
	openClawDispatchQueuedWarning = "agent bridge unavailable; message was saved and queued for delivery"

	defaultOpenClawDispatchPullLimit = 50
	maxOpenClawDispatchPullLimit     = 200
	openClawDispatchClaimTTLSeconds  = 90
)

type openClawDispatchJob struct {
	ID         int64
	EventType  string
	Payload    json.RawMessage
	ClaimToken string
	Attempts   int
}

var (
	openClawDispatchQueueSchemaOnce sync.Once
	openClawDispatchQueueSchemaErr  error
)

func ensureOpenClawDispatchQueueSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database unavailable")
	}

	openClawDispatchQueueSchemaOnce.Do(func() {
		statements := []string{
			`CREATE TABLE IF NOT EXISTS openclaw_dispatch_queue (
				id BIGSERIAL PRIMARY KEY,
				org_id UUID NOT NULL,
				event_type TEXT NOT NULL,
				dedupe_key TEXT NOT NULL UNIQUE,
				payload JSONB NOT NULL,
				status TEXT NOT NULL DEFAULT 'pending'
					CHECK (status IN ('pending', 'processing', 'delivered')),
				attempts INTEGER NOT NULL DEFAULT 0,
				last_error TEXT,
				available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				claimed_at TIMESTAMPTZ,
				claim_token TEXT,
				delivered_at TIMESTAMPTZ,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
			`CREATE INDEX IF NOT EXISTS openclaw_dispatch_queue_pending_idx
				ON openclaw_dispatch_queue (status, available_at, created_at)`,
			`CREATE INDEX IF NOT EXISTS openclaw_dispatch_queue_org_idx
				ON openclaw_dispatch_queue (org_id, created_at)`,
		}
		for _, statement := range statements {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				openClawDispatchQueueSchemaErr = err
				return
			}
		}
	})

	return openClawDispatchQueueSchemaErr
}

func sanitizeOpenClawDispatchPullLimit(raw int) int {
	if raw <= 0 {
		return defaultOpenClawDispatchPullLimit
	}
	if raw > maxOpenClawDispatchPullLimit {
		return maxOpenClawDispatchPullLimit
	}
	return raw
}

func enqueueOpenClawDispatchEvent(
	ctx context.Context,
	db *sql.DB,
	orgID string,
	eventType string,
	dedupeKey string,
	event interface{},
) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("database unavailable")
	}
	if err := ensureOpenClawDispatchQueueSchema(ctx, db); err != nil {
		return false, err
	}

	trimmedOrgID := strings.TrimSpace(orgID)
	trimmedEventType := strings.TrimSpace(eventType)
	trimmedDedupeKey := strings.TrimSpace(dedupeKey)
	if trimmedOrgID == "" || trimmedEventType == "" || trimmedDedupeKey == "" {
		return false, fmt.Errorf("invalid openclaw dispatch queue input")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return false, err
	}

	result, err := db.ExecContext(
		ctx,
		`INSERT INTO openclaw_dispatch_queue (
			org_id,
			event_type,
			dedupe_key,
			payload,
			status,
			available_at,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, 'pending', NOW(), NOW(), NOW())
		ON CONFLICT (dedupe_key) DO NOTHING`,
		trimmedOrgID,
		trimmedEventType,
		trimmedDedupeKey,
		payload,
	)
	if err != nil {
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func markOpenClawDispatchDeliveredByKey(
	ctx context.Context,
	db *sql.DB,
	dedupeKey string,
) error {
	if db == nil {
		return nil
	}
	if err := ensureOpenClawDispatchQueueSchema(ctx, db); err != nil {
		return err
	}

	_, err := db.ExecContext(
		ctx,
		`UPDATE openclaw_dispatch_queue
		 SET status = 'delivered',
		     delivered_at = NOW(),
		     last_error = NULL,
		     claim_token = NULL,
		     claimed_at = NULL,
		     updated_at = NOW()
		 WHERE dedupe_key = $1`,
		strings.TrimSpace(dedupeKey),
	)
	return err
}

func claimOpenClawDispatchJobs(
	ctx context.Context,
	db *sql.DB,
	limit int,
) ([]openClawDispatchJob, error) {
	if db == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	if err := ensureOpenClawDispatchQueueSchema(ctx, db); err != nil {
		return nil, err
	}

	limit = sanitizeOpenClawDispatchPullLimit(limit)

	rows, err := db.QueryContext(
		ctx,
		`WITH candidates AS (
			SELECT id
			FROM openclaw_dispatch_queue
			WHERE status IN ('pending', 'processing')
			  AND available_at <= NOW()
			  AND (
			    status = 'pending'
			    OR claimed_at <= NOW() - ($2::INT * INTERVAL '1 second')
			  )
			ORDER BY created_at ASC, id ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE openclaw_dispatch_queue q
		SET status = 'processing',
		    attempts = q.attempts + 1,
		    claimed_at = NOW(),
		    claim_token = md5(random()::text || clock_timestamp()::text || q.id::text),
		    updated_at = NOW()
		FROM candidates c
		WHERE q.id = c.id
		RETURNING q.id, q.event_type, q.payload, q.claim_token, q.attempts`,
		limit,
		openClawDispatchClaimTTLSeconds,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]openClawDispatchJob, 0, limit)
	for rows.Next() {
		var job openClawDispatchJob
		if err := rows.Scan(&job.ID, &job.EventType, &job.Payload, &job.ClaimToken, &job.Attempts); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func ackOpenClawDispatchJob(
	ctx context.Context,
	db *sql.DB,
	id int64,
	claimToken string,
	success bool,
	lastError string,
) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("database unavailable")
	}
	if err := ensureOpenClawDispatchQueueSchema(ctx, db); err != nil {
		return false, err
	}

	claimToken = strings.TrimSpace(claimToken)
	if claimToken == "" {
		return false, fmt.Errorf("claim token required")
	}

	var (
		result sql.Result
		err    error
	)

	if success {
		result, err = db.ExecContext(
			ctx,
			`UPDATE openclaw_dispatch_queue
			 SET status = 'delivered',
			     delivered_at = NOW(),
			     last_error = NULL,
			     claim_token = NULL,
			     claimed_at = NULL,
			     updated_at = NOW()
			 WHERE id = $1
			   AND claim_token = $2
			   AND status = 'processing'`,
			id,
			claimToken,
		)
	} else {
		result, err = db.ExecContext(
			ctx,
			`UPDATE openclaw_dispatch_queue
			 SET status = 'pending',
			     last_error = NULLIF($3, ''),
			     available_at = NOW() + make_interval(
			       secs => LEAST(300, GREATEST(5, (power(2, LEAST(attempts, 8)))::int))
			     ),
			     claim_token = NULL,
			     claimed_at = NULL,
			     updated_at = NOW()
			 WHERE id = $1
			   AND claim_token = $2
			   AND status = 'processing'`,
			id,
			claimToken,
			strings.TrimSpace(lastError),
		)
	}
	if err != nil {
		return false, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
