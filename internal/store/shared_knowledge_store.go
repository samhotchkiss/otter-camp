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

	"github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	SharedKnowledgeKindDecision   = "decision"
	SharedKnowledgeKindLesson     = "lesson"
	SharedKnowledgeKindPreference = "preference"
	SharedKnowledgeKindFact       = "fact"
	SharedKnowledgeKindPattern    = "pattern"
	SharedKnowledgeKindCorrection = "correction"
)

const (
	SharedKnowledgeScopeTeam = "team"
	SharedKnowledgeScopeOrg  = "org"
)

const (
	SharedKnowledgeStatusActive     = "active"
	SharedKnowledgeStatusStale      = "stale"
	SharedKnowledgeStatusSuperseded = "superseded"
	SharedKnowledgeStatusArchived   = "archived"
)

var (
	ErrSharedKnowledgeInvalidID       = errors.New("shared knowledge id is invalid")
	ErrSharedKnowledgeInvalidAgentID  = errors.New("shared knowledge source_agent_id is invalid")
	ErrSharedKnowledgeInvalidKind     = errors.New("shared knowledge kind is invalid")
	ErrSharedKnowledgeInvalidScope    = errors.New("shared knowledge scope is invalid")
	ErrSharedKnowledgeTitleMissing    = errors.New("shared knowledge title is required")
	ErrSharedKnowledgeContentMissing  = errors.New("shared knowledge content is required")
	ErrSharedKnowledgeInvalidQuality  = errors.New("shared knowledge quality_score must be between 0 and 1")
	ErrSharedKnowledgeSearchRequired  = errors.New("shared knowledge search query is required")
	ErrSharedKnowledgeInvalidTeamName = errors.New("shared knowledge team_name is required")
)

var sharedKnowledgeKinds = map[string]struct{}{
	SharedKnowledgeKindDecision:   {},
	SharedKnowledgeKindLesson:     {},
	SharedKnowledgeKindPreference: {},
	SharedKnowledgeKindFact:       {},
	SharedKnowledgeKindPattern:    {},
	SharedKnowledgeKindCorrection: {},
}

var sharedKnowledgeStatuses = map[string]struct{}{
	SharedKnowledgeStatusActive:     {},
	SharedKnowledgeStatusStale:      {},
	SharedKnowledgeStatusSuperseded: {},
	SharedKnowledgeStatusArchived:   {},
}

type SharedKnowledgeEntry struct {
	ID             string          `json:"id"`
	OrgID          string          `json:"org_id"`
	SourceAgentID  string          `json:"source_agent_id"`
	SourceMemoryID *string         `json:"source_memory_id,omitempty"`
	Kind           string          `json:"kind"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Metadata       json.RawMessage `json:"metadata"`
	Scope          string          `json:"scope"`
	ScopeTeams     []string        `json:"scope_teams"`
	QualityScore   float64         `json:"quality_score"`
	Confirmations  int             `json:"confirmations"`
	Contradictions int             `json:"contradictions"`
	LastAccessedAt *time.Time      `json:"last_accessed_at,omitempty"`
	Status         string          `json:"status"`
	SupersededBy   *string         `json:"superseded_by,omitempty"`
	OccurredAt     time.Time       `json:"occurred_at"`
	ExpiresAt      *time.Time      `json:"expires_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Relevance      *float64        `json:"relevance,omitempty"`
}

type CreateSharedKnowledgeInput struct {
	SourceAgentID  string
	SourceMemoryID *string
	Kind           string
	Title          string
	Content        string
	Metadata       json.RawMessage
	Scope          string
	ScopeTeams     []string
	QualityScore   float64
	OccurredAt     time.Time
	ExpiresAt      *time.Time
}

type SharedKnowledgeSearchParams struct {
	Query      string
	Kinds      []string
	Statuses   []string
	MinQuality float64
	Limit      int
}

type SharedKnowledgeStore struct {
	db *sql.DB
}

func NewSharedKnowledgeStore(db *sql.DB) *SharedKnowledgeStore {
	return &SharedKnowledgeStore{db: db}
}

func (s *SharedKnowledgeStore) Create(ctx context.Context, input CreateSharedKnowledgeInput) (*SharedKnowledgeEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	sourceAgentID := strings.TrimSpace(input.SourceAgentID)
	if !uuidRegex.MatchString(sourceAgentID) {
		return nil, ErrSharedKnowledgeInvalidAgentID
	}

	kind, err := normalizeSharedKnowledgeKind(input.Kind)
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, ErrSharedKnowledgeTitleMissing
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, ErrSharedKnowledgeContentMissing
	}

	scope := strings.TrimSpace(strings.ToLower(input.Scope))
	if scope == "" {
		scope = SharedKnowledgeScopeOrg
	}
	if scope != SharedKnowledgeScopeOrg && scope != SharedKnowledgeScopeTeam {
		return nil, ErrSharedKnowledgeInvalidScope
	}

	scopeTeams, err := normalizeSharedKnowledgeTeams(input.ScopeTeams)
	if err != nil {
		return nil, err
	}

	qualityScore := input.QualityScore
	if qualityScore == 0 {
		qualityScore = 0.5
	}
	if math.IsNaN(qualityScore) || qualityScore < 0 || qualityScore > 1 {
		return nil, ErrSharedKnowledgeInvalidQuality
	}

	if scope == SharedKnowledgeScopeTeam && len(scopeTeams) == 0 {
		return nil, ErrSharedKnowledgeInvalidTeamName
	}

	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	sourceMemoryID, err := normalizeOptionalUUID(input.SourceMemoryID)
	if err != nil {
		return nil, fmt.Errorf("source_memory_id: %w", err)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	entry, err := scanSharedKnowledgeEntry(conn.QueryRowContext(
		ctx,
		`INSERT INTO shared_knowledge (
			org_id, source_agent_id, source_memory_id, kind, title, content, metadata,
			scope, scope_teams, quality_score, status, occurred_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9::text[], $10, $11, $12, $13
		)
		RETURNING
			id, org_id, source_agent_id, source_memory_id, kind, title, content, metadata,
			scope, scope_teams, quality_score, confirmations, contradictions, last_accessed_at,
			status, superseded_by, occurred_at, expires_at, created_at, updated_at`,
		workspaceID,
		sourceAgentID,
		sourceMemoryID,
		kind,
		title,
		content,
		normalizeJSONMap(input.Metadata),
		scope,
		pq.Array(scopeTeams),
		qualityScore,
		SharedKnowledgeStatusActive,
		occurredAt,
		nullableTime(input.ExpiresAt),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create shared knowledge entry: %w", err)
	}
	return &entry, nil
}

func (s *SharedKnowledgeStore) ListForAgent(ctx context.Context, agentID string, limit int) ([]SharedKnowledgeEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedAgentID := strings.TrimSpace(agentID)
	if !uuidRegex.MatchString(normalizedAgentID) {
		return nil, ErrSharedKnowledgeInvalidAgentID
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT
			sk.id, sk.org_id, sk.source_agent_id, sk.source_memory_id, sk.kind, sk.title, sk.content,
			sk.metadata, sk.scope, sk.scope_teams, sk.quality_score, sk.confirmations,
			sk.contradictions, sk.last_accessed_at, sk.status, sk.superseded_by, sk.occurred_at,
			sk.expires_at, sk.created_at, sk.updated_at
		FROM shared_knowledge sk
		WHERE sk.org_id = $1
		  AND sk.status IN ('active', 'stale', 'superseded')
		  AND (
			sk.scope = 'org'
			OR EXISTS (
				SELECT 1
				FROM agent_teams at
				WHERE at.org_id = sk.org_id
				  AND at.agent_id = $2
				  AND at.team_name = ANY(sk.scope_teams)
			)
		  )
		ORDER BY sk.quality_score DESC, sk.occurred_at DESC
		LIMIT $3`,
		workspaceID,
		normalizedAgentID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list shared knowledge entries: %w", err)
	}
	defer rows.Close()

	entries := make([]SharedKnowledgeEntry, 0)
	for rows.Next() {
		entry, scanErr := scanSharedKnowledgeEntry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan shared knowledge entry: %w", scanErr)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate shared knowledge entries: %w", err)
	}
	return entries, nil
}

func (s *SharedKnowledgeStore) Search(ctx context.Context, params SharedKnowledgeSearchParams) ([]SharedKnowledgeEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, ErrSharedKnowledgeSearchRequired
	}
	escapedQuery := escapeLikePattern(query)

	kinds, err := normalizeSharedKnowledgeKinds(params.Kinds)
	if err != nil {
		return nil, err
	}
	statuses, err := normalizeSharedKnowledgeStatuses(params.Statuses)
	if err != nil {
		return nil, err
	}

	minQuality := params.MinQuality
	if minQuality < 0 || minQuality > 1 {
		return nil, ErrSharedKnowledgeInvalidQuality
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT
			id, org_id, source_agent_id, source_memory_id, kind, title, content, metadata,
			scope, scope_teams, quality_score, confirmations, contradictions, last_accessed_at,
			status, superseded_by, occurred_at, expires_at, created_at, updated_at, relevance
		FROM (
			SELECT
				id, org_id, source_agent_id, source_memory_id, kind, title, content, metadata,
				scope, scope_teams, quality_score, confirmations, contradictions, last_accessed_at,
				status, superseded_by, occurred_at, expires_at, created_at, updated_at,
				CASE
					WHEN title ILIKE '%' || $5 || '%' ESCAPE '\\' THEN 1.0
					WHEN content ILIKE '%' || $5 || '%' ESCAPE '\\' THEN 0.9
					ELSE 0.0
				END AS relevance
			FROM shared_knowledge
			WHERE org_id = $1
			  AND kind = ANY($2::text[])
			  AND status = ANY($3::text[])
			  AND quality_score >= $4
			  AND (
				title ILIKE '%' || $5 || '%' ESCAPE '\\'
				OR content ILIKE '%' || $5 || '%' ESCAPE '\\'
			  )
		) ranked
		ORDER BY relevance DESC, quality_score DESC, occurred_at DESC
		LIMIT $6`,
		workspaceID,
		pq.Array(kinds),
		pq.Array(statuses),
		minQuality,
		escapedQuery,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search shared knowledge entries: %w", err)
	}
	defer rows.Close()

	entries := make([]SharedKnowledgeEntry, 0)
	for rows.Next() {
		entry, scanErr := scanSharedKnowledgeEntryWithRelevance(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan searched shared knowledge entry: %w", scanErr)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate searched shared knowledge entries: %w", err)
	}

	return entries, nil
}

func (s *SharedKnowledgeStore) Confirm(ctx context.Context, id string) (*SharedKnowledgeEntry, error) {
	return s.applySignal(ctx, id, true)
}

func (s *SharedKnowledgeStore) Contradict(ctx context.Context, id string) (*SharedKnowledgeEntry, error) {
	return s.applySignal(ctx, id, false)
}

func (s *SharedKnowledgeStore) applySignal(ctx context.Context, id string, confirm bool) (*SharedKnowledgeEntry, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	entryID := strings.TrimSpace(id)
	if !uuidRegex.MatchString(entryID) {
		return nil, ErrSharedKnowledgeInvalidID
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var query string
	if confirm {
		query = `UPDATE shared_knowledge
		         SET confirmations = confirmations + 1,
		             quality_score = LEAST(1.0, quality_score + 0.1),
		             updated_at = NOW()
		         WHERE org_id = $1
		           AND id = $2
		         RETURNING
		         	id, org_id, source_agent_id, source_memory_id, kind, title, content, metadata,
		         	scope, scope_teams, quality_score, confirmations, contradictions, last_accessed_at,
		         	status, superseded_by, occurred_at, expires_at, created_at, updated_at`
	} else {
		query = `UPDATE shared_knowledge
		         SET contradictions = contradictions + 1,
		             quality_score = GREATEST(0.0, quality_score - 0.15),
		             updated_at = NOW()
		         WHERE org_id = $1
		           AND id = $2
		         RETURNING
		         	id, org_id, source_agent_id, source_memory_id, kind, title, content, metadata,
		         	scope, scope_teams, quality_score, confirmations, contradictions, last_accessed_at,
		         	status, superseded_by, occurred_at, expires_at, created_at, updated_at`
	}

	entry, err := scanSharedKnowledgeEntry(conn.QueryRowContext(ctx, query, workspaceID, entryID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to apply shared knowledge signal: %w", err)
	}
	return &entry, nil
}

func normalizeSharedKnowledgeKind(raw string) (string, error) {
	kind := strings.TrimSpace(strings.ToLower(raw))
	if _, ok := sharedKnowledgeKinds[kind]; !ok {
		return "", ErrSharedKnowledgeInvalidKind
	}
	return kind, nil
}

func normalizeSharedKnowledgeKinds(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{
			SharedKnowledgeKindDecision,
			SharedKnowledgeKindLesson,
			SharedKnowledgeKindPreference,
			SharedKnowledgeKindFact,
			SharedKnowledgeKindPattern,
			SharedKnowledgeKindCorrection,
		}, nil
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		kind, err := normalizeSharedKnowledgeKind(value)
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

func normalizeSharedKnowledgeStatuses(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{
			SharedKnowledgeStatusActive,
			SharedKnowledgeStatusStale,
			SharedKnowledgeStatusSuperseded,
			SharedKnowledgeStatusArchived,
		}, nil
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		status := strings.TrimSpace(strings.ToLower(value))
		if _, ok := sharedKnowledgeStatuses[status]; !ok {
			return nil, ErrValidation
		}
		if _, ok := seen[status]; ok {
			continue
		}
		seen[status] = struct{}{}
		out = append(out, status)
	}
	return out, nil
}

func normalizeSharedKnowledgeTeams(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}

	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		team := strings.TrimSpace(strings.ToLower(value))
		if team == "" {
			return nil, ErrSharedKnowledgeInvalidTeamName
		}
		if _, ok := seen[team]; ok {
			continue
		}
		seen[team] = struct{}{}
		out = append(out, team)
	}
	return out, nil
}

func scanSharedKnowledgeEntry(scanner interface{ Scan(...any) error }) (SharedKnowledgeEntry, error) {
	var entry SharedKnowledgeEntry
	var metadataBytes []byte
	var sourceMemoryID sql.NullString
	var scopeTeams []string
	var lastAccessedAt sql.NullTime
	var supersededBy sql.NullString
	var expiresAt sql.NullTime

	err := scanner.Scan(
		&entry.ID,
		&entry.OrgID,
		&entry.SourceAgentID,
		&sourceMemoryID,
		&entry.Kind,
		&entry.Title,
		&entry.Content,
		&metadataBytes,
		&entry.Scope,
		pq.Array(&scopeTeams),
		&entry.QualityScore,
		&entry.Confirmations,
		&entry.Contradictions,
		&lastAccessedAt,
		&entry.Status,
		&supersededBy,
		&entry.OccurredAt,
		&expiresAt,
		&entry.CreatedAt,
		&entry.UpdatedAt,
	)
	if err != nil {
		return SharedKnowledgeEntry{}, err
	}

	if len(metadataBytes) == 0 {
		entry.Metadata = json.RawMessage(`{}`)
	} else {
		entry.Metadata = json.RawMessage(metadataBytes)
	}
	entry.ScopeTeams = scopeTeams
	if sourceMemoryID.Valid {
		value := sourceMemoryID.String
		entry.SourceMemoryID = &value
	}
	if supersededBy.Valid {
		value := supersededBy.String
		entry.SupersededBy = &value
	}
	if lastAccessedAt.Valid {
		value := lastAccessedAt.Time.UTC()
		entry.LastAccessedAt = &value
	}
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		entry.ExpiresAt = &value
	}
	return entry, nil
}

func scanSharedKnowledgeEntryWithRelevance(scanner interface{ Scan(...any) error }) (SharedKnowledgeEntry, error) {
	var entry SharedKnowledgeEntry
	var metadataBytes []byte
	var sourceMemoryID sql.NullString
	var scopeTeams []string
	var lastAccessedAt sql.NullTime
	var supersededBy sql.NullString
	var expiresAt sql.NullTime
	var relevance float64

	err := scanner.Scan(
		&entry.ID,
		&entry.OrgID,
		&entry.SourceAgentID,
		&sourceMemoryID,
		&entry.Kind,
		&entry.Title,
		&entry.Content,
		&metadataBytes,
		&entry.Scope,
		pq.Array(&scopeTeams),
		&entry.QualityScore,
		&entry.Confirmations,
		&entry.Contradictions,
		&lastAccessedAt,
		&entry.Status,
		&supersededBy,
		&entry.OccurredAt,
		&expiresAt,
		&entry.CreatedAt,
		&entry.UpdatedAt,
		&relevance,
	)
	if err != nil {
		return SharedKnowledgeEntry{}, err
	}

	if len(metadataBytes) == 0 {
		entry.Metadata = json.RawMessage(`{}`)
	} else {
		entry.Metadata = json.RawMessage(metadataBytes)
	}
	entry.ScopeTeams = scopeTeams
	if sourceMemoryID.Valid {
		value := sourceMemoryID.String
		entry.SourceMemoryID = &value
	}
	if supersededBy.Valid {
		value := supersededBy.String
		entry.SupersededBy = &value
	}
	if lastAccessedAt.Valid {
		value := lastAccessedAt.Time.UTC()
		entry.LastAccessedAt = &value
	}
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		entry.ExpiresAt = &value
	}

	entry.Relevance = &relevance
	return entry, nil
}
