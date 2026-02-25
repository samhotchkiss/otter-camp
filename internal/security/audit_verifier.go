package security

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditReport struct {
	Gaps         []string `json:"gaps"`
	CheckedCount int      `json:"checked_count"`
	GapCount     int      `json:"gap_count"`
}

type StateChange struct {
	RequestID  string
	OccurredAt time.Time
}

type stateChangeSource interface {
	ListStateChanges(ctx context.Context, orgID uuid.UUID, since time.Time) ([]StateChange, error)
}

type auditEventSource interface {
	HasAuditEvent(ctx context.Context, orgID uuid.UUID, requestID string, occurredAt time.Time) (bool, error)
}

type AuditVerifier struct {
	changes stateChangeSource
	audits  auditEventSource
}

func NewAuditVerifier(pool *pgxpool.Pool) *AuditVerifier {
	if pool == nil {
		return &AuditVerifier{}
	}
	return &AuditVerifier{
		changes: &sqlStateChangeSource{pool: pool},
		audits:  &sqlAuditEventSource{pool: pool},
	}
}

func NewAuditVerifierWithSources(changes stateChangeSource, audits auditEventSource) *AuditVerifier {
	return &AuditVerifier{changes: changes, audits: audits}
}

func (v *AuditVerifier) VerifyCompleteness(ctx context.Context, orgID uuid.UUID, since time.Time) AuditReport {
	report := AuditReport{Gaps: []string{}}
	if v == nil || v.changes == nil || v.audits == nil || orgID == uuid.Nil {
		return report
	}

	changes, err := v.changes.ListStateChanges(ctx, orgID, since.UTC())
	if err != nil {
		report.Gaps = append(report.Gaps, fmt.Sprintf("state-change query failed: %v", err))
		report.GapCount = len(report.Gaps)
		return report
	}
	report.CheckedCount = len(changes)

	for _, change := range changes {
		if change.RequestID == "" {
			report.Gaps = append(report.Gaps, "state change missing request_id")
			continue
		}
		hasAudit, checkErr := v.audits.HasAuditEvent(ctx, orgID, change.RequestID, change.OccurredAt.UTC())
		if checkErr != nil {
			report.Gaps = append(report.Gaps, fmt.Sprintf("audit lookup failed for request_id=%s: %v", change.RequestID, checkErr))
			continue
		}
		if !hasAudit {
			report.Gaps = append(report.Gaps, fmt.Sprintf("missing audit event for request_id=%s", change.RequestID))
		}
	}

	report.GapCount = len(report.Gaps)
	return report
}

type sqlStateChangeSource struct {
	pool *pgxpool.Pool
}

func (s *sqlStateChangeSource) ListStateChanges(ctx context.Context, orgID uuid.UUID, since time.Time) ([]StateChange, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT attributes->>'request_id' AS request_id, started_at
		FROM trace_span
		WHERE organization_id = $1
		  AND kind = 'server'
		  AND started_at >= $2
		  AND LOWER(COALESCE(attributes->>'method', '')) IN ('post', 'put', 'patch', 'delete')
		ORDER BY started_at ASC
	`, orgID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]StateChange, 0)
	for rows.Next() {
		var item StateChange
		if err := rows.Scan(&item.RequestID, &item.OccurredAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

type sqlAuditEventSource struct {
	pool *pgxpool.Pool
}

func (s *sqlAuditEventSource) HasAuditEvent(ctx context.Context, orgID uuid.UUID, requestID string, occurredAt time.Time) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM audit_event
			WHERE organization_id = $1
			  AND metadata->>'request_id' = $2
			  AND created_at >= $3 - interval '5 seconds'
			  AND created_at <= $3 + interval '5 seconds'
		)
	`, orgID, requestID, occurredAt).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}
