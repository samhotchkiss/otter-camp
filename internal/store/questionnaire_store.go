package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	QuestionnaireContextIssue       = "issue"
	QuestionnaireContextProjectChat = "project_chat"
	QuestionnaireContextTemplate    = "template"
)

var ErrQuestionnaireAlreadyResponded = errors.New("questionnaire already responded")

type Questionnaire struct {
	ID          string          `json:"id"`
	OrgID       string          `json:"org_id"`
	ContextType string          `json:"context_type"`
	ContextID   string          `json:"context_id"`
	Author      string          `json:"author"`
	Title       *string         `json:"title,omitempty"`
	Questions   json.RawMessage `json:"questions"`
	Responses   json.RawMessage `json:"responses,omitempty"`
	RespondedBy *string         `json:"responded_by,omitempty"`
	RespondedAt *time.Time      `json:"responded_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type CreateQuestionnaireInput struct {
	ContextType string
	ContextID   string
	Author      string
	Title       *string
	Questions   json.RawMessage
}

type RespondQuestionnaireInput struct {
	QuestionnaireID string
	RespondedBy     string
	Responses       json.RawMessage
}

type QuestionnaireStore struct {
	db *sql.DB
}

func NewQuestionnaireStore(db *sql.DB) *QuestionnaireStore {
	return &QuestionnaireStore{db: db}
}

const questionnaireColumns = `
	id,
	org_id,
	context_type,
	context_id,
	author,
	title,
	questions,
	responses,
	responded_by,
	responded_at,
	created_at
`

func (s *QuestionnaireStore) Create(ctx context.Context, input CreateQuestionnaireInput) (*Questionnaire, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	contextType := strings.TrimSpace(input.ContextType)
	if !isValidQuestionnaireContextType(contextType) {
		return nil, fmt.Errorf("invalid context_type")
	}
	contextID := strings.TrimSpace(input.ContextID)
	if !uuidRegex.MatchString(contextID) {
		return nil, fmt.Errorf("invalid context_id")
	}
	author := strings.TrimSpace(input.Author)
	if author == "" {
		return nil, fmt.Errorf("author is required")
	}
	if len(input.Questions) == 0 || !json.Valid(input.Questions) {
		return nil, fmt.Errorf("questions is required")
	}

	title := normalizeOptionalString(input.Title)

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensureQuestionnaireContextVisible(ctx, conn, contextType, contextID); err != nil {
		return nil, err
	}

	record, err := scanQuestionnaire(conn.QueryRowContext(
		ctx,
		`INSERT INTO questionnaires (
			org_id,
			context_type,
			context_id,
			author,
			title,
			questions
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		RETURNING `+questionnaireColumns,
		workspaceID,
		contextType,
		contextID,
		author,
		nullableString(title),
		[]byte(input.Questions),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create questionnaire: %w", err)
	}
	return &record, nil
}

func (s *QuestionnaireStore) ListByContext(
	ctx context.Context,
	contextType string,
	contextID string,
) ([]Questionnaire, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	contextType = strings.TrimSpace(contextType)
	if !isValidQuestionnaireContextType(contextType) {
		return nil, fmt.Errorf("invalid context_type")
	}
	contextID = strings.TrimSpace(contextID)
	if !uuidRegex.MatchString(contextID) {
		return nil, fmt.Errorf("invalid context_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensureQuestionnaireContextVisible(ctx, conn, contextType, contextID); err != nil {
		return nil, err
	}

	rows, err := conn.QueryContext(
		ctx,
		`SELECT `+questionnaireColumns+` FROM questionnaires
		 WHERE context_type = $1 AND context_id = $2 AND org_id = $3
		 ORDER BY created_at ASC, id ASC`,
		contextType,
		contextID,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list questionnaires: %w", err)
	}
	defer rows.Close()

	out := make([]Questionnaire, 0)
	for rows.Next() {
		record, scanErr := scanQuestionnaire(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan questionnaire: %w", scanErr)
		}
		if record.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read questionnaire rows: %w", err)
	}
	return out, nil
}

func (s *QuestionnaireStore) GetByID(ctx context.Context, questionnaireID string) (*Questionnaire, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	questionnaireID = strings.TrimSpace(questionnaireID)
	if !uuidRegex.MatchString(questionnaireID) {
		return nil, fmt.Errorf("invalid questionnaire_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	record, err := scanQuestionnaire(conn.QueryRowContext(
		ctx,
		`SELECT `+questionnaireColumns+` FROM questionnaires WHERE id = $1 AND org_id = $2`,
		questionnaireID,
		workspaceID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get questionnaire: %w", err)
	}
	if record.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &record, nil
}

func (s *QuestionnaireStore) Respond(ctx context.Context, input RespondQuestionnaireInput) (*Questionnaire, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	questionnaireID := strings.TrimSpace(input.QuestionnaireID)
	if !uuidRegex.MatchString(questionnaireID) {
		return nil, fmt.Errorf("invalid questionnaire_id")
	}
	respondedBy := strings.TrimSpace(input.RespondedBy)
	if respondedBy == "" {
		return nil, fmt.Errorf("responded_by is required")
	}
	if len(input.Responses) == 0 || !json.Valid(input.Responses) {
		return nil, fmt.Errorf("responses is required")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	record, err := scanQuestionnaire(tx.QueryRowContext(
		ctx,
		`SELECT `+questionnaireColumns+` FROM questionnaires
		 WHERE id = $1 AND org_id = $2
		 FOR UPDATE`,
		questionnaireID,
		workspaceID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load questionnaire: %w", err)
	}
	if record.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	if record.RespondedAt != nil {
		return nil, ErrQuestionnaireAlreadyResponded
	}

	updated, err := scanQuestionnaire(tx.QueryRowContext(
		ctx,
		`UPDATE questionnaires
		 SET responses = $2::jsonb,
		     responded_by = $3,
		     responded_at = NOW()
		 WHERE id = $1 AND org_id = $4
		 RETURNING `+questionnaireColumns,
		questionnaireID,
		[]byte(input.Responses),
		respondedBy,
		workspaceID,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to respond questionnaire: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit questionnaire response: %w", err)
	}
	return &updated, nil
}

func scanQuestionnaire(scanner interface{ Scan(dest ...any) error }) (Questionnaire, error) {
	var record Questionnaire
	var title sql.NullString
	var responses []byte
	var respondedBy sql.NullString
	var respondedAt sql.NullTime

	err := scanner.Scan(
		&record.ID,
		&record.OrgID,
		&record.ContextType,
		&record.ContextID,
		&record.Author,
		&title,
		&record.Questions,
		&responses,
		&respondedBy,
		&respondedAt,
		&record.CreatedAt,
	)
	if err != nil {
		return Questionnaire{}, err
	}

	if title.Valid {
		trimmed := strings.TrimSpace(title.String)
		record.Title = &trimmed
	}
	if len(responses) > 0 {
		record.Responses = responses
	}
	if respondedBy.Valid {
		value := respondedBy.String
		record.RespondedBy = &value
	}
	if respondedAt.Valid {
		value := respondedAt.Time.UTC()
		record.RespondedAt = &value
	}
	record.CreatedAt = record.CreatedAt.UTC()

	return record, nil
}

func isValidQuestionnaireContextType(contextType string) bool {
	switch contextType {
	case QuestionnaireContextIssue, QuestionnaireContextProjectChat, QuestionnaireContextTemplate:
		return true
	default:
		return false
	}
}

func ensureQuestionnaireContextVisible(ctx context.Context, q Querier, contextType, contextID string) error {
	switch contextType {
	case QuestionnaireContextIssue:
		return ensureIssueVisible(ctx, q, contextID)
	case QuestionnaireContextProjectChat:
		return ensureProjectVisible(ctx, q, contextID)
	case QuestionnaireContextTemplate:
		// TODO: verify template exists when a templates table is introduced.
		return nil
	default:
		return fmt.Errorf("invalid context_type")
	}
}

func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
