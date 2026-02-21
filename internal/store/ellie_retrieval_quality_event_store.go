package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type EllieRetrievalQualityEvent struct {
	ID              int64
	OrgID           string
	ProjectID       *string
	RoomID          *string
	Query           string
	TierUsed        int
	InjectedCount   int
	ReferencedCount int
	MissedCount     int
	NoInformation   bool
	Metadata        json.RawMessage
	CreatedAt       time.Time
}

type CreateEllieRetrievalQualityEventInput struct {
	OrgID           string
	ProjectID       *string
	RoomID          *string
	Query           string
	TierUsed        int
	InjectedCount   int
	ReferencedCount int
	MissedCount     int
	NoInformation   bool
	Metadata        json.RawMessage
}

type EllieRetrievalQualityAggregate struct {
	OrgID           string
	ProjectID       *string
	EventCount      int
	TotalInjected   int
	TotalReferenced int
	TotalMissed     int
	Precision       float64
	Recall          float64
}

type EllieRetrievalQualityEventStore struct {
	db *sql.DB
}

func NewEllieRetrievalQualityEventStore(db *sql.DB) *EllieRetrievalQualityEventStore {
	return &EllieRetrievalQualityEventStore{db: db}
}

func (s *EllieRetrievalQualityEventStore) RecordEvent(
	ctx context.Context,
	input CreateEllieRetrievalQualityEventInput,
) (*EllieRetrievalQualityEvent, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("ellie retrieval quality event store is not configured")
	}
	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	projectID, err := normalizeQualityOptionalUUID(input.ProjectID, "project_id")
	if err != nil {
		return nil, err
	}
	roomID, err := normalizeQualityOptionalUUID(input.RoomID, "room_id")
	if err != nil {
		return nil, err
	}
	tierUsed := input.TierUsed
	if tierUsed < 1 || tierUsed > 5 {
		return nil, fmt.Errorf("tier_used must be between 1 and 5")
	}
	if input.InjectedCount < 0 {
		return nil, fmt.Errorf("injected_count must be non-negative")
	}
	if input.ReferencedCount < 0 {
		return nil, fmt.Errorf("referenced_count must be non-negative")
	}
	if input.MissedCount < 0 {
		return nil, fmt.Errorf("missed_count must be non-negative")
	}
	query := strings.TrimSpace(input.Query)
	metadata := input.Metadata
	if len(strings.TrimSpace(string(metadata))) == 0 {
		metadata = json.RawMessage(`{}`)
	}

	row := s.db.QueryRowContext(
		ctx,
		`INSERT INTO ellie_retrieval_quality_events (
			org_id,
			project_id,
			room_id,
			query,
			tier_used,
			injected_count,
			referenced_count,
			missed_count,
			no_information,
			metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb
		)
		RETURNING id, org_id, project_id, room_id, query, tier_used, injected_count, referenced_count, missed_count, no_information, metadata, created_at`,
		orgID,
		projectID,
		roomID,
		query,
		tierUsed,
		input.InjectedCount,
		input.ReferencedCount,
		input.MissedCount,
		input.NoInformation,
		metadata,
	)

	var event EllieRetrievalQualityEvent
	var metadataBytes []byte
	if err := row.Scan(
		&event.ID,
		&event.OrgID,
		&event.ProjectID,
		&event.RoomID,
		&event.Query,
		&event.TierUsed,
		&event.InjectedCount,
		&event.ReferencedCount,
		&event.MissedCount,
		&event.NoInformation,
		&metadataBytes,
		&event.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert ellie retrieval quality event: %w", err)
	}
	if len(metadataBytes) == 0 {
		event.Metadata = json.RawMessage(`{}`)
	} else {
		event.Metadata = json.RawMessage(metadataBytes)
	}
	return &event, nil
}

func (s *EllieRetrievalQualityEventStore) AggregateByProject(
	ctx context.Context,
	orgID, projectID string,
) (EllieRetrievalQualityAggregate, error) {
	if s == nil || s.db == nil {
		return EllieRetrievalQualityAggregate{}, fmt.Errorf("ellie retrieval quality event store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(orgID) {
		return EllieRetrievalQualityAggregate{}, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(projectID) {
		return EllieRetrievalQualityAggregate{}, fmt.Errorf("invalid project_id")
	}

	agg, err := s.aggregate(
		ctx,
		`SELECT
			COUNT(*)::int,
			COALESCE(SUM(injected_count), 0)::int,
			COALESCE(SUM(referenced_count), 0)::int,
			COALESCE(SUM(missed_count), 0)::int
		 FROM ellie_retrieval_quality_events
		 WHERE org_id = $1
		   AND project_id = $2`,
		orgID,
		projectID,
	)
	if err != nil {
		return EllieRetrievalQualityAggregate{}, err
	}
	agg.OrgID = orgID
	agg.ProjectID = &projectID
	return agg, nil
}

func (s *EllieRetrievalQualityEventStore) AggregateOrgWide(
	ctx context.Context,
	orgID string,
) (EllieRetrievalQualityAggregate, error) {
	if s == nil || s.db == nil {
		return EllieRetrievalQualityAggregate{}, fmt.Errorf("ellie retrieval quality event store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return EllieRetrievalQualityAggregate{}, fmt.Errorf("invalid org_id")
	}

	agg, err := s.aggregate(
		ctx,
		`SELECT
			COUNT(*)::int,
			COALESCE(SUM(injected_count), 0)::int,
			COALESCE(SUM(referenced_count), 0)::int,
			COALESCE(SUM(missed_count), 0)::int
		 FROM ellie_retrieval_quality_events
		 WHERE org_id = $1`,
		orgID,
	)
	if err != nil {
		return EllieRetrievalQualityAggregate{}, err
	}
	agg.OrgID = orgID
	return agg, nil
}

func (s *EllieRetrievalQualityEventStore) aggregate(
	ctx context.Context,
	query string,
	args ...any,
) (EllieRetrievalQualityAggregate, error) {
	var agg EllieRetrievalQualityAggregate
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&agg.EventCount,
		&agg.TotalInjected,
		&agg.TotalReferenced,
		&agg.TotalMissed,
	); err != nil {
		return EllieRetrievalQualityAggregate{}, fmt.Errorf("failed to aggregate ellie retrieval quality events: %w", err)
	}
	agg.Precision = safeRatio(float64(agg.TotalReferenced), float64(agg.TotalInjected))
	agg.Recall = safeRatio(float64(agg.TotalReferenced), float64(agg.TotalReferenced+agg.TotalMissed))
	return agg, nil
}

func normalizeQualityOptionalUUID(value *string, field string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid %s", field)
	}
	return &trimmed, nil
}

func safeRatio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	value := numerator / denominator
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
