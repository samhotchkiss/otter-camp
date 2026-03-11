package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/metrics"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
)

var (
	ErrActiveSyncSessionExists = errors.New("active sync session already exists")
	ErrTurnInProgress          = errors.New("turn is in progress")
	ErrNoActiveTurn            = errors.New("no active turn")
	ErrAlreadyParticipant      = errors.New("participant already active in session")
	ErrDuplicateReaction       = errors.New("duplicate reaction")
	ErrMessageNotEditable      = errors.New("message is not editable")
	ErrInvalidStatusTransition = errors.New("invalid status transition")
	ErrSessionClosed           = errors.New("session is closed")
	ErrProjectArchived         = errors.New("project is archived")
	ErrForbidden               = errors.New("forbidden")
)

const (
	defaultListLimit   = 50
	maxListLimit       = 200
	chatJobPriority    = 50
	summarizePriority  = 60
	workerHealthWindow = 2 * time.Minute
)

var messageStatusTransitions = map[string]map[string]struct{}{
	"pending": {
		"streaming": {},
		"final":     {},
		"failed":    {},
	},
	"streaming": {
		"final":  {},
		"failed": {},
	},
	"final":    {},
	"failed":   {},
	"redacted": {},
}

type ChatSession = repo.ChatSession
type ChatParticipant = repo.ChatParticipant
type ChatMessage = repo.ChatMessage
type ChatTurn = repo.ChatTurn
type ChatMessageReaction = repo.ChatMessageReaction

type CreateSessionInput struct {
	OrganizationID uuid.UUID
	ScopeType      string
	ScopeID        uuid.UUID
	Mode           string
	Title          *string
	Metadata       json.RawMessage
}

type SessionFilter struct {
	OrganizationID  uuid.UUID
	ScopeType       string
	ScopeID         *uuid.UUID
	Status          string
	Mode            string
	Limit           int
	CursorSessionID *uuid.UUID
}

type AppendMessageInput struct {
	SessionID     uuid.UUID
	TurnID        *uuid.UUID
	AuthorType    *string
	AuthorID      *uuid.UUID
	Role          string
	Content       string
	ContentFormat string
	ToolCallID    *string
	Metadata      json.RawMessage
}

type MessageFilter struct {
	Status         string
	Limit          int
	CursorSequence *int64
	BeforeSequence *int64
	AfterSequence  *int64
}

type ChatService interface {
	CreateSession(ctx context.Context, input CreateSessionInput) (*ChatSession, error)
	GetSession(ctx context.Context, id uuid.UUID) (*ChatSession, error)
	ListSessions(ctx context.Context, filter SessionFilter) ([]*ChatSession, error)
	SwitchMode(ctx context.Context, sessionID uuid.UUID, newMode string) error
	CloseSession(ctx context.Context, sessionID uuid.UUID) error
	GetOrCreateNodeSession(ctx context.Context, flowNodeExecutionID, agentID uuid.UUID) (*ChatSession, error)

	AddParticipant(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*ChatParticipant, error)
	RemoveParticipant(ctx context.Context, sessionID, participantID uuid.UUID) error
	ListParticipants(ctx context.Context, sessionID uuid.UUID) ([]*ChatParticipant, error)
	UpdateNotificationPreference(ctx context.Context, sessionID, userID uuid.UUID, preference string) error

	AppendMessage(ctx context.Context, input AppendMessageInput) (*ChatMessage, error)
	UpdateMessageStatus(ctx context.Context, messageID uuid.UUID, newStatus, errorMsg string) error
	RedactMessage(ctx context.Context, messageID uuid.UUID) error
	EditQueuedMessage(ctx context.Context, messageID uuid.UUID, newContent string) error
	GetMessage(ctx context.Context, messageID uuid.UUID) (*ChatMessage, error)
	ListMessages(ctx context.Context, sessionID uuid.UUID, filter MessageFilter) ([]*ChatMessage, error)

	CreateTurn(ctx context.Context, sessionID, agentID uuid.UUID) (*ChatTurn, error)
	StartTurn(ctx context.Context, turnID uuid.UUID) error
	CompleteTurn(ctx context.Context, turnID uuid.UUID) error
	CancelTurn(ctx context.Context, turnID uuid.UUID, reason string) error
	FailTurn(ctx context.Context, turnID uuid.UUID, errorMsg string) error
	GetTurn(ctx context.Context, turnID uuid.UUID) (*ChatTurn, error)
	ListTurns(ctx context.Context, sessionID uuid.UUID) ([]*ChatTurn, error)

	CancelCurrentTurn(ctx context.Context, sessionID uuid.UUID) error
	CancelAndQueueNew(ctx context.Context, sessionID uuid.UUID, newMessage string) error
	SteerTurn(ctx context.Context, sessionID, messageID uuid.UUID, steerContent string) error

	AddReaction(ctx context.Context, messageID uuid.UUID, reactorType string, reactorID uuid.UUID, emoji string) (*ChatMessageReaction, error)
	RemoveReaction(ctx context.Context, reactionID uuid.UUID) error
	ListReactions(ctx context.Context, messageID uuid.UUID) ([]*ChatMessageReaction, error)
}

type chatSessionRepository interface {
	Create(ctx context.Context, session repo.ChatSession) (repo.ChatSession, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
	GetByScopeAndMode(ctx context.Context, scopeType string, scopeID uuid.UUID, mode string) (*repo.ChatSession, error)
	ListByOrg(ctx context.Context, organizationID uuid.UUID) ([]repo.ChatSession, error)
	UpdateMode(ctx context.Context, id uuid.UUID, mode string) (repo.ChatSession, error)
	Close(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
	UpdateCurrentTurn(ctx context.Context, id uuid.UUID, currentTurnID *uuid.UUID) (repo.ChatSession, error)
	IncrementCounts(ctx context.Context, id uuid.UUID, turnDelta, messageDelta int) (repo.ChatSession, error)
}

type chatParticipantRepository interface {
	Create(ctx context.Context, participant repo.ChatParticipant) (repo.ChatParticipant, error)
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatParticipant, error)
	UpdateNotificationPreference(ctx context.Context, id uuid.UUID, preference string) (repo.ChatParticipant, error)
	Remove(ctx context.Context, id uuid.UUID) (repo.ChatParticipant, error)
}

type chatMessageRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error)
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage string) (repo.ChatMessage, error)
	UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ChatMessage, error)
	UpdateContent(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error)
	Redact(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error)
}

type chatTurnRepository interface {
	Create(ctx context.Context, turn repo.ChatTurn) (repo.ChatTurn, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatTurn, error)
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatTurn, error)
	SetStarted(ctx context.Context, id uuid.UUID, startedAt time.Time) (repo.ChatTurn, error)
	SetCompleted(ctx context.Context, id uuid.UUID, completedAt time.Time, durationMS int) (repo.ChatTurn, error)
	SetCancelled(ctx context.Context, id uuid.UUID, cancelRequestedAt time.Time, completedAt time.Time) (repo.ChatTurn, error)
	SetFailed(ctx context.Context, id uuid.UUID, errorMessage string, completedAt time.Time) (repo.ChatTurn, error)
}

type chatReactionRepository interface {
	Create(ctx context.Context, reaction repo.ChatMessageReaction) (repo.ChatMessageReaction, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatMessageReaction, error)
	ListByMessage(ctx context.Context, messageID uuid.UUID) ([]repo.ChatMessageReaction, error)
	DeleteByID(ctx context.Context, id uuid.UUID) error
}

type projectTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type projectRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type humanUserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
}

type agentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type eventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type jobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
	CancelGroup(ctx context.Context, tx pgx.Tx, groupKey, reason string) (int64, error)
}

type Options struct {
	Pool *pgxpool.Pool

	Sessions     chatSessionRepository
	Participants chatParticipantRepository
	Messages     chatMessageRepository
	Turns        chatTurnRepository
	Reactions    chatReactionRepository
	Tasks        projectTaskRepository
	Projects     projectRepository
	Users        humanUserRepository
	Agents       agentRepository

	Events   eventPublisher
	Enqueuer jobEnqueuer
	Clock    clock.Clock
}

type service struct {
	pool *pgxpool.Pool

	sessions     chatSessionRepository
	participants chatParticipantRepository
	messages     chatMessageRepository
	turns        chatTurnRepository
	reactions    chatReactionRepository
	tasks        projectTaskRepository
	projects     projectRepository
	users        humanUserRepository
	agents       agentRepository

	events   eventPublisher
	enqueuer jobEnqueuer
	clock    clock.Clock
}

func NewService(opts Options) (ChatService, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("chat service requires a database pool")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("chat service requires an event bus")
	}

	svc := &service{
		pool:   opts.Pool,
		events: opts.Events,
		clock:  opts.Clock,
	}
	if svc.clock == nil {
		svc.clock = clock.Real{}
	}
	if opts.Sessions != nil {
		svc.sessions = opts.Sessions
	} else {
		svc.sessions = repo.NewChatSessionRepo(opts.Pool)
	}
	if opts.Participants != nil {
		svc.participants = opts.Participants
	} else {
		svc.participants = repo.NewChatParticipantRepo(opts.Pool)
	}
	if opts.Messages != nil {
		svc.messages = opts.Messages
	} else {
		svc.messages = repo.NewChatMessageRepo(opts.Pool)
	}
	if opts.Turns != nil {
		svc.turns = opts.Turns
	} else {
		svc.turns = repo.NewChatTurnRepo(opts.Pool)
	}
	if opts.Reactions != nil {
		svc.reactions = opts.Reactions
	} else {
		svc.reactions = repo.NewChatMessageReactionRepo(opts.Pool)
	}
	if opts.Tasks != nil {
		svc.tasks = opts.Tasks
	} else {
		svc.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if opts.Projects != nil {
		svc.projects = opts.Projects
	} else {
		svc.projects = repo.NewProjectRepo(opts.Pool)
	}
	if opts.Users != nil {
		svc.users = opts.Users
	} else {
		svc.users = repo.NewHumanUserRepo(opts.Pool)
	}
	if opts.Agents != nil {
		svc.agents = opts.Agents
	} else {
		svc.agents = repo.NewAgentRepo(opts.Pool)
	}
	if opts.Enqueuer != nil {
		svc.enqueuer = opts.Enqueuer
	} else {
		svc.enqueuer = jobqueue.New(opts.Pool, nil, jobqueue.Config{})
	}

	return svc, nil
}

func shouldReuseCanonicalSession(scopeType, mode string) bool {
	return strings.EqualFold(strings.TrimSpace(scopeType), "project") && strings.EqualFold(strings.TrimSpace(mode), "async")
}

func canonicalSessionLess(left, right repo.ChatSession) bool {
	if !left.CreatedAt.IsZero() && !right.CreatedAt.IsZero() && !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	if left.ID != right.ID {
		return left.ID.String() < right.ID.String()
	}
	return strings.TrimSpace(derefStr(left.Title)) < strings.TrimSpace(derefStr(right.Title))
}

func derefStr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *service) findReusableCanonicalSession(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, mode string) (*repo.ChatSession, error) {
	if s == nil || s.sessions == nil || !shouldReuseCanonicalSession(scopeType, mode) {
		return nil, nil
	}
	sessions, err := s.sessions.ListByOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}

	var reusable *repo.ChatSession
	for i := range sessions {
		session := sessions[i]
		if session.OrganizationID != organizationID || session.ScopeID != scopeID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.ScopeType), strings.TrimSpace(scopeType)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.Mode), strings.TrimSpace(mode)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
			continue
		}
		if reusable == nil || canonicalSessionLess(session, *reusable) {
			candidate := session
			reusable = &candidate
		}
	}
	return reusable, nil
}

func (s *service) CreateSession(ctx context.Context, input CreateSessionInput) (*ChatSession, error) {
	orgID, principal, err := s.resolveOrg(ctx, input.OrganizationID)
	if err != nil {
		return nil, err
	}
	if input.ScopeID == uuid.Nil {
		return nil, fmt.Errorf("scope_id is required")
	}

	scopeType := normalizeScopeType(input.ScopeType)
	if scopeType == "" {
		return nil, fmt.Errorf("invalid scope_type")
	}
	mode := normalizeSessionMode(input.Mode)
	if mode == "" {
		return nil, fmt.Errorf("invalid mode")
	}
	if err := s.ensureProjectActiveForScope(ctx, scopeType, input.ScopeID, orgID); err != nil {
		return nil, err
	}

	if mode == "sync" {
		existing, getErr := s.sessions.GetByScopeAndMode(ctx, scopeType, input.ScopeID, mode)
		if getErr != nil {
			return nil, getErr
		}
		if existing != nil && existing.Status == "active" && existing.OrganizationID == orgID {
			return nil, ErrActiveSyncSessionExists
		}
	}
	if reusable, reuseErr := s.findReusableCanonicalSession(ctx, orgID, scopeType, input.ScopeID, mode); reuseErr != nil {
		return nil, reuseErr
	} else if reusable != nil {
		return reusable, nil
	}

	if scopeType == "project_task" {
		taskRecord, taskErr := s.tasks.GetByID(ctx, input.ScopeID)
		if errors.Is(taskErr, repo.ErrNotFound) {
			return nil, repo.ErrNotFound
		}
		if taskErr != nil {
			return nil, taskErr
		}
		if taskRecord.OrganizationID != orgID {
			return nil, repo.ErrNotFound
		}
	}
	if mode == "async" {
		if err := s.ensureProjectNotPausedForScope(ctx, scopeType, input.ScopeID, orgID); err != nil {
			return nil, err
		}
	}
	if scopeType == "project_task" && mode == "async" {
		reusable, err := s.reuseCanonicalTaskAsyncSession(ctx, orgID, input.ScopeID)
		if err != nil {
			return nil, err
		}
		if reusable != nil {
			return reusable, nil
		}
	}

	createdByType := "system"
	createdByID := uuid.Nil
	if principal != nil {
		createdByType = "human_user"
		createdByID = principal.UserID
	}

	session, err := s.sessions.Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      scopeType,
		ScopeID:        input.ScopeID,
		Mode:           mode,
		Status:         "active",
		Title:          trimStringPointer(input.Title),
		CreatedByType:  createdByType,
		CreatedByID:    createdByID,
		Metadata:       normalizeJSON(input.Metadata, json.RawMessage(`{}`)),
	})
	if err != nil {
		return nil, err
	}

	if err := s.publishEvent(ctx, orgID, "chat.session.created", createdByType, uuidPtr(createdByID), map[string]any{
		"session_id": session.ID,
		"scope_type": session.ScopeType,
		"scope_id":   session.ScopeID,
		"mode":       session.Mode,
	}); err != nil {
		return nil, err
	}

	return &session, nil
}

func (s *service) reuseCanonicalTaskAsyncSession(ctx context.Context, organizationID, taskID uuid.UUID) (*ChatSession, error) {
	sessions, err := s.sessions.ListByOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}

	blanks := make([]repo.ChatSession, 0, 2)
	var canonical *repo.ChatSession
	for _, session := range sessions {
		if session.OrganizationID != organizationID || session.ScopeID != taskID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.Mode), "async") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(session.Status), "active") {
			continue
		}
		if isBlankTaskAsyncSession(session) {
			blanks = append(blanks, session)
			continue
		}
		if canonical == nil || taskAsyncSessionMoreRecent(session, *canonical) {
			candidate := session
			canonical = &candidate
		}
	}
	if canonical != nil {
		for _, duplicate := range blanks {
			if _, err := s.sessions.Close(ctx, duplicate.ID); err != nil && !errors.Is(err, repo.ErrNotFound) {
				return nil, err
			}
		}
		reusable := ChatSession(*canonical)
		return &reusable, nil
	}
	if len(blanks) == 0 {
		return nil, nil
	}
	sort.Slice(blanks, func(i, j int) bool {
		return taskAsyncSessionMoreRecent(blanks[i], blanks[j])
	})
	for _, duplicate := range blanks[1:] {
		if _, err := s.sessions.Close(ctx, duplicate.ID); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return nil, err
		}
	}
	reusable := ChatSession(blanks[0])
	return &reusable, nil
}

func isBlankTaskAsyncSession(session repo.ChatSession) bool {
	return session.CurrentTurnID == nil &&
		session.TurnCount == 0 &&
		session.MessageCount == 0 &&
		session.LastMessageAt == nil
}

func taskAsyncSessionMoreRecent(left, right repo.ChatSession) bool {
	switch {
	case left.LastMessageAt != nil && right.LastMessageAt != nil && !left.LastMessageAt.Equal(*right.LastMessageAt):
		return left.LastMessageAt.After(*right.LastMessageAt)
	case left.LastMessageAt != nil && right.LastMessageAt == nil:
		return true
	case left.LastMessageAt == nil && right.LastMessageAt != nil:
		return false
	case !left.CreatedAt.IsZero() && !right.CreatedAt.IsZero() && !left.CreatedAt.Equal(right.CreatedAt):
		return left.CreatedAt.After(right.CreatedAt)
	default:
		return left.ID.String() > right.ID.String()
	}
}

func (s *service) GetSession(ctx context.Context, id uuid.UUID) (*ChatSession, error) {
	session, err := s.sessions.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !s.orgVisibleInContext(ctx, session.OrganizationID) {
		return nil, repo.ErrNotFound
	}
	return &session, nil
}

func (s *service) ListSessions(ctx context.Context, filter SessionFilter) ([]*ChatSession, error) {
	orgID, _, err := s.resolveOrg(ctx, filter.OrganizationID)
	if err != nil {
		return nil, err
	}

	sessions, err := s.sessions.ListByOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}

	scopeType := normalizeScopeType(filter.ScopeType)
	status := normalizeSessionStatus(filter.Status)
	mode := normalizeSessionMode(filter.Mode)

	items := make([]repo.ChatSession, 0, len(sessions))
	for _, item := range sessions {
		if scopeType != "" && item.ScopeType != scopeType {
			continue
		}
		if filter.ScopeID != nil && item.ScopeID != *filter.ScopeID {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		if mode != "" && item.Mode != mode {
			continue
		}
		if err := s.ensureProjectActiveForScope(ctx, item.ScopeType, item.ScopeID, item.OrganizationID); err != nil {
			if errors.Is(err, ErrProjectArchived) {
				continue
			}
			return nil, err
		}
		items = append(items, item)
	}

	if filter.CursorSessionID != nil {
		cursorSeen := false
		cropped := make([]repo.ChatSession, 0, len(items))
		for _, item := range items {
			if cursorSeen {
				cropped = append(cropped, item)
				continue
			}
			if item.ID == *filter.CursorSessionID {
				cursorSeen = true
			}
		}
		items = cropped
	}

	limit := normalizeLimit(filter.Limit)
	if len(items) > limit {
		items = items[:limit]
	}

	result := make([]*ChatSession, 0, len(items))
	for i := range items {
		copyItem := items[i]
		result = append(result, &copyItem)
	}
	return result, nil
}

func (s *service) SwitchMode(ctx context.Context, sessionID uuid.UUID, newMode string) error {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.Status != "active" {
		return ErrSessionClosed
	}

	target := normalizeSessionMode(newMode)
	if target == "" {
		return fmt.Errorf("invalid mode")
	}
	if session.Mode == target {
		return nil
	}

	switch {
	case session.Mode == "sync" && target == "async":
		// always allowed
	case session.Mode == "async" && target == "sync":
		inProgress, ipErr := s.hasInProgressTurn(ctx, session.ID)
		if ipErr != nil {
			return ipErr
		}
		if inProgress {
			return ErrTurnInProgress
		}
	default:
		return ErrInvalidStatusTransition
	}

	if _, err := s.sessions.UpdateMode(ctx, session.ID, target); err != nil {
		return err
	}

	actorType, actorID := actorFromContext(ctx)
	return s.publishEvent(ctx, session.OrganizationID, "chat.session.mode_changed", actorType, actorID, map[string]any{
		"session_id": session.ID,
		"old_mode":   session.Mode,
		"new_mode":   target,
	})
}

func (s *service) CloseSession(ctx context.Context, sessionID uuid.UUID) error {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.Status != "active" {
		return ErrSessionClosed
	}

	inProgress, err := s.hasInProgressTurn(ctx, session.ID)
	if err != nil {
		return err
	}
	if inProgress {
		return ErrTurnInProgress
	}

	closed, err := s.sessions.Close(ctx, session.ID)
	if err != nil {
		return err
	}

	actorType, actorID := actorFromContext(ctx)
	if err := s.publishEvent(ctx, session.OrganizationID, "chat.session.closed", actorType, actorID, map[string]any{
		"session_id": closed.ID,
		"closed_at":  closed.ClosedAt,
	}); err != nil {
		return err
	}

	if _, err := s.enqueuer.Enqueue(ctx, nil, ChatSummarizeJobType, summarizePriority, ChatSummarizePayload{
		SessionID:         session.ID,
		LayerBudgetTokens: 0,
	}, nil); err != nil {
		return err
	}

	runAfter := nextCleanupRunAfter(s.clock.Now().UTC())
	cleanupTypes := []string{
		CleanupTypeEphemeralPurge,
		CleanupTypeToolCompaction,
		CleanupTypeSummaryConsolidate,
	}
	for _, cleanupType := range cleanupTypes {
		if _, err := s.enqueuer.Enqueue(ctx, nil, ChatSessionCleanupJobType, chatJobPriority, ChatSessionCleanupPayload{
			SessionID:   session.ID,
			CleanupType: cleanupType,
		}, &runAfter); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) GetOrCreateNodeSession(ctx context.Context, flowNodeExecutionID, agentID uuid.UUID) (*ChatSession, error) {
	if flowNodeExecutionID == uuid.Nil {
		return nil, fmt.Errorf("flow_node_execution_id is required")
	}
	if agentID == uuid.Nil {
		return nil, fmt.Errorf("agent_id is required")
	}

	agentRecord, err := s.agents.GetByID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if !s.orgVisibleInContext(ctx, agentRecord.OrganizationID) {
		return nil, repo.ErrNotFound
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, flowNodeExecutionID.String()); err != nil {
		return nil, mapDBError(err)
	}

	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM chat_session
		WHERE organization_id = $1
		  AND mode = 'async'
		  AND status = 'active'
		  AND metadata->>'flow_node_execution_id' = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, agentRecord.OrganizationID, flowNodeExecutionID.String()).Scan(&existingID)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx, `
			UPDATE flow_node_execution
			SET session_id = $2
			WHERE id = $1
			  AND (session_id IS NULL OR session_id = $2)
		`, flowNodeExecutionID, existingID); err != nil {
			return nil, mapDBError(err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		session, getErr := s.sessions.GetByID(ctx, existingID)
		if getErr != nil {
			return nil, getErr
		}
		return &session, nil
	case errors.Is(err, pgx.ErrNoRows):
		// continue
	default:
		return nil, mapDBError(err)
	}

	var taskID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT fne.task_id
		FROM flow_node_execution fne
		JOIN project_task pt ON pt.id = fne.task_id
		WHERE fne.id = $1
		  AND pt.organization_id = $2
	`, flowNodeExecutionID, agentRecord.OrganizationID).Scan(&taskID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repo.ErrNotFound
		}
		return nil, mapDBError(err)
	}
	taskRecord, err := s.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureProjectNotPausedByID(ctx, taskRecord.ProjectID, agentRecord.OrganizationID); err != nil {
		return nil, err
	}

	metadata := normalizeJSON(mustJSON(map[string]any{"flow_node_execution_id": flowNodeExecutionID.String()}), json.RawMessage(`{}`))
	var createdID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id,
			scope_type,
			scope_id,
			mode,
			status,
			created_by_type,
			created_by_id,
			metadata
		)
		VALUES ($1, 'project_task', $2, 'async', 'active', 'agent', $3, $4::jsonb)
		RETURNING id
	`, agentRecord.OrganizationID, taskID, agentID, metadata).Scan(&createdID); err != nil {
		return nil, mapDBError(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE flow_node_execution
		SET session_id = $2
		WHERE id = $1
	`, flowNodeExecutionID, createdID); err != nil {
		return nil, mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	created, err := s.sessions.GetByID(ctx, createdID)
	if err != nil {
		return nil, err
	}

	if err := s.publishEvent(ctx, agentRecord.OrganizationID, "chat.session.created", "agent", &agentID, map[string]any{
		"session_id": created.ID,
		"scope_type": created.ScopeType,
		"scope_id":   created.ScopeID,
		"mode":       created.Mode,
	}); err != nil {
		return nil, err
	}

	return &created, nil
}

func (s *service) AddParticipant(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*ChatParticipant, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != "active" {
		return nil, ErrSessionClosed
	}
	if participantID == uuid.Nil {
		return nil, fmt.Errorf("participant_id is required")
	}

	normalizedType := normalizeParticipantType(participantType)
	if normalizedType == "" {
		return nil, fmt.Errorf("invalid participant_type")
	}
	if err := s.validateParticipantInOrg(ctx, normalizedType, participantID, session.OrganizationID); err != nil {
		return nil, err
	}

	participant, err := s.participants.Create(ctx, repo.ChatParticipant{
		SessionID:              session.ID,
		ParticipantType:        normalizedType,
		ParticipantID:          participantID,
		Role:                   normalizeParticipantRole(role),
		NotificationPreference: "all",
	})
	if errors.Is(err, repo.ErrConflict) {
		return nil, ErrAlreadyParticipant
	}
	if err != nil {
		return nil, err
	}
	return &participant, nil
}

func (s *service) RemoveParticipant(ctx context.Context, sessionID, participantID uuid.UUID) error {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	participants, err := s.participants.ListBySession(ctx, session.ID)
	if err != nil {
		return err
	}

	for _, participant := range participants {
		if participant.ParticipantID != participantID {
			continue
		}
		_, err := s.participants.Remove(ctx, participant.ID)
		return err
	}

	return repo.ErrNotFound
}

func (s *service) ListParticipants(ctx context.Context, sessionID uuid.UUID) ([]*ChatParticipant, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	participants, err := s.participants.ListBySession(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	result := make([]*ChatParticipant, 0, len(participants))
	for i := range participants {
		copyItem := participants[i]
		result = append(result, &copyItem)
	}
	return result, nil
}

func (s *service) UpdateNotificationPreference(ctx context.Context, sessionID, userID uuid.UUID, preference string) error {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if !isNotificationPreference(preference) {
		return fmt.Errorf("invalid notification_preference")
	}

	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return ErrForbidden
	}
	if principal.UserID != userID {
		return ErrForbidden
	}
	if principal.OrganizationID != session.OrganizationID {
		return repo.ErrNotFound
	}

	participants, err := s.participants.ListBySession(ctx, session.ID)
	if err != nil {
		return err
	}
	for _, participant := range participants {
		if participant.ParticipantType != "human_user" {
			continue
		}
		if participant.ParticipantID != userID {
			continue
		}
		_, updateErr := s.participants.UpdateNotificationPreference(ctx, participant.ID, preference)
		return updateErr
	}

	return repo.ErrNotFound
}

func (s *service) AppendMessage(ctx context.Context, input AppendMessageInput) (*ChatMessage, error) {
	session, err := s.GetSession(ctx, input.SessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != "active" {
		return nil, ErrSessionClosed
	}

	message, err := s.appendMessageAndUpdateSession(ctx, *session, input)
	if err != nil {
		return nil, err
	}

	actorType, actorID := actorFromContext(ctx)
	if input.AuthorType != nil {
		actorType = normalizeActorType(*input.AuthorType)
	}
	if input.AuthorID != nil {
		actorID = input.AuthorID
	}

	if err := s.publishEvent(ctx, session.OrganizationID, "chat.message.created", actorType, actorID, map[string]any{
		"session_id":      session.ID,
		"message_id":      message.ID,
		"sequence_number": message.SequenceNumber,
		"status":          message.Status,
	}); err != nil {
		return nil, err
	}
	if message.Role == "user" && !isSteerMetadata(message.Metadata) {
		if err := s.publishEvent(ctx, session.OrganizationID, "chat.message.user_sent", actorType, actorID, map[string]any{
			"session_id":      session.ID,
			"message_id":      message.ID,
			"sequence_number": message.SequenceNumber,
			"status":          message.Status,
		}); err != nil {
			return nil, err
		}
		if err := s.publishEvent(ctx, session.OrganizationID, "chat.message.finalized", actorType, actorID, map[string]any{
			"session_id": session.ID,
			"message_id": message.ID,
			"role":       "user",
			"content":    message.Content,
		}); err != nil {
			return nil, err
		}
		go s.monitorTurnResponse(session.OrganizationID, session.ID, message.CreatedAt)
	}

	return message, nil
}

func (s *service) monitorTurnResponse(orgID, sessionID uuid.UUID, msgAt time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	time.Sleep(20 * time.Second)

	state, err := s.loadTurnResponseMonitorState(ctx, sessionID, msgAt)
	if err != nil {
		return
	}
	msg, ok := state.warningMessage()
	if !ok {
		return
	}

	_ = s.publishEvent(ctx, orgID, "worker.unresponsive", "system", nil, map[string]any{
		"session_id": sessionID,
		"message":    msg,
	})
}

type turnResponseMonitorState struct {
	hasResponse          bool
	pendingAgentTurn     bool
	claimedAgentTurn     bool
	recentWorkerActivity bool
}

func (s *service) loadTurnResponseMonitorState(ctx context.Context, sessionID uuid.UUID, msgAt time.Time) (turnResponseMonitorState, error) {
	var state turnResponseMonitorState
	recentSince := s.clock.Now().UTC().Add(-workerHealthWindow)
	err := s.pool.QueryRow(ctx, `
		SELECT
			EXISTS(
				SELECT 1
				FROM chat_message
				WHERE session_id = $1
				  AND role = 'assistant'
				  AND created_at > $2
			),
			EXISTS(
				SELECT 1
				FROM job_queue
				WHERE job_type = 'agent_turn'
				  AND status = 'pending'
				  AND payload->>'session_id' = $3
			),
			EXISTS(
				SELECT 1
				FROM job_queue
				WHERE job_type = 'agent_turn'
				  AND status = 'claimed'
				  AND payload->>'session_id' = $3
			),
			EXISTS(
				SELECT 1
				FROM job_queue
				WHERE (status = 'claimed' AND claimed_at >= $4)
				   OR (job_type = $5 AND status = 'done' AND updated_at >= $4)
			) OR EXISTS(
				SELECT 1
				FROM run_event
				WHERE event_type = 'heartbeat'
				  AND created_at >= $4
			)
	`, sessionID, msgAt, sessionID.String(), recentSince, scheduling.ScheduleTickJobType).Scan(
		&state.hasResponse,
		&state.pendingAgentTurn,
		&state.claimedAgentTurn,
		&state.recentWorkerActivity,
	)
	if err != nil {
		return turnResponseMonitorState{}, err
	}
	return state, nil
}

func (s turnResponseMonitorState) warningMessage() (string, bool) {
	if s.hasResponse || s.claimedAgentTurn || s.recentWorkerActivity {
		return "", false
	}
	if s.pendingAgentTurn {
		return "Worker appears offline — job queued but not processing. Run: ottercamp worker", true
	}
	return "Worker appears offline — check that `ottercamp worker` is running.", true
}

func (s *service) ensureProjectNotPausedForSession(ctx context.Context, session *ChatSession) error {
	if session == nil {
		return nil
	}
	return s.ensureProjectNotPausedForScope(ctx, session.ScopeType, session.ScopeID, session.OrganizationID)
}

func (s *service) ensureProjectActiveForScope(ctx context.Context, scopeType string, scopeID, organizationID uuid.UUID) error {
	projectID, err := s.projectIDForScope(ctx, scopeType, scopeID, organizationID)
	if err != nil || projectID == nil || *projectID == uuid.Nil {
		return err
	}
	return s.ensureProjectActiveByID(ctx, *projectID, organizationID)
}

func (s *service) ensureProjectNotPausedForScope(ctx context.Context, scopeType string, scopeID, organizationID uuid.UUID) error {
	projectID, err := s.projectIDForScope(ctx, scopeType, scopeID, organizationID)
	if err != nil || projectID == nil || *projectID == uuid.Nil {
		return err
	}
	return s.ensureProjectNotPausedByID(ctx, *projectID, organizationID)
}

func (s *service) projectIDForScope(ctx context.Context, scopeType string, scopeID, organizationID uuid.UUID) (*uuid.UUID, error) {
	switch normalizeScopeType(scopeType) {
	case "project":
		projectID := scopeID
		return &projectID, nil
	case "project_task":
		taskRecord, err := s.tasks.GetByID(ctx, scopeID)
		if err != nil {
			return nil, err
		}
		if taskRecord.OrganizationID != organizationID {
			return nil, repo.ErrNotFound
		}
		projectID := taskRecord.ProjectID
		return &projectID, nil
	default:
		return nil, nil
	}
}

func (s *service) ensureProjectNotPausedByID(ctx context.Context, projectID, organizationID uuid.UUID) error {
	if err := s.ensureProjectActiveByID(ctx, projectID, organizationID); err != nil {
		return err
	}
	if s.projects == nil || projectID == uuid.Nil {
		return nil
	}
	projectRecord, err := s.projects.GetByID(ctx, projectID)
	if err != nil {
		return err
	}
	pauseState := projectpause.Parse(projectRecord.Settings)
	if !pauseState.IsPaused {
		return nil
	}
	return projectpause.NewError(pauseState.Reason)
}

func (s *service) ensureProjectActiveByID(ctx context.Context, projectID, organizationID uuid.UUID) error {
	if s.projects == nil || projectID == uuid.Nil {
		return nil
	}
	projectRecord, err := s.projects.GetByID(ctx, projectID)
	if err != nil {
		return err
	}
	if projectRecord.OrganizationID != organizationID {
		return repo.ErrNotFound
	}
	if strings.EqualFold(strings.TrimSpace(projectRecord.Status), "archived") {
		return ErrProjectArchived
	}
	return nil
}

func (s *service) UpdateMessageStatus(ctx context.Context, messageID uuid.UUID, newStatus, errorMsg string) error {
	message, err := s.GetMessage(ctx, messageID)
	if err != nil {
		return err
	}

	current := normalizeMessageStatus(message.Status)
	target := normalizeMessageStatus(newStatus)
	if target == "" {
		return ErrInvalidStatusTransition
	}
	if !isMessageTransitionAllowed(current, target) {
		return ErrInvalidStatusTransition
	}

	_, err = s.messages.UpdateStatus(ctx, message.ID, target, strings.TrimSpace(errorMsg))
	return err
}

func (s *service) RedactMessage(ctx context.Context, messageID uuid.UUID) error {
	message, err := s.GetMessage(ctx, messageID)
	if err != nil {
		return err
	}
	session, err := s.GetSession(ctx, message.SessionID)
	if err != nil {
		return err
	}

	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return ErrForbidden
	}
	isAuthor := message.AuthorType != nil && *message.AuthorType == "human_user" && message.AuthorID != nil && *message.AuthorID == principal.UserID
	isSessionOwner := strings.EqualFold(session.CreatedByType, "human_user") && session.CreatedByID == principal.UserID
	if !isSessionOwner && !isAuthor {
		return ErrForbidden
	}

	if _, err := s.messages.Redact(ctx, message.ID); err != nil {
		return err
	}

	actorType, actorID := actorFromContext(ctx)
	return s.publishEvent(ctx, session.OrganizationID, "chat.message.redacted", actorType, actorID, map[string]any{
		"session_id": session.ID,
		"message_id": message.ID,
	})
}

func (s *service) EditQueuedMessage(ctx context.Context, messageID uuid.UUID, newContent string) error {
	message, err := s.GetMessage(ctx, messageID)
	if err != nil {
		return err
	}
	if normalizeMessageStatus(message.Status) != "pending" {
		return ErrMessageNotEditable
	}
	if message.AuthorType == nil || *message.AuthorType != "human_user" {
		return ErrMessageNotEditable
	}

	if principal, ok := middleware.PrincipalFromContext(ctx); ok {
		if message.AuthorID == nil || *message.AuthorID != principal.UserID {
			return ErrMessageNotEditable
		}
	}

	if _, err := s.messages.UpdateContent(ctx, message.ID, newContent); errors.Is(err, repo.ErrMessageContentImmutable) {
		return ErrMessageNotEditable
	} else {
		return err
	}
}

func (s *service) GetMessage(ctx context.Context, messageID uuid.UUID) (*ChatMessage, error) {
	message, err := s.messages.GetByID(ctx, messageID)
	if err != nil {
		return nil, err
	}
	session, err := s.sessions.GetByID(ctx, message.SessionID)
	if err != nil {
		return nil, err
	}
	if !s.orgVisibleInContext(ctx, session.OrganizationID) {
		return nil, repo.ErrNotFound
	}
	return &message, nil
}

func (s *service) ListMessages(ctx context.Context, sessionID uuid.UUID, filter MessageFilter) ([]*ChatMessage, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages, err := s.messages.ListBySession(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	status := normalizeMessageStatus(filter.Status)
	filtered := make([]repo.ChatMessage, 0, len(messages))
	for _, message := range messages {
		if status != "" && normalizeMessageStatus(message.Status) != status {
			continue
		}
		if filter.BeforeSequence != nil && message.SequenceNumber >= *filter.BeforeSequence {
			continue
		}
		if filter.AfterSequence != nil && message.SequenceNumber <= *filter.AfterSequence {
			continue
		}
		if filter.CursorSequence != nil && message.SequenceNumber <= *filter.CursorSequence {
			continue
		}
		filtered = append(filtered, message)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].SequenceNumber < filtered[j].SequenceNumber
	})

	limit := normalizeLimit(filter.Limit)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	result := make([]*ChatMessage, 0, len(filtered))
	for i := range filtered {
		copyItem := filtered[i]
		result = append(result, &copyItem)
	}
	return result, nil
}

func (s *service) CreateTurn(ctx context.Context, sessionID, agentID uuid.UUID) (*ChatTurn, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != "active" {
		return nil, ErrSessionClosed
	}
	if err := s.ensureProjectNotPausedForSession(ctx, session); err != nil {
		return nil, err
	}

	agentRecord, err := s.agents.GetByID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agentRecord.OrganizationID != session.OrganizationID {
		return nil, repo.ErrNotFound
	}

	turns, err := s.turns.ListBySession(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	if current, err := s.reconcileSessionCurrentTurn(ctx, session.ID, turns); err != nil {
		return nil, err
	} else if current != nil {
		live := ChatTurn(*current)
		return &live, nil
	}
	nextTurnNumber := 1
	for _, turn := range turns {
		if turn.TurnNumber >= nextTurnNumber {
			nextTurnNumber = turn.TurnNumber + 1
		}
	}
	cycleID := uuid.New()

	created, err := s.turns.Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     nextTurnNumber,
		CycleID:        &cycleID,
		RespondingType: "agent",
		RespondingID:   agentID,
		Status:         "pending",
	})
	if err != nil {
		return nil, err
	}

	turns = append(turns, created)
	if _, err := s.reconcileSessionCurrentTurn(ctx, session.ID, turns); err != nil {
		return nil, err
	}
	if _, err := s.sessions.IncrementCounts(ctx, session.ID, 1, 0); err != nil {
		return nil, err
	}

	return &created, nil
}

func (s *service) StartTurn(ctx context.Context, turnID uuid.UUID) error {
	turn, err := s.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	if normalizeTurnStatus(turn.Status) != "pending" {
		return ErrInvalidStatusTransition
	}
	started, err := s.turns.SetStarted(ctx, turn.ID, s.clock.Now().UTC())
	if err != nil {
		return err
	}
	turns, err := s.turns.ListBySession(ctx, turn.SessionID)
	if err != nil {
		return err
	}
	if _, err := s.reconcileSessionCurrentTurn(ctx, turn.SessionID, turns); err != nil {
		return err
	}

	session, err := s.GetSession(ctx, turn.SessionID)
	if err != nil {
		return err
	}
	metrics.RecordAgentTurn("started")
	actorType, actorID := actorFromContext(ctx)
	return s.publishEvent(ctx, session.OrganizationID, "chat.turn.started", actorType, actorID, map[string]any{
		"session_id": session.ID,
		"turn_id":    turn.ID,
		"status":     started.Status,
	})
}

func (s *service) CompleteTurn(ctx context.Context, turnID uuid.UUID) error {
	turn, err := s.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	if normalizeTurnStatus(turn.Status) != "in_progress" {
		return ErrInvalidStatusTransition
	}

	completedAt := s.clock.Now().UTC()
	durationMS := 0
	if turn.StartedAt != nil {
		durationMS = int(completedAt.Sub(turn.StartedAt.UTC()).Milliseconds())
		if durationMS < 0 {
			durationMS = 0
		}
	}
	if _, err := s.turns.SetCompleted(ctx, turn.ID, completedAt, durationMS); err != nil {
		return err
	}
	metrics.RecordAgentTurn("completed")
	turns, err := s.turns.ListBySession(ctx, turn.SessionID)
	if err != nil {
		return err
	}
	if _, err := s.reconcileSessionCurrentTurn(ctx, turn.SessionID, turns); err != nil {
		return err
	}

	session, err := s.GetSession(ctx, turn.SessionID)
	if err != nil {
		return err
	}
	actorType, actorID := actorFromContext(ctx)
	return s.publishEvent(ctx, session.OrganizationID, "chat.turn.completed", actorType, actorID, map[string]any{
		"session_id":  session.ID,
		"turn_id":     turn.ID,
		"status":      "completed",
		"duration_ms": durationMS,
	})
}

func (s *service) CancelTurn(ctx context.Context, turnID uuid.UUID, reason string) error {
	turn, err := s.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	if normalizeTurnStatus(turn.Status) != "in_progress" {
		return ErrInvalidStatusTransition
	}

	now := s.clock.Now().UTC()
	session, err := s.GetSession(ctx, turn.SessionID)
	if err != nil {
		return err
	}

	turnsToCancel := []repo.ChatTurn{repo.ChatTurn(*turn)}
	if turn.TriggerMessageID != nil && *turn.TriggerMessageID != uuid.Nil {
		if err := s.markMessageDispatchCancelled(ctx, *turn.TriggerMessageID, reason, now); err != nil {
			return err
		}
		sessionTurns, err := s.turns.ListBySession(ctx, turn.SessionID)
		if err != nil {
			return err
		}
		turnsToCancel = collectMessageTurnCancellations(sessionTurns, *turn.TriggerMessageID)
		if len(turnsToCancel) == 0 {
			turnsToCancel = []repo.ChatTurn{repo.ChatTurn(*turn)}
		}
		if s.enqueuer != nil {
			if _, err := s.enqueuer.CancelGroup(ctx, nil, jobqueue.AgentTurnGroupKey(turn.SessionID, *turn.TriggerMessageID), "chat turn cancelled: "+strings.TrimSpace(reason)); err != nil {
				return err
			}
		}
	}

	cancelledTurns := make([]repo.ChatTurn, 0, len(turnsToCancel))
	for _, candidate := range turnsToCancel {
		if isTerminalTurnStatus(candidate.Status) {
			continue
		}
		cancelled, err := s.turns.SetCancelled(ctx, candidate.ID, now, now)
		if err != nil {
			return err
		}
		metrics.RecordAgentTurn("cancelled")
		cancelledTurns = append(cancelledTurns, cancelled)
	}
	if len(cancelledTurns) == 0 {
		return ErrInvalidStatusTransition
	}

	sessionTurns, err := s.turns.ListBySession(ctx, turn.SessionID)
	if err != nil {
		return err
	}
	if _, err := s.reconcileSessionCurrentTurn(ctx, turn.SessionID, sessionTurns); err != nil {
		return err
	}
	actorType, actorID := actorFromContext(ctx)
	for _, cancelled := range cancelledTurns {
		payload := map[string]any{
			"session_id":  session.ID,
			"turn_id":     cancelled.ID,
			"reason":      strings.TrimSpace(reason),
			"retry_count": cancelled.RetryCount,
		}
		if cancelled.TriggerMessageID != nil && *cancelled.TriggerMessageID != uuid.Nil {
			payload["trigger_message_id"] = *cancelled.TriggerMessageID
		}
		if err := s.publishEvent(ctx, session.OrganizationID, "chat.turn.cancelled", actorType, actorID, payload); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) FailTurn(ctx context.Context, turnID uuid.UUID, errorMsg string) error {
	turn, err := s.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	if normalizeTurnStatus(turn.Status) != "in_progress" {
		return ErrInvalidStatusTransition
	}
	if _, err := s.turns.SetFailed(ctx, turn.ID, strings.TrimSpace(errorMsg), s.clock.Now().UTC()); err != nil {
		return err
	}
	metrics.RecordAgentTurn("failed")
	turns, err := s.turns.ListBySession(ctx, turn.SessionID)
	if err != nil {
		return err
	}
	if _, err := s.reconcileSessionCurrentTurn(ctx, turn.SessionID, turns); err != nil {
		return err
	}
	return nil
}

func (s *service) GetTurn(ctx context.Context, turnID uuid.UUID) (*ChatTurn, error) {
	turn, err := s.turns.GetByID(ctx, turnID)
	if err != nil {
		return nil, err
	}
	session, err := s.sessions.GetByID(ctx, turn.SessionID)
	if err != nil {
		return nil, err
	}
	if !s.orgVisibleInContext(ctx, session.OrganizationID) {
		return nil, repo.ErrNotFound
	}
	return &turn, nil
}

func (s *service) ListTurns(ctx context.Context, sessionID uuid.UUID) ([]*ChatTurn, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	turns, err := s.turns.ListBySession(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	result := make([]*ChatTurn, 0, len(turns))
	for i := range turns {
		copyItem := turns[i]
		result = append(result, &copyItem)
	}
	return result, nil
}

func (s *service) CancelCurrentTurn(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	turn, err := s.currentInProgressTurn(ctx, sessionID)
	if err != nil {
		return err
	}
	if turn == nil {
		return ErrNoActiveTurn
	}
	return s.CancelTurn(ctx, turn.ID, "cancel_current_turn")
}

func (s *service) CancelAndQueueNew(ctx context.Context, sessionID uuid.UUID, newMessage string) error {
	if err := s.CancelCurrentTurn(ctx, sessionID); err != nil {
		return err
	}
	_, err := s.AppendMessage(ctx, AppendMessageInput{
		SessionID: sessionID,
		Role:      "user",
		Content:   newMessage,
	})
	return err
}

func (s *service) SteerTurn(ctx context.Context, sessionID, messageID uuid.UUID, steerContent string) error {
	_, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	turn, err := s.currentInProgressTurn(ctx, sessionID)
	if err != nil {
		return err
	}
	if turn == nil {
		return ErrNoActiveTurn
	}

	message, err := s.GetMessage(ctx, messageID)
	if err != nil {
		return err
	}
	if message.SessionID != sessionID {
		return repo.ErrNotFound
	}

	if err := s.CancelTurn(ctx, turn.ID, "steer_turn"); err != nil {
		return err
	}
	if err := s.RedactMessage(ctx, message.ID); err != nil {
		return err
	}

	nextTurn, err := s.CreateTurn(ctx, sessionID, turn.RespondingID)
	if err != nil {
		return err
	}

	// Preserve linkage to the steered message/turn and attach the steer input to
	// the newly queued pending turn so the executor can resume from this direction.
	steerMetadata := mustJSON(map[string]any{
		"steer_message_id": messageID,
		"steer_turn_id":    turn.ID,
	})
	_, err = s.AppendMessage(ctx, AppendMessageInput{
		SessionID: sessionID,
		TurnID:    &nextTurn.ID,
		Role:      "user",
		Content:   steerContent,
		Metadata:  steerMetadata,
	})
	return err
}

func (s *service) AddReaction(ctx context.Context, messageID uuid.UUID, reactorType string, reactorID uuid.UUID, emoji string) (*ChatMessageReaction, error) {
	message, err := s.GetMessage(ctx, messageID)
	if err != nil {
		return nil, err
	}
	session, err := s.GetSession(ctx, message.SessionID)
	if err != nil {
		return nil, err
	}

	normalizedReactorType := normalizeParticipantType(reactorType)
	if normalizedReactorType == "" {
		return nil, fmt.Errorf("invalid reactor_type")
	}
	if reactorID == uuid.Nil {
		return nil, fmt.Errorf("reactor_id is required")
	}
	if strings.TrimSpace(emoji) == "" {
		return nil, fmt.Errorf("emoji is required")
	}
	if err := s.validateParticipantInOrg(ctx, normalizedReactorType, reactorID, session.OrganizationID); err != nil {
		return nil, err
	}

	reaction, err := s.reactions.Create(ctx, repo.ChatMessageReaction{
		MessageID:   message.ID,
		SessionID:   message.SessionID,
		ReactorType: normalizedReactorType,
		ReactorID:   reactorID,
		Emoji:       strings.TrimSpace(emoji),
	})
	if errors.Is(err, repo.ErrConflict) {
		return nil, ErrDuplicateReaction
	}
	if err != nil {
		return nil, err
	}

	if err := s.publishEvent(ctx, session.OrganizationID, "chat.reaction.added", normalizedReactorType, &reactorID, map[string]any{
		"session_id":  session.ID,
		"message_id":  message.ID,
		"reaction_id": reaction.ID,
		"emoji":       reaction.Emoji,
	}); err != nil {
		return nil, err
	}

	return &reaction, nil
}

func (s *service) RemoveReaction(ctx context.Context, reactionID uuid.UUID) error {
	reaction, err := s.reactions.GetByID(ctx, reactionID)
	if err != nil {
		return err
	}
	session, err := s.GetSession(ctx, reaction.SessionID)
	if err != nil {
		return err
	}

	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return ErrForbidden
	}
	isAdmin := isAdminRole(principal.Role)
	isReactor := reaction.ReactorType == "human_user" && principal.UserID == reaction.ReactorID
	if !isAdmin && !isReactor {
		return ErrForbidden
	}

	if err := s.reactions.DeleteByID(ctx, reaction.ID); err != nil {
		return err
	}

	actorType, actorID := actorFromContext(ctx)
	return s.publishEvent(ctx, session.OrganizationID, "chat.reaction.removed", actorType, actorID, map[string]any{
		"session_id":  session.ID,
		"message_id":  reaction.MessageID,
		"reaction_id": reaction.ID,
	})
}

func (s *service) ListReactions(ctx context.Context, messageID uuid.UUID) ([]*ChatMessageReaction, error) {
	message, err := s.GetMessage(ctx, messageID)
	if err != nil {
		return nil, err
	}
	reactions, err := s.reactions.ListByMessage(ctx, message.ID)
	if err != nil {
		return nil, err
	}

	result := make([]*ChatMessageReaction, 0, len(reactions))
	for i := range reactions {
		copyItem := reactions[i]
		result = append(result, &copyItem)
	}
	return result, nil
}

func (s *service) appendMessageAndUpdateSession(ctx context.Context, session repo.ChatSession, input AppendMessageInput) (*repo.ChatMessage, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, session.ID.String()); err != nil {
		return nil, mapDBError(err)
	}

	var sequenceNumber int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence_number), 0) + 1
		FROM chat_message
		WHERE session_id = $1
	`, session.ID).Scan(&sequenceNumber); err != nil {
		return nil, mapDBError(err)
	}

	role := normalizeMessageRole(input.Role)
	if role == "" {
		return nil, fmt.Errorf("invalid role")
	}
	contentFormat := normalizeContentFormat(input.ContentFormat)

	authorType := ""
	var authorID *uuid.UUID
	if input.AuthorType != nil {
		authorType = normalizeParticipantType(*input.AuthorType)
		if authorType == "" {
			return nil, fmt.Errorf("invalid author_type")
		}
	}
	if input.AuthorID != nil && *input.AuthorID != uuid.Nil {
		authorID = input.AuthorID
	}
	if authorType == "" {
		if principal, ok := middleware.PrincipalFromContext(ctx); ok {
			authorType = "human_user"
			authorID = &principal.UserID
		}
	}
	if authorType != "" && authorID == nil {
		return nil, fmt.Errorf("author_id is required for author_type %s", authorType)
	}
	if authorType == "" {
		authorID = nil
	}

	metadata := normalizeJSON(input.Metadata, json.RawMessage(`{}`))
	var messageID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO chat_message (
			session_id,
			turn_id,
			sequence_number,
			author_type,
			author_id,
			role,
			content,
			content_format,
			status,
			tool_call_id,
			metadata
		)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, 'pending', $9, $10::jsonb)
		RETURNING id
	`, session.ID, input.TurnID, sequenceNumber, authorType, authorID, role, input.Content, contentFormat, trimStringPointer(input.ToolCallID), metadata).Scan(&messageID); err != nil {
		return nil, mapDBError(err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE chat_session
		SET last_message_at = now(),
		    message_count = message_count + 1
		WHERE id = $1
	`, session.ID); err != nil {
		return nil, mapDBError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	message, err := s.messages.GetByID(ctx, messageID)
	if err != nil {
		return nil, err
	}
	return &message, nil
}

func (s *service) currentInProgressTurn(ctx context.Context, sessionID uuid.UUID) (*repo.ChatTurn, error) {
	turns, err := s.turns.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	var selected *repo.ChatTurn
	for i := range turns {
		turn := turns[i]
		if normalizeTurnStatus(turn.Status) != "in_progress" {
			continue
		}
		if selected == nil || turn.TurnNumber > selected.TurnNumber {
			copied := turn
			selected = &copied
		}
	}
	return selected, nil
}

func (s *service) reconcileSessionCurrentTurn(ctx context.Context, sessionID uuid.UUID, turns []repo.ChatTurn) (*repo.ChatTurn, error) {
	current, duplicateTurns := repo.CanonicalLiveTurn(turns)
	if len(duplicateTurns) > 0 {
		now := s.clock.Now().UTC()
		for _, turn := range duplicateTurns {
			if _, err := s.turns.SetCancelled(ctx, turn.ID, now, now); err != nil {
				return nil, err
			}
		}
	}

	var currentTurnID *uuid.UUID
	if current != nil {
		id := current.ID
		currentTurnID = &id
	}
	if _, err := s.sessions.UpdateCurrentTurn(ctx, sessionID, currentTurnID); err != nil {
		return nil, err
	}
	return current, nil
}

func (s *service) hasInProgressTurn(ctx context.Context, sessionID uuid.UUID) (bool, error) {
	turn, err := s.currentInProgressTurn(ctx, sessionID)
	if err != nil {
		return false, err
	}
	return turn != nil, nil
}

func (s *service) resolveOrg(ctx context.Context, fallback uuid.UUID) (uuid.UUID, *middleware.Principal, error) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if ok {
		return principal.OrganizationID, &principal, nil
	}
	if fallback != uuid.Nil {
		return fallback, nil, nil
	}
	return uuid.Nil, nil, fmt.Errorf("organization_id is required")
}

func (s *service) orgVisibleInContext(ctx context.Context, organizationID uuid.UUID) bool {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return true
	}
	return principal.OrganizationID == organizationID
}

func (s *service) validateParticipantInOrg(ctx context.Context, participantType string, participantID uuid.UUID, orgID uuid.UUID) error {
	switch participantType {
	case "human_user":
		user, err := s.users.GetByID(ctx, participantID)
		if errors.Is(err, repo.ErrNotFound) {
			return repo.ErrNotFound
		}
		if err != nil {
			return err
		}
		if user.OrganizationID != orgID {
			return repo.ErrNotFound
		}
		return nil
	case "agent":
		agentRecord, err := s.agents.GetByID(ctx, participantID)
		if errors.Is(err, repo.ErrNotFound) {
			return repo.ErrNotFound
		}
		if err != nil {
			return err
		}
		if agentRecord.OrganizationID != orgID {
			return repo.ErrNotFound
		}
		return nil
	default:
		return fmt.Errorf("invalid participant_type")
	}
}

func (s *service) publishEvent(ctx context.Context, orgID uuid.UUID, eventType, actorType string, actorID *uuid.UUID, payload map[string]any) error {
	domainType, domainActorID := normalizeDomainActor(actorType, actorID)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: orgID,
		EventType:      strings.TrimSpace(eventType),
		ActorType:      domainType,
		ActorID:        domainActorID,
		Payload:        encoded,
	})
}

func actorFromContext(ctx context.Context) (string, *uuid.UUID) {
	principal, ok := middleware.PrincipalFromContext(ctx)
	if !ok {
		return "system", nil
	}
	id := principal.UserID
	return "human_user", &id
}

func normalizeDomainActor(actorType string, actorID *uuid.UUID) (string, *uuid.UUID) {
	switch normalizeActorType(actorType) {
	case "human_user", "human":
		if actorID == nil || *actorID == uuid.Nil {
			return "system", nil
		}
		return "human", actorID
	case "agent":
		if actorID == nil || *actorID == uuid.Nil {
			return "system", nil
		}
		return "agent", actorID
	case "supervisor":
		return "supervisor", nil
	default:
		return "system", nil
	}
}

func normalizeActorType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "human", "human_user":
		return "human_user"
	case "agent":
		return "agent"
	case "system":
		return "system"
	case "supervisor":
		return "supervisor"
	default:
		return ""
	}
}

func normalizeScopeType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "organization":
		return "organization"
	case "project":
		return "project"
	case "project_task":
		return "project_task"
	default:
		return ""
	}
}

func normalizeSessionMode(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "sync":
		return "sync"
	case "async":
		return "async"
	default:
		return ""
	}
}

func normalizeSessionStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "active":
		return "active"
	case "closed":
		return "closed"
	case "archived":
		return "archived"
	default:
		return ""
	}
}

func normalizeParticipantType(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "human_user", "human":
		return "human_user"
	case "agent":
		return "agent"
	default:
		return ""
	}
}

func normalizeParticipantRole(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "owner":
		return "owner"
	case "observer":
		return "observer"
	default:
		return "member"
	}
}

func normalizeMessageRole(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return "user"
	case "user":
		return "user"
	case "assistant":
		return "assistant"
	case "tool_call":
		return "tool_call"
	case "tool_result":
		return "tool_result"
	case "system":
		return "system"
	default:
		return ""
	}
}

func normalizeContentFormat(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "text":
		return "text"
	case "markdown":
		return "markdown"
	case "tool_json":
		return "tool_json"
	default:
		return "text"
	}
}

func normalizeMessageStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "pending":
		return "pending"
	case "streaming":
		return "streaming"
	case "final":
		return "final"
	case "failed":
		return "failed"
	case "redacted":
		return "redacted"
	default:
		return ""
	}
}

func normalizeTurnStatus(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "pending":
		return "pending"
	case "in_progress":
		return "in_progress"
	case "completed":
		return "completed"
	case "cancelled":
		return "cancelled"
	case "failed":
		return "failed"
	default:
		return ""
	}
}

func isTerminalTurnStatus(status string) bool {
	switch normalizeTurnStatus(status) {
	case "completed", "cancelled", "failed":
		return true
	default:
		return false
	}
}

func collectMessageTurnCancellations(turns []repo.ChatTurn, triggerMessageID uuid.UUID) []repo.ChatTurn {
	matches := make([]repo.ChatTurn, 0, len(turns))
	for _, turn := range turns {
		if turn.TriggerMessageID == nil || *turn.TriggerMessageID != triggerMessageID {
			continue
		}
		matches = append(matches, turn)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].TurnNumber == matches[j].TurnNumber {
			return matches[i].RetryCount < matches[j].RetryCount
		}
		return matches[i].TurnNumber < matches[j].TurnNumber
	})
	return matches
}

func (s *service) markMessageDispatchCancelled(ctx context.Context, messageID uuid.UUID, reason string, now time.Time) error {
	message, err := s.messages.GetByID(ctx, messageID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return err
	}
	merged, err := MergeAgentTurnDispatchCancelledMetadata(message.Metadata, reason, now)
	if err != nil {
		return err
	}
	_, err = s.messages.UpdateMetadata(ctx, message.ID, merged)
	return err
}

func isMessageTransitionAllowed(fromStatus, toStatus string) bool {
	allowed, ok := messageStatusTransitions[fromStatus]
	if !ok {
		return false
	}
	_, ok = allowed[toStatus]
	return ok
}

func isNotificationPreference(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "all", "mentions", "none":
		return true
	default:
		return false
	}
}

func isAdminRole(role string) bool {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "admin", "owner":
		return true
	default:
		return false
	}
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

func normalizeJSON(value json.RawMessage, fallback json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return fallback
	}
	if !json.Valid(value) {
		return fallback
	}
	return value
}

func mustJSON(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}

func trimStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func uuidPtr(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	copied := value
	return &copied
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%w: %s", repo.ErrConflict, pgErr.Message)
		case "23503":
			return fmt.Errorf("%w: %s", repo.ErrNotFound, pgErr.Message)
		case "23514":
			return fmt.Errorf("validation failed: %s", pgErr.Message)
		}
	}
	return err
}

func isSteerMetadata(metadata json.RawMessage) bool {
	if len(metadata) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return false
	}
	if _, ok := payload["steer_turn_id"]; ok {
		return true
	}
	if _, ok := payload["steer_message_id"]; ok {
		return true
	}
	return false
}
