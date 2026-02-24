package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	ChatSummarizeJobType      = "chat_summarize"
	ChatSessionCleanupJobType = "chat_session_cleanup"
	ChatRetentionSweepJobType = "chat_retention_sweep"

	CleanupTypeEphemeralPurge     = "ephemeral_purge"
	CleanupTypeToolCompaction     = "tool_result_compaction"
	CleanupTypeSummaryConsolidate = "summary_consolidation"

	summarizationInvocationPurpose = "summarization"
	summarizationThresholdRatio    = 0.55
	summarizationPercentile        = 0.28
	cacheEstimateTTL               = 30 * time.Second
	tokenEstimatorDivisor          = 4
	messageEstimatorOverheadChars  = 16

	toolResultCompactionThresholdBytes = 4 * 1024
	retentionWindow                    = 90 * 24 * time.Hour
)

var ErrAlreadySummarized = errors.New("session range already summarized")

type chatSummaryRepository interface {
	Create(ctx context.Context, summary repo.ChatSummary) (repo.ChatSummary, error)
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatSummary, error)
}

type SummarizationCheckerOptions struct {
	Pool      *pgxpool.Pool
	Messages  chatMessageRepository
	Summaries chatSummaryRepository
	Now       func() time.Time
}

type SummarizationChecker struct {
	messages  chatMessageRepository
	summaries chatSummaryRepository
	now       func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]cachedEstimate
}

type cachedEstimate struct {
	tokens    int
	expiresAt time.Time
}

func NewSummarizationChecker(opts SummarizationCheckerOptions) (*SummarizationChecker, error) {
	if opts.Pool == nil && (opts.Messages == nil || opts.Summaries == nil) {
		return nil, fmt.Errorf("summarization checker requires pool or explicit repositories")
	}

	checker := &SummarizationChecker{
		messages:  opts.Messages,
		summaries: opts.Summaries,
		now:       opts.Now,
		cache:     make(map[uuid.UUID]cachedEstimate),
	}
	if checker.now == nil {
		checker.now = time.Now
	}
	if checker.messages == nil {
		checker.messages = repo.NewChatMessageRepo(opts.Pool)
	}
	if checker.summaries == nil {
		checker.summaries = repo.NewChatSummaryRepo(opts.Pool)
	}
	return checker, nil
}

func (c *SummarizationChecker) ShouldSummarize(ctx context.Context, sessionID uuid.UUID, layerBudgetTokens int) (bool, error) {
	if c == nil || c.messages == nil || c.summaries == nil {
		return false, fmt.Errorf("summarization checker is not configured")
	}
	if sessionID == uuid.Nil {
		return false, fmt.Errorf("session id is required")
	}
	if layerBudgetTokens <= 0 {
		return false, nil
	}

	estimate, err := c.unsummarizedTokenEstimate(ctx, sessionID)
	if err != nil {
		return false, err
	}

	threshold := int(math.Ceil(float64(layerBudgetTokens) * summarizationThresholdRatio))
	if threshold <= 0 {
		threshold = 1
	}
	return estimate >= threshold, nil
}

func (c *SummarizationChecker) unsummarizedTokenEstimate(ctx context.Context, sessionID uuid.UUID) (int, error) {
	now := c.now().UTC()

	c.mu.Lock()
	if cached, ok := c.cache[sessionID]; ok && now.Before(cached.expiresAt) {
		value := cached.tokens
		c.mu.Unlock()
		return value, nil
	}
	c.mu.Unlock()

	messages, err := c.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	summaries, err := c.summaries.ListBySession(ctx, sessionID)
	if err != nil {
		return 0, err
	}

	unsummarized := filterUnsummarizedMessages(messages, summaries)
	charCount := 0
	for _, message := range unsummarized {
		charCount += len(message.Content) + messageEstimatorOverheadChars
	}
	estimated := estimateTokensFromChars(charCount)

	c.mu.Lock()
	c.cache[sessionID] = cachedEstimate{tokens: estimated, expiresAt: now.Add(cacheEstimateTTL)}
	c.mu.Unlock()

	return estimated, nil
}

type SummarizationRequest struct {
	OrganizationID    uuid.UUID
	SessionID         uuid.UUID
	InvocationPurpose string
	Profile           repo.ModelProfile
	Prompt            string
	Messages          []repo.ChatMessage
	Summaries         []repo.ChatSummary
}

type SummarizationResponse struct {
	SummaryText       string
	ModelInvocationID *uuid.UUID
}

type SummarizationModel interface {
	Summarize(ctx context.Context, req SummarizationRequest) (SummarizationResponse, error)
}

type SummarizerOptions struct {
	Pool      *pgxpool.Pool
	Sessions  chatSessionRepository
	Messages  chatMessageRepository
	Summaries chatSummaryRepository
	Tasks     projectTaskRepository
	Resolver  model.ProfileResolver
	Model     SummarizationModel
	Prompt    string
}

type Summarizer struct {
	sessions  chatSessionRepository
	messages  chatMessageRepository
	summaries chatSummaryRepository
	tasks     projectTaskRepository
	resolver  model.ProfileResolver
	model     SummarizationModel
	prompt    string
}

func NewSummarizer(opts SummarizerOptions) (*Summarizer, error) {
	if opts.Model == nil {
		return nil, fmt.Errorf("summarizer model is required")
	}
	if opts.Pool == nil && (opts.Sessions == nil || opts.Messages == nil || opts.Summaries == nil || opts.Tasks == nil || opts.Resolver == nil) {
		return nil, fmt.Errorf("summarizer requires pool or explicit dependencies")
	}

	s := &Summarizer{
		sessions:  opts.Sessions,
		messages:  opts.Messages,
		summaries: opts.Summaries,
		tasks:     opts.Tasks,
		resolver:  opts.Resolver,
		model:     opts.Model,
		prompt:    strings.TrimSpace(opts.Prompt),
	}
	if s.sessions == nil {
		s.sessions = repo.NewChatSessionRepo(opts.Pool)
	}
	if s.messages == nil {
		s.messages = repo.NewChatMessageRepo(opts.Pool)
	}
	if s.summaries == nil {
		s.summaries = repo.NewChatSummaryRepo(opts.Pool)
	}
	if s.tasks == nil {
		s.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if s.resolver == nil {
		s.resolver = model.NewProfileResolver(repo.NewModelProfileAssignmentRepo(opts.Pool), repo.NewModelProfileRepo(opts.Pool))
	}
	if s.prompt == "" {
		s.prompt = defaultSummarizationPrompt()
	}

	return s, nil
}

func (s *Summarizer) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if s == nil || registrar == nil {
		return
	}

	registrar.Register(ChatSummarizeJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload ChatSummarizePayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("decode chat_summarize payload: %w", err)
		}
		if payload.SessionID == uuid.Nil {
			return fmt.Errorf("chat_summarize payload missing session_id")
		}
		_, err := s.RunForSession(ctx, payload.SessionID)
		if errors.Is(err, ErrAlreadySummarized) {
			return nil
		}
		return err
	})
}

func (s *Summarizer) RunForSession(ctx context.Context, sessionID uuid.UUID) (*repo.ChatSummary, error) {
	if s == nil || s.sessions == nil || s.messages == nil || s.summaries == nil || s.resolver == nil || s.model == nil {
		return nil, fmt.Errorf("summarizer is not configured")
	}
	if sessionID == uuid.Nil {
		return nil, fmt.Errorf("session id is required")
	}

	session, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages, err := s.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	summaries, err := s.summaries.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	candidate, err := summarizeCandidate(messages, summaries)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, ErrAlreadySummarized
	}
	if rangeCovered(candidate.FromSequence, candidate.ToSequence, summaries) {
		return nil, ErrAlreadySummarized
	}

	profile, err := s.resolveSessionProfile(ctx, session)
	if err != nil {
		return nil, err
	}

	response, err := s.model.Summarize(ctx, SummarizationRequest{
		OrganizationID:    session.OrganizationID,
		SessionID:         session.ID,
		InvocationPurpose: summarizationInvocationPurpose,
		Profile:           profile,
		Prompt:            s.prompt,
		Messages:          candidate.Messages,
	})
	if err != nil {
		return nil, err
	}

	created, err := s.summaries.Create(ctx, repo.ChatSummary{
		SessionID:           session.ID,
		FromSequence:        candidate.FromSequence,
		ToSequence:          candidate.ToSequence,
		SummaryText:         strings.TrimSpace(response.SummaryText),
		SummarizedTurnCount: candidate.TurnCount,
		ModelInvocationID:   response.ModelInvocationID,
	})
	if errors.Is(err, repo.ErrConflict) {
		return nil, ErrAlreadySummarized
	}
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *Summarizer) resolveSessionProfile(ctx context.Context, session repo.ChatSession) (repo.ModelProfile, error) {
	scopes, err := s.sessionScopes(ctx, session)
	if err != nil {
		return repo.ModelProfile{}, err
	}
	profile, err := s.resolver.Resolve(ctx, session.OrganizationID, summarizationInvocationPurpose, scopes...)
	if err != nil {
		return repo.ModelProfile{}, err
	}
	return *profile, nil
}

func (s *Summarizer) sessionScopes(ctx context.Context, session repo.ChatSession) ([]model.Scope, error) {
	scopes := make([]model.Scope, 0, 1)

	switch strings.TrimSpace(session.ScopeType) {
	case "project":
		scopes = append(scopes, model.Scope{Type: "project", ID: session.ScopeID})
	case "project_task":
		task, err := s.tasks.GetByID(ctx, session.ScopeID)
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, model.Scope{Type: "project", ID: task.ProjectID})
	}
	return scopes, nil
}

type SessionCleanerOptions struct {
	Pool      *pgxpool.Pool
	Sessions  chatSessionRepository
	Summaries chatSummaryRepository
	Tasks     projectTaskRepository
	Resolver  model.ProfileResolver
	Model     SummarizationModel
	Now       func() time.Time
}

type SessionCleaner struct {
	pool      *pgxpool.Pool
	sessions  chatSessionRepository
	summaries chatSummaryRepository
	tasks     projectTaskRepository
	resolver  model.ProfileResolver
	model     SummarizationModel
	now       func() time.Time
}

func NewSessionCleaner(opts SessionCleanerOptions) (*SessionCleaner, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("session cleaner requires a database pool")
	}

	cleaner := &SessionCleaner{
		pool:      opts.Pool,
		sessions:  opts.Sessions,
		summaries: opts.Summaries,
		tasks:     opts.Tasks,
		resolver:  opts.Resolver,
		model:     opts.Model,
		now:       opts.Now,
	}
	if cleaner.now == nil {
		cleaner.now = time.Now
	}
	if cleaner.sessions == nil {
		cleaner.sessions = repo.NewChatSessionRepo(opts.Pool)
	}
	if cleaner.summaries == nil {
		cleaner.summaries = repo.NewChatSummaryRepo(opts.Pool)
	}
	if cleaner.tasks == nil {
		cleaner.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if cleaner.resolver == nil {
		cleaner.resolver = model.NewProfileResolver(repo.NewModelProfileAssignmentRepo(opts.Pool), repo.NewModelProfileRepo(opts.Pool))
	}
	return cleaner, nil
}

func (c *SessionCleaner) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if c == nil || registrar == nil {
		return
	}

	registrar.Register(ChatSessionCleanupJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload ChatSessionCleanupPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("decode chat_session_cleanup payload: %w", err)
		}
		if payload.SessionID == uuid.Nil {
			return fmt.Errorf("chat_session_cleanup payload missing session_id")
		}
		return c.RunCleanup(ctx, payload.SessionID, payload.CleanupType)
	})
}

func (c *SessionCleaner) RunCleanup(ctx context.Context, sessionID uuid.UUID, cleanupType string) error {
	if c == nil || c.pool == nil || c.sessions == nil {
		return fmt.Errorf("session cleaner is not configured")
	}
	if sessionID == uuid.Nil {
		return fmt.Errorf("session id is required")
	}

	session, err := c.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.Status != "closed" {
		return nil
	}

	switch strings.TrimSpace(cleanupType) {
	case CleanupTypeEphemeralPurge:
		return c.runEphemeralPurge(ctx, session)
	case CleanupTypeToolCompaction:
		return c.runToolResultCompaction(ctx, session)
	case CleanupTypeSummaryConsolidate:
		return c.runSummaryConsolidation(ctx, session)
	default:
		return fmt.Errorf("invalid cleanup_type %q", cleanupType)
	}
}

func (c *SessionCleaner) runEphemeralPurge(ctx context.Context, session repo.ChatSession) error {
	if session.Mode != "async" {
		return nil
	}
	if !hasFlowNodeExecutionMetadata(session.Metadata) {
		return nil
	}
	cutoff := c.now().UTC().Add(-retentionWindow)
	_, err := c.pool.Exec(ctx, `
		DELETE FROM chat_message
		WHERE session_id = $1
		  AND role IN ('tool_call', 'tool_result')
		  AND created_at < $2
	`, session.ID, cutoff)
	return mapDBError(err)
}

func (c *SessionCleaner) runToolResultCompaction(ctx context.Context, session repo.ChatSession) error {
	rows, err := c.pool.Query(ctx, `
		SELECT id, content, metadata
		FROM chat_message
		WHERE session_id = $1
		  AND role = 'tool_result'
		  AND status = 'final'
		  AND octet_length(content) > $2
	`, session.ID, toolResultCompactionThresholdBytes)
	if err != nil {
		return mapDBError(err)
	}
	defer rows.Close()

	type compactCandidate struct {
		id       uuid.UUID
		content  string
		metadata json.RawMessage
	}
	candidates := make([]compactCandidate, 0)
	for rows.Next() {
		var candidate compactCandidate
		if scanErr := rows.Scan(&candidate.id, &candidate.content, &candidate.metadata); scanErr != nil {
			return mapDBError(scanErr)
		}
		candidates = append(candidates, candidate)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return mapDBError(rowsErr)
	}

	for _, candidate := range candidates {
		updatedContent, updatedMetadata := compactToolResultPayload(candidate.content, candidate.metadata)
		if _, err := c.pool.Exec(ctx, `
			UPDATE chat_message
			SET content = $2,
			    metadata = $3::jsonb
			WHERE id = $1
		`, candidate.id, updatedContent, updatedMetadata); err != nil {
			return mapDBError(err)
		}
	}
	return nil
}

func (c *SessionCleaner) runSummaryConsolidation(ctx context.Context, session repo.ChatSession) error {
	if c.model == nil {
		return fmt.Errorf("session cleaner model is required for summary consolidation")
	}

	summaries, err := c.summaries.ListBySession(ctx, session.ID)
	if err != nil {
		return err
	}
	if len(summaries) <= 5 {
		return nil
	}

	sorter := make([]repo.ChatSummary, len(summaries))
	copy(sorter, summaries)
	sort.Slice(sorter, func(i, j int) bool {
		if sorter[i].FromSequence == sorter[j].FromSequence {
			return sorter[i].CreatedAt.Before(sorter[j].CreatedAt)
		}
		return sorter[i].FromSequence < sorter[j].FromSequence
	})

	fromSequence := sorter[0].FromSequence
	toSequence := sorter[len(sorter)-1].ToSequence
	turnCount := 0
	for _, summary := range sorter {
		turnCount += summary.SummarizedTurnCount
	}

	profile, err := c.resolveSessionProfile(ctx, session)
	if err != nil {
		return err
	}
	response, err := c.model.Summarize(ctx, SummarizationRequest{
		OrganizationID:    session.OrganizationID,
		SessionID:         session.ID,
		InvocationPurpose: summarizationInvocationPurpose,
		Profile:           profile,
		Prompt:            defaultConsolidationPrompt(),
		Summaries:         sorter,
	})
	if err != nil {
		return err
	}

	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, session.ID.String()); err != nil {
		return mapDBError(err)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM chat_summary WHERE session_id = $1`, session.ID).Scan(&count); err != nil {
		return mapDBError(err)
	}
	if count <= 5 {
		if err := tx.Commit(ctx); err != nil {
			return mapDBError(err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `DELETE FROM chat_summary WHERE session_id = $1`, session.ID); err != nil {
		return mapDBError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO chat_summary (session_id, from_sequence, to_sequence, summary_text, summarized_turn_count, model_invocation_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, session.ID, fromSequence, toSequence, strings.TrimSpace(response.SummaryText), turnCount, response.ModelInvocationID); err != nil {
		return mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return mapDBError(err)
	}
	return nil
}

func (c *SessionCleaner) resolveSessionProfile(ctx context.Context, session repo.ChatSession) (repo.ModelProfile, error) {
	scopes := make([]model.Scope, 0, 1)
	switch strings.TrimSpace(session.ScopeType) {
	case "project":
		scopes = append(scopes, model.Scope{Type: "project", ID: session.ScopeID})
	case "project_task":
		task, err := c.tasks.GetByID(ctx, session.ScopeID)
		if err != nil {
			return repo.ModelProfile{}, err
		}
		scopes = append(scopes, model.Scope{Type: "project", ID: task.ProjectID})
	}
	profile, err := c.resolver.Resolve(ctx, session.OrganizationID, summarizationInvocationPurpose, scopes...)
	if err != nil {
		return repo.ModelProfile{}, err
	}
	return *profile, nil
}

type RetentionSweeperOptions struct {
	Pool *pgxpool.Pool
	Now  func() time.Time
}

type RetentionSweeper struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRetentionSweeper(opts RetentionSweeperOptions) (*RetentionSweeper, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("retention sweeper requires a database pool")
	}
	sweeper := &RetentionSweeper{pool: opts.Pool, now: opts.Now}
	if sweeper.now == nil {
		sweeper.now = time.Now
	}
	return sweeper, nil
}

func (s *RetentionSweeper) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if s == nil || registrar == nil {
		return
	}

	registrar.Register(ChatRetentionSweepJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload ChatRetentionSweepPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("decode chat_retention_sweep payload: %w", err)
		}
		cutoff, err := parseCutoffDate(payload.CutoffDate)
		if err != nil {
			return err
		}
		if cutoff.IsZero() {
			cutoff = s.now().UTC().Add(-retentionWindow)
		}
		return s.RunSweep(ctx, cutoff)
	})
}

func (s *RetentionSweeper) RunSweep(ctx context.Context, cutoffDate time.Time) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("retention sweeper is not configured")
	}
	if cutoffDate.IsZero() {
		return fmt.Errorf("cutoff date is required")
	}

	archivedAt := s.now().UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapDBError(err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	rows, err := tx.Query(ctx, `
		SELECT id
		FROM chat_session
		WHERE status = 'closed'
		  AND closed_at IS NOT NULL
		  AND closed_at < $1
		FOR UPDATE
	`, cutoffDate.UTC())
	if err != nil {
		return mapDBError(err)
	}
	sessionIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			rows.Close()
			return mapDBError(scanErr)
		}
		sessionIDs = append(sessionIDs, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return mapDBError(rowsErr)
	}
	rows.Close()

	if len(sessionIDs) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return mapDBError(err)
		}
		return nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE chat_session
		SET status = 'archived',
		    metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('archived_at', $2::timestamptz)
		WHERE id = ANY($1::uuid[])
	`, sessionIDs, archivedAt); err != nil {
		return mapDBError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE chat_message
		SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('archived_at', $2::timestamptz)
		WHERE session_id = ANY($1::uuid[])
	`, sessionIDs, archivedAt); err != nil {
		return mapDBError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE chat_artifact
		SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('archived_at', $2::timestamptz)
		WHERE session_id = ANY($1::uuid[])
	`, sessionIDs, archivedAt); err != nil {
		return mapDBError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE chat_summary
		SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('archived_at', $2::timestamptz)
		WHERE session_id = ANY($1::uuid[])
	`, sessionIDs, archivedAt); err != nil {
		return mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return mapDBError(err)
	}
	return nil
}

type ChatSummarizePayload struct {
	SessionID         uuid.UUID `json:"session_id"`
	LayerBudgetTokens int       `json:"layer_budget_tokens"`
}

type ChatSessionCleanupPayload struct {
	SessionID   uuid.UUID `json:"session_id"`
	CleanupType string    `json:"cleanup_type"`
}

type ChatRetentionSweepPayload struct {
	CutoffDate string `json:"cutoff_date"`
}

type summaryCandidate struct {
	FromSequence int64
	ToSequence   int64
	TurnCount    int
	Messages     []repo.ChatMessage
}

type turnWindow struct {
	key          string
	fromSequence int64
	toSequence   int64
}

func summarizeCandidate(messages []repo.ChatMessage, summaries []repo.ChatSummary) (*summaryCandidate, error) {
	unsummarizedMessages := filterUnsummarizedMessages(messages, summaries)
	if len(unsummarizedMessages) == 0 {
		return nil, ErrAlreadySummarized
	}
	turns := buildUnsummarizedTurnWindows(unsummarizedMessages)
	if len(turns) == 0 {
		return nil, ErrAlreadySummarized
	}

	targetMessageCount := targetMessageCount(len(unsummarizedMessages))
	if targetMessageCount <= 0 {
		return nil, ErrAlreadySummarized
	}
	boundarySequence := unsummarizedMessages[targetMessageCount-1].SequenceNumber

	toSequence := int64(0)
	turnCount := 0
	for _, turn := range turns {
		turnCount++
		if boundarySequence < turn.fromSequence || boundarySequence > turn.toSequence {
			continue
		}
		toSequence = turn.toSequence
		break
	}
	if toSequence == 0 {
		return nil, fmt.Errorf("failed to resolve summarization turn boundary")
	}

	fromSequence := turns[0].fromSequence
	if fromSequence <= 0 || toSequence < fromSequence {
		return nil, fmt.Errorf("invalid summarization range")
	}

	selectedMessages := make([]repo.ChatMessage, 0, len(unsummarizedMessages))
	for _, message := range unsummarizedMessages {
		if message.SequenceNumber < fromSequence || message.SequenceNumber > toSequence {
			continue
		}
		selectedMessages = append(selectedMessages, message)
	}
	if len(selectedMessages) == 0 {
		return nil, ErrAlreadySummarized
	}

	return &summaryCandidate{
		FromSequence: fromSequence,
		ToSequence:   toSequence,
		TurnCount:    turnCount,
		Messages:     selectedMessages,
	}, nil
}

func filterUnsummarizedMessages(messages []repo.ChatMessage, summaries []repo.ChatSummary) []repo.ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	if len(summaries) == 0 {
		items := make([]repo.ChatMessage, len(messages))
		copy(items, messages)
		return items
	}

	filtered := make([]repo.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if isSequenceCovered(message.SequenceNumber, summaries) {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func isSequenceCovered(sequence int64, summaries []repo.ChatSummary) bool {
	for _, summary := range summaries {
		if sequence >= summary.FromSequence && sequence <= summary.ToSequence {
			return true
		}
	}
	return false
}

func rangeCovered(fromSequence, toSequence int64, summaries []repo.ChatSummary) bool {
	for _, summary := range summaries {
		if summary.FromSequence <= fromSequence && summary.ToSequence >= toSequence {
			return true
		}
	}
	return false
}

func buildUnsummarizedTurnWindows(messages []repo.ChatMessage) []turnWindow {
	if len(messages) == 0 {
		return nil
	}

	items := make([]turnWindow, 0)
	var current *turnWindow
	for _, message := range messages {
		key := turnWindowKey(message)
		if current == nil || current.key != key {
			items = append(items, turnWindow{key: key, fromSequence: message.SequenceNumber, toSequence: message.SequenceNumber})
			current = &items[len(items)-1]
			continue
		}
		if message.SequenceNumber > current.toSequence {
			current.toSequence = message.SequenceNumber
		}
	}
	return items
}

func turnWindowKey(message repo.ChatMessage) string {
	if message.TurnID != nil {
		return "turn:" + message.TurnID.String()
	}
	return fmt.Sprintf("sequence:%d", message.SequenceNumber)
}

func targetMessageCount(totalMessages int) int {
	if totalMessages <= 0 {
		return 0
	}
	count := int(math.Round(float64(totalMessages) * summarizationPercentile))
	if count < 1 {
		count = 1
	}
	if count > totalMessages {
		count = totalMessages
	}
	return count
}

func estimateTokensFromChars(charCount int) int {
	if charCount <= 0 {
		return 0
	}
	return int(math.Ceil(float64(charCount) / tokenEstimatorDivisor))
}

func hasFlowNodeExecutionMetadata(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return false
	}
	value, ok := metadata["flow_node_execution_id"]
	if !ok {
		return false
	}
	flowNodeExecutionID, ok := value.(string)
	if !ok {
		return false
	}
	return strings.TrimSpace(flowNodeExecutionID) != ""
}

func parseCutoffDate(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cutoff_date %q", raw)
	}
	return parsed.UTC(), nil
}

func compactedToolResultContent(size int) string {
	return fmt.Sprintf("[tool_result compacted — %d bytes]", size)
}

func compactToolResultPayload(content string, metadata json.RawMessage) (string, json.RawMessage) {
	return compactedToolResultContent(len(content)), withCompactedMetadata(metadata)
}

func withCompactedMetadata(raw json.RawMessage) json.RawMessage {
	metadata := map[string]any{}
	if len(raw) > 0 && json.Valid(raw) {
		_ = json.Unmarshal(raw, &metadata)
	}
	metadata["compacted"] = true
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return json.RawMessage(`{"compacted":true}`)
	}
	return encoded
}

func defaultSummarizationPrompt() string {
	return strings.TrimSpace(`Summarize the selected conversation span.
Preserve ALL file paths, URLs, artifact IDs, code blocks, and tool call/result pairs exactly as they appear. Do not paraphrase, abbreviate, or omit these elements. If in doubt, copy verbatim.`)
}

func defaultConsolidationPrompt() string {
	return strings.TrimSpace(`Consolidate the provided summaries into one comprehensive summary while preserving critical details.
Preserve ALL file paths, URLs, artifact IDs, code blocks, and tool call/result pairs exactly as they appear. Do not paraphrase, abbreviate, or omit these elements. If in doubt, copy verbatim.`)
}

func nextCleanupRunAfter(now time.Time) time.Time {
	base := now.UTC()
	nextDay := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, time.UTC).Add(24 * time.Hour)
	return time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), 3, 0, 0, 0, time.UTC)
}
