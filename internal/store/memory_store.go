package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	MemoryKindSummary    = "summary"
	MemoryKindDecision   = "decision"
	MemoryKindActionItem = "action_item"
	MemoryKindLesson     = "lesson"
	MemoryKindPreference = "preference"
	MemoryKindFact       = "fact"
	MemoryKindFeedback   = "feedback"
	MemoryKindContext    = "context"
)

const (
	MemorySensitivityPublic     = "public"
	MemorySensitivityInternal   = "internal"
	MemorySensitivityRestricted = "restricted"
)

const (
	MemoryStatusActive   = "active"
	MemoryStatusWarm     = "warm"
	MemoryStatusArchived = "archived"
)

var (
	ErrMemoryInvalidAgentID          = errors.New("memory agent_id is invalid")
	ErrMemoryInvalidEntryID          = errors.New("memory id is invalid")
	ErrMemoryInvalidKind             = errors.New("memory kind is invalid")
	ErrMemoryInvalidSensitivity      = errors.New("memory sensitivity is invalid")
	ErrMemoryInvalidStatus           = errors.New("memory status is invalid")
	ErrMemoryInvalidStatusTransition = errors.New("memory status transition is invalid")
	ErrMemoryTitleMissing            = errors.New("memory title is required")
	ErrMemoryContentMissing          = errors.New("memory content is required")
	ErrMemoryInvalidImportance       = errors.New("memory importance must be between 1 and 5")
	ErrMemoryInvalidConfidence       = errors.New("memory confidence must be between 0 and 1")
	ErrMemoryQueryMissing            = errors.New("memory search query is required")
	ErrMemoryInvalidRelevance        = errors.New("memory min relevance must be between 0 and 1")
	ErrDuplicateMemory               = errors.New("duplicate memory entry")
)

var memoryKinds = map[string]struct{}{
	MemoryKindSummary:    {},
	MemoryKindDecision:   {},
	MemoryKindActionItem: {},
	MemoryKindLesson:     {},
	MemoryKindPreference: {},
	MemoryKindFact:       {},
	MemoryKindFeedback:   {},
	MemoryKindContext:    {},
}

var memorySensitivities = map[string]struct{}{
	MemorySensitivityPublic:     {},
	MemorySensitivityInternal:   {},
	MemorySensitivityRestricted: {},
}

type MemoryEntry struct {
	ID            string          `json:"id"`
	OrgID         string          `json:"org_id"`
	AgentID       string          `json:"agent_id"`
	Kind          string          `json:"kind"`
	Title         string          `json:"title"`
	Content       string          `json:"content"`
	Metadata      json.RawMessage `json:"metadata"`
	Importance    int             `json:"importance"`
	Confidence    float64         `json:"confidence"`
	Sensitivity   string          `json:"sensitivity"`
	Status        string          `json:"status"`
	OccurredAt    time.Time       `json:"occurred_at"`
	ExpiresAt     *time.Time      `json:"expires_at,omitempty"`
	SourceSession *string         `json:"source_session,omitempty"`
	SourceProject *string         `json:"source_project,omitempty"`
	SourceIssue   *string         `json:"source_issue,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Relevance     *float64        `json:"relevance,omitempty"`
}

type CreateMemoryEntryInput struct {
	AgentID       string
	Kind          string
	Title         string
	Content       string
	Metadata      json.RawMessage
	Importance    int
	Confidence    float64
	Sensitivity   string
	OccurredAt    time.Time
	ExpiresAt     *time.Time
	SourceSession *string
	SourceProject *string
	SourceIssue   *string
}

type MemorySearchParams struct {
	AgentID       string
	Query         string
	Kinds         []string
	MinRelevance  float64
	MinImportance int
	AllowedScopes []string
	Limit         int
	Since         *time.Time
	Until         *time.Time
	SourceProject *string
}

type RecallContextConfig struct {
	MaxResults    int
	MinRelevance  float64
	MinImportance int
	AllowedScopes []string
	MaxChars      int
}

type MemoryStore struct {
	db *sql.DB
}

func NewMemoryStore(db *sql.DB) *MemoryStore {
	return &MemoryStore{db: db}
}

func (s *MemoryStore) Create(ctx context.Context, input CreateMemoryEntryInput) (*MemoryEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	agentID := strings.TrimSpace(input.AgentID)
	if !uuidRegex.MatchString(agentID) {
		return nil, ErrMemoryInvalidAgentID
	}

	kind, err := normalizeMemoryKind(input.Kind)
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, ErrMemoryTitleMissing
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, ErrMemoryContentMissing
	}

	importance := input.Importance
	if importance == 0 {
		importance = 3
	}
	if importance < 1 || importance > 5 {
		return nil, ErrMemoryInvalidImportance
	}

	confidence := input.Confidence
	if math.IsNaN(confidence) || confidence < 0 || confidence > 1 {
		return nil, ErrMemoryInvalidConfidence
	}

	sensitivity := strings.TrimSpace(strings.ToLower(input.Sensitivity))
	if sensitivity == "" {
		sensitivity = MemorySensitivityInternal
	}
	if _, ok := memorySensitivities[sensitivity]; !ok {
		return nil, ErrMemoryInvalidSensitivity
	}

	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	metadata := normalizeJSONMap(input.Metadata)
	sourceSession := sanitizeOptionalString(input.SourceSession)
	sourceIssue := sanitizeOptionalString(input.SourceIssue)

	sourceProject, err := normalizeOptionalUUID(input.SourceProject)
	if err != nil {
		return nil, fmt.Errorf("source_project: %w", err)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	entry, err := scanMemoryEntry(conn.QueryRowContext(
		ctx,
		`INSERT INTO memory_entries (
			org_id, agent_id, kind, title, content, metadata, importance, confidence,
			sensitivity, status, occurred_at, expires_at, source_session, source_project, source_issue
		) VALUES (
			$1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, $10, $11, $12, $13, $14, $15
		)
		RETURNING
			id, org_id, agent_id, kind, title, content, metadata, importance, confidence,
			sensitivity, status, occurred_at, expires_at, source_session, source_project,
			source_issue, created_at, updated_at`,
		workspaceID,
		agentID,
		kind,
		title,
		content,
		metadata,
		importance,
		confidence,
		sensitivity,
		MemoryStatusActive,
		occurredAt,
		nullableTime(input.ExpiresAt),
		sourceSession,
		sourceProject,
		sourceIssue,
	))
	if err != nil {
		if isMemoryDedupConstraintViolation(err) {
			return nil, ErrDuplicateMemory
		}
		return nil, fmt.Errorf("failed to create memory entry: %w", err)
	}
	return &entry, nil
}

func (s *MemoryStore) ListByAgent(ctx context.Context, agentID, kind string, limit, offset int) ([]MemoryEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedAgentID := strings.TrimSpace(agentID)
	if !uuidRegex.MatchString(normalizedAgentID) {
		return nil, ErrMemoryInvalidAgentID
	}

	normalizedKind := strings.TrimSpace(strings.ToLower(kind))
	if normalizedKind != "" {
		if _, err := normalizeMemoryKind(normalizedKind); err != nil {
			return nil, err
		}
	}

	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT
			id, org_id, agent_id, kind, title, content, metadata, importance, confidence,
			sensitivity, status, occurred_at, expires_at, source_session, source_project,
			source_issue, created_at, updated_at
		FROM memory_entries
		WHERE org_id = $1
		  AND agent_id = $2
		  AND ($3 = '' OR kind = $3)
		ORDER BY occurred_at DESC, created_at DESC
		LIMIT $4
		OFFSET $5`,
		workspaceID,
		normalizedAgentID,
		normalizedKind,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list memory entries: %w", err)
	}
	defer rows.Close()

	entries := make([]MemoryEntry, 0)
	for rows.Next() {
		entry, scanErr := scanMemoryEntry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan memory entry: %w", scanErr)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate memory entries: %w", err)
	}

	return entries, nil
}

func (s *MemoryStore) UpdateStatus(ctx context.Context, id, status string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	entryID := strings.TrimSpace(id)
	if !uuidRegex.MatchString(entryID) {
		return ErrMemoryInvalidEntryID
	}

	targetStatus := strings.TrimSpace(strings.ToLower(status))
	if targetStatus != MemoryStatusActive && targetStatus != MemoryStatusWarm && targetStatus != MemoryStatusArchived {
		return ErrMemoryInvalidStatus
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	var currentStatus string
	if err := conn.QueryRowContext(
		ctx,
		`SELECT status
		   FROM memory_entries
		  WHERE org_id = $1
		    AND id = $2`,
		workspaceID,
		entryID,
	).Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to load memory status: %w", err)
	}

	currentStatus = strings.TrimSpace(strings.ToLower(currentStatus))
	if currentStatus == targetStatus {
		return nil
	}

	if !isValidMemoryStatusTransition(currentStatus, targetStatus) {
		return ErrMemoryInvalidStatusTransition
	}

	result, err := conn.ExecContext(
		ctx,
		`UPDATE memory_entries
		    SET status = $1
		  WHERE org_id = $2
		    AND id = $3`,
		targetStatus,
		workspaceID,
		entryID,
	)
	if err != nil {
		return fmt.Errorf("failed to update memory status: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read memory status update result: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *MemoryStore) Search(ctx context.Context, params MemorySearchParams) ([]MemoryEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	agentID := strings.TrimSpace(params.AgentID)
	if !uuidRegex.MatchString(agentID) {
		return nil, ErrMemoryInvalidAgentID
	}

	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, ErrMemoryQueryMissing
	}
	escapedQuery := escapeLikePattern(query)

	if params.MinRelevance < 0 || params.MinRelevance > 1 {
		return nil, ErrMemoryInvalidRelevance
	}

	minImportance := params.MinImportance
	if minImportance <= 0 {
		minImportance = 1
	}
	if minImportance > 5 {
		return nil, ErrMemoryInvalidImportance
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	kinds, err := normalizeMemoryKinds(params.Kinds)
	if err != nil {
		return nil, err
	}
	sensitivities, err := normalizeMemoryScopes(params.AllowedScopes)
	if err != nil {
		return nil, err
	}

	sourceProject, err := normalizeOptionalUUID(params.SourceProject)
	if err != nil {
		return nil, fmt.Errorf("source_project: %w", err)
	}

	var since interface{}
	if params.Since != nil {
		since = params.Since.UTC()
	}
	var until interface{}
	if params.Until != nil {
		until = params.Until.UTC()
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT
			id, org_id, agent_id, kind, title, content, metadata, importance, confidence,
			sensitivity, status, occurred_at, expires_at, source_session, source_project,
			source_issue, created_at, updated_at, relevance
		FROM (
			SELECT
				id, org_id, agent_id, kind, title, content, metadata, importance, confidence,
				sensitivity, status, occurred_at, expires_at, source_session, source_project,
				source_issue, created_at, updated_at,
				CASE
					WHEN title ILIKE '%' || $9 || '%' ESCAPE '\\' THEN 1.0
					WHEN content ILIKE '%' || $9 || '%' ESCAPE '\\' THEN 0.9
					ELSE 0.0
				END AS relevance
			FROM memory_entries
			WHERE org_id = $1
			  AND agent_id = $2
			  AND kind = ANY($3::text[])
			  AND sensitivity = ANY($4::text[])
			  AND importance >= $5
			  AND status IN ('active', 'warm')
			  AND ($6::timestamptz IS NULL OR occurred_at >= $6)
			  AND ($7::timestamptz IS NULL OR occurred_at <= $7)
			  AND ($8::uuid IS NULL OR source_project = $8::uuid)
			  AND (
				title ILIKE '%' || $9 || '%' ESCAPE '\\'
				OR content ILIKE '%' || $9 || '%' ESCAPE '\\'
			  )
		) ranked
		WHERE relevance >= $10
		ORDER BY relevance DESC, importance DESC, occurred_at DESC
		LIMIT $11`,
		workspaceID,
		agentID,
		pq.Array(kinds),
		pq.Array(sensitivities),
		minImportance,
		since,
		until,
		sourceProject,
		escapedQuery,
		params.MinRelevance,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search memory entries: %w", err)
	}
	defer rows.Close()

	entries := make([]MemoryEntry, 0)
	for rows.Next() {
		entry, scanErr := scanMemoryEntryWithRelevance(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan searched memory entry: %w", scanErr)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate searched memory entries: %w", err)
	}

	return entries, nil
}

func (s *MemoryStore) GetRecallContext(
	ctx context.Context,
	agentID string,
	query string,
	config RecallContextConfig,
) (string, error) {
	maxResults := config.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}

	entries, err := s.Search(ctx, MemorySearchParams{
		AgentID:       agentID,
		Query:         query,
		MinRelevance:  config.MinRelevance,
		MinImportance: config.MinImportance,
		AllowedScopes: config.AllowedScopes,
		Limit:         maxResults,
	})
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}

	var builder strings.Builder
	builder.WriteString("[RECALLED CONTEXT]\n")
	for _, entry := range entries {
		builder.WriteString("- [")
		builder.WriteString(entry.Kind)
		builder.WriteString("] ")
		builder.WriteString(entry.Title)
		builder.WriteString(": ")
		builder.WriteString(entry.Content)
		builder.WriteByte('\n')
	}

	recall := strings.TrimSpace(builder.String())
	recall = truncateRecallText(recall, config.MaxChars)
	return recall, nil
}

func truncateRecallText(recall string, maxChars int) string {
	if maxChars <= 0 || len(recall) <= maxChars {
		return recall
	}
	cut := maxChars
	for cut > 0 && !utf8.RuneStart(recall[cut]) {
		cut -= 1
	}
	if cut <= 0 {
		return ""
	}
	return recall[:cut]
}

func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	entryID := strings.TrimSpace(id)
	if !uuidRegex.MatchString(entryID) {
		return ErrMemoryInvalidEntryID
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	result, err := conn.ExecContext(
		ctx,
		`DELETE FROM memory_entries
		 WHERE org_id = $1
		   AND id = $2`,
		workspaceID,
		entryID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete memory entry: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read delete result: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func normalizeMemoryKind(raw string) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(raw))
	if _, ok := memoryKinds[kind]; !ok {
		return "", ErrMemoryInvalidKind
	}
	return kind, nil
}

func normalizeMemoryKinds(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{
			MemoryKindSummary,
			MemoryKindDecision,
			MemoryKindActionItem,
			MemoryKindLesson,
			MemoryKindPreference,
			MemoryKindFact,
			MemoryKindFeedback,
			MemoryKindContext,
		}, nil
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		kind, err := normalizeMemoryKind(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	return out, nil
}

func normalizeMemoryScopes(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{
			MemorySensitivityPublic,
			MemorySensitivityInternal,
			MemorySensitivityRestricted,
		}, nil
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		scope := strings.TrimSpace(strings.ToLower(value))
		if _, ok := memorySensitivities[scope]; !ok {
			return nil, ErrMemoryInvalidSensitivity
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out, nil
}

func normalizeOptionalUUID(value *string) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, ErrInvalidWorkspace
	}
	return trimmed, nil
}

func sanitizeOptionalString(value *string) interface{} {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func normalizeJSONMap(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(trimmed)
}

func nullableTime(value *time.Time) interface{} {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func isMemoryDedupConstraintViolation(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	if pqErr.Code != "23505" {
		return false
	}
	return pqErr.Constraint == "idx_memory_entries_dedup_active" || pqErr.Constraint == "idx_memory_entries_dedup"
}

func isValidMemoryStatusTransition(currentStatus, targetStatus string) bool {
	switch currentStatus {
	case MemoryStatusActive:
		return targetStatus == MemoryStatusWarm
	case MemoryStatusWarm:
		return targetStatus == MemoryStatusArchived
	default:
		return false
	}
}

func scanMemoryEntry(scanner interface{ Scan(...any) error }) (MemoryEntry, error) {
	var entry MemoryEntry
	var metadataBytes []byte
	var expiresAt sql.NullTime
	var sourceSession sql.NullString
	var sourceProject sql.NullString
	var sourceIssue sql.NullString

	err := scanner.Scan(
		&entry.ID,
		&entry.OrgID,
		&entry.AgentID,
		&entry.Kind,
		&entry.Title,
		&entry.Content,
		&metadataBytes,
		&entry.Importance,
		&entry.Confidence,
		&entry.Sensitivity,
		&entry.Status,
		&entry.OccurredAt,
		&expiresAt,
		&sourceSession,
		&sourceProject,
		&sourceIssue,
		&entry.CreatedAt,
		&entry.UpdatedAt,
	)
	if err != nil {
		return MemoryEntry{}, err
	}

	if len(metadataBytes) == 0 {
		entry.Metadata = json.RawMessage(`{}`)
	} else {
		entry.Metadata = json.RawMessage(metadataBytes)
	}

	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		entry.ExpiresAt = &value
	}
	if sourceSession.Valid {
		value := sourceSession.String
		entry.SourceSession = &value
	}
	if sourceProject.Valid {
		value := sourceProject.String
		entry.SourceProject = &value
	}
	if sourceIssue.Valid {
		value := sourceIssue.String
		entry.SourceIssue = &value
	}

	return entry, nil
}

func scanMemoryEntryWithRelevance(scanner interface{ Scan(...any) error }) (MemoryEntry, error) {
	var entry MemoryEntry
	var metadataBytes []byte
	var expiresAt sql.NullTime
	var sourceSession sql.NullString
	var sourceProject sql.NullString
	var sourceIssue sql.NullString
	var relevance float64

	err := scanner.Scan(
		&entry.ID,
		&entry.OrgID,
		&entry.AgentID,
		&entry.Kind,
		&entry.Title,
		&entry.Content,
		&metadataBytes,
		&entry.Importance,
		&entry.Confidence,
		&entry.Sensitivity,
		&entry.Status,
		&entry.OccurredAt,
		&expiresAt,
		&sourceSession,
		&sourceProject,
		&sourceIssue,
		&entry.CreatedAt,
		&entry.UpdatedAt,
		&relevance,
	)
	if err != nil {
		return MemoryEntry{}, err
	}

	if len(metadataBytes) == 0 {
		entry.Metadata = json.RawMessage(`{}`)
	} else {
		entry.Metadata = json.RawMessage(metadataBytes)
	}
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		entry.ExpiresAt = &value
	}
	if sourceSession.Valid {
		value := sourceSession.String
		entry.SourceSession = &value
	}
	if sourceProject.Valid {
		value := sourceProject.String
		entry.SourceProject = &value
	}
	if sourceIssue.Valid {
		value := sourceIssue.String
		entry.SourceIssue = &value
	}

	entry.Relevance = &relevance
	return entry, nil
}
