package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	statusDraft     = "draft"
	statusActive    = "active"
	statusPaused    = "paused"
	statusRetired   = "retired"
	statusCancelled = "cancelled"
	statusExpired   = "expired"
	statusPromoted  = "promoted"

	agentClassStaff = "staff"
	agentClassTemp  = "temp"

	createdBySystem = "system"
	createdByAgent  = "agent"
	createdByHuman  = "human_user"

	tempBatchSizeDefault = 100
)

var (
	systemActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

	ErrOrganizationIDRequired     = errors.New("organization_id is required")
	ErrDisplayNameRequired        = errors.New("display_name is required")
	ErrTempProjectIDRequired      = errors.New("temp_project_id is required")
	ErrInvalidCreatedByType       = errors.New("created_by_type must be human_user, agent, or system")
	ErrCreatedByIDRequired        = errors.New("created_by_id is required")
	ErrInvalidTransition          = errors.New("invalid lifecycle transition")
	ErrInvalidForTempAgent        = errors.New("operation is invalid for temp agent")
	ErrConcurrentTempLimitReached = errors.New("max concurrent temp agent limit reached")
)

type Agent = repo.Agent

type AgentFilter struct {
	AgentClass      string
	LifecycleStatus string
}

type CreateAgentRequest struct {
	OrganizationID        uuid.UUID
	DisplayName           string
	SystemPrompt          string
	OperatorInstructions  string
	AgentType             string
	PrivateMemory         bool
	MemoryReadScopes      []string
	ToolAllowList         []string
	ToolDenyList          []string
	DefaultModelProfileID *string
	BudgetCapTokens       *int64
	BudgetPeriod          *string
	CreatedByType         string
	CreatedByID           uuid.UUID
}

type UpdateAgentRequest struct {
	DisplayName           *string
	SystemPrompt          *string
	OperatorInstructions  *string
	AgentType             *string
	PrivateMemory         *bool
	MemoryReadScopes      *[]string
	ToolAllowList         *[]string
	ToolDenyList          *[]string
	DefaultModelProfileID *string
	BudgetCapTokens       *int64
	BudgetPeriod          *string
}

type CreateTempAgentRequest struct {
	DisplayName           string
	SystemPrompt          string
	OperatorInstructions  string
	AgentType             string
	PrivateMemory         bool
	MemoryReadScopes      []string
	ToolAllowList         []string
	ToolDenyList          []string
	DefaultModelProfileID *string
	BudgetCapTokens       *int64
	BudgetPeriod          *string
	TempProjectID         uuid.UUID
	TempTTLSeconds        *int32
	CreatedByType         string
	CreatedByID           uuid.UUID
}

type PromoteTempRequest struct {
	CreatedByType string
	CreatedByID   uuid.UUID
}

type AgentService interface {
	Create(ctx context.Context, req CreateAgentRequest) (*Agent, error)
	Get(ctx context.Context, orgID, agentID uuid.UUID) (*Agent, error)
	List(ctx context.Context, orgID uuid.UUID, filter AgentFilter) ([]*Agent, error)
	Update(ctx context.Context, orgID, agentID uuid.UUID, req UpdateAgentRequest) (*Agent, error)

	Pause(ctx context.Context, orgID, agentID uuid.UUID) error
	Unpause(ctx context.Context, orgID, agentID uuid.UUID) error
	Retire(ctx context.Context, orgID, agentID uuid.UUID) error
	Cancel(ctx context.Context, orgID, agentID uuid.UUID) error

	CreateTemp(ctx context.Context, orgID uuid.UUID, req CreateTempAgentRequest) (*Agent, error)
	ExpireTemp(ctx context.Context, orgID, agentID uuid.UUID, reason string) error
	PromoteTemp(ctx context.Context, orgID, agentID uuid.UUID, req PromoteTempRequest) (*Agent, error)

	EnforceMaxConcurrentTemps(ctx context.Context, orgID uuid.UUID) error
	RetireExpiredTemps(ctx context.Context) error
}

type AgentRepository interface {
	Create(ctx context.Context, agent repo.Agent) (repo.Agent, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.Agent, error)
	Update(ctx context.Context, agent repo.Agent) (repo.Agent, error)
	CountActiveTemps(ctx context.Context, organizationID uuid.UUID) (int, error)
}

type ArchivalSummaryService interface {
	Generate(ctx context.Context, agent *Agent) (string, error)
}

type Options struct {
	Pool   *pgxpool.Pool
	Agents AgentRepository
	Events eventbus.EventBus

	Clock   clock.Clock
	Logger  *slog.Logger
	Summary ArchivalSummaryService

	MaxConcurrentTempsLookup func(ctx context.Context, orgID uuid.UUID) (int, error)
	RetireExpiredBatchSize   int
}

type service struct {
	pool   *pgxpool.Pool
	agents AgentRepository
	events eventbus.EventBus

	clock   clock.Clock
	logger  *slog.Logger
	summary ArchivalSummaryService

	maxConcurrentTempsLookup func(ctx context.Context, orgID uuid.UUID) (int, error)
	retireExpiredBatchSize   int
}

func NewService(opts Options) (AgentService, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("agent service requires a database pool")
	}
	if opts.Agents == nil {
		return nil, fmt.Errorf("agent service requires an agent repository")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("agent service requires an event bus")
	}

	clk := opts.Clock
	if clk == nil {
		clk = clock.Real{}
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	summarySvc := opts.Summary
	if summarySvc == nil {
		summarySvc = staticArchivalSummaryService{}
	}

	svc := &service{
		pool:                   opts.Pool,
		agents:                 opts.Agents,
		events:                 opts.Events,
		clock:                  clk,
		logger:                 logger,
		summary:                summarySvc,
		retireExpiredBatchSize: opts.RetireExpiredBatchSize,
	}
	if svc.retireExpiredBatchSize <= 0 {
		svc.retireExpiredBatchSize = tempBatchSizeDefault
	}
	if opts.MaxConcurrentTempsLookup != nil {
		svc.maxConcurrentTempsLookup = opts.MaxConcurrentTempsLookup
	} else {
		svc.maxConcurrentTempsLookup = svc.lookupMaxConcurrentTemps
	}

	return svc, nil
}

func (s *service) Create(ctx context.Context, req CreateAgentRequest) (*Agent, error) {
	if req.OrganizationID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		return nil, ErrDisplayNameRequired
	}

	createdByType, createdByID, err := normalizeCreatedBy(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}

	created, err := s.agents.Create(ctx, repo.Agent{
		OrganizationID:        req.OrganizationID,
		DisplayName:           displayName,
		AgentClass:            agentClassStaff,
		LifecycleStatus:       statusDraft,
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		AgentType:             strings.TrimSpace(req.AgentType),
		PrivateMemory:         req.PrivateMemory,
		MemoryReadScopes:      cloneStringSlice(req.MemoryReadScopes),
		ToolAllowList:         cloneStringSlice(req.ToolAllowList),
		ToolDenyList:          cloneStringSlice(req.ToolDenyList),
		DefaultModelProfileID: req.DefaultModelProfileID,
		BudgetCapTokens:       req.BudgetCapTokens,
		BudgetPeriod:          req.BudgetPeriod,
		CreatedByType:         createdByType,
		CreatedByID:           createdByID,
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *service) Get(ctx context.Context, orgID, agentID uuid.UUID) (*Agent, error) {
	agent, err := s.agents.GetByID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent.OrganizationID != orgID {
		return nil, repo.ErrNotFound
	}
	return &agent, nil
}

func (s *service) List(ctx context.Context, orgID uuid.UUID, filter AgentFilter) ([]*Agent, error) {
	items, err := s.agents.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	wantClass := strings.TrimSpace(strings.ToLower(filter.AgentClass))
	wantStatus := strings.TrimSpace(strings.ToLower(filter.LifecycleStatus))

	out := make([]*Agent, 0, len(items))
	for i := range items {
		item := items[i]
		if wantClass != "" && strings.ToLower(item.AgentClass) != wantClass {
			continue
		}
		if wantStatus != "" && strings.ToLower(item.LifecycleStatus) != wantStatus {
			continue
		}
		copy := item
		out = append(out, &copy)
	}
	return out, nil
}

func (s *service) Update(ctx context.Context, orgID, agentID uuid.UUID, req UpdateAgentRequest) (*Agent, error) {
	existing, err := s.Get(ctx, orgID, agentID)
	if err != nil {
		return nil, err
	}

	if req.DisplayName != nil {
		next := strings.TrimSpace(*req.DisplayName)
		if next == "" {
			return nil, ErrDisplayNameRequired
		}
		existing.DisplayName = next
	}
	if req.SystemPrompt != nil {
		existing.SystemPrompt = *req.SystemPrompt
	}
	if req.OperatorInstructions != nil {
		existing.OperatorInstructions = *req.OperatorInstructions
	}
	if req.AgentType != nil {
		existing.AgentType = strings.TrimSpace(*req.AgentType)
	}
	if req.PrivateMemory != nil {
		existing.PrivateMemory = *req.PrivateMemory
	}
	if req.MemoryReadScopes != nil {
		existing.MemoryReadScopes = cloneStringSlice(*req.MemoryReadScopes)
	}
	if req.ToolAllowList != nil {
		existing.ToolAllowList = cloneStringSlice(*req.ToolAllowList)
	}
	if req.ToolDenyList != nil {
		existing.ToolDenyList = cloneStringSlice(*req.ToolDenyList)
	}
	if req.DefaultModelProfileID != nil {
		existing.DefaultModelProfileID = req.DefaultModelProfileID
	}
	if req.BudgetCapTokens != nil {
		existing.BudgetCapTokens = req.BudgetCapTokens
	}
	if req.BudgetPeriod != nil {
		existing.BudgetPeriod = req.BudgetPeriod
	}

	updated, err := s.agents.Update(ctx, *existing)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *service) Pause(ctx context.Context, orgID, agentID uuid.UUID) error {
	_, err := s.transitionStaffLifecycle(ctx, orgID, agentID, statusPaused)
	return err
}

func (s *service) Unpause(ctx context.Context, orgID, agentID uuid.UUID) error {
	_, err := s.transitionStaffLifecycle(ctx, orgID, agentID, statusActive)
	return err
}

func (s *service) Retire(ctx context.Context, orgID, agentID uuid.UUID) error {
	_, err := s.transitionStaffLifecycle(ctx, orgID, agentID, statusRetired)
	return err
}

func (s *service) Cancel(ctx context.Context, orgID, agentID uuid.UUID) error {
	_, err := s.transitionStaffLifecycle(ctx, orgID, agentID, statusCancelled)
	return err
}

func (s *service) CreateTemp(ctx context.Context, orgID uuid.UUID, req CreateTempAgentRequest) (*Agent, error) {
	if req.TempProjectID == uuid.Nil {
		return nil, ErrTempProjectIDRequired
	}

	if err := s.EnforceMaxConcurrentTemps(ctx, orgID); err != nil {
		return nil, err
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		return nil, ErrDisplayNameRequired
	}

	createdByType, createdByID, err := normalizeCreatedBy(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}

	tempProjectID := req.TempProjectID
	tempExpiresAt := computeTempExpiresAt(s.clock.Now(), req.TempTTLSeconds)

	created, err := s.agents.Create(ctx, repo.Agent{
		OrganizationID:        orgID,
		DisplayName:           displayName,
		AgentClass:            agentClassTemp,
		LifecycleStatus:       statusActive,
		SystemPrompt:          req.SystemPrompt,
		OperatorInstructions:  req.OperatorInstructions,
		AgentType:             strings.TrimSpace(req.AgentType),
		PrivateMemory:         req.PrivateMemory,
		MemoryReadScopes:      cloneStringSlice(req.MemoryReadScopes),
		ToolAllowList:         cloneStringSlice(req.ToolAllowList),
		ToolDenyList:          cloneStringSlice(req.ToolDenyList),
		DefaultModelProfileID: req.DefaultModelProfileID,
		BudgetCapTokens:       req.BudgetCapTokens,
		BudgetPeriod:          req.BudgetPeriod,
		TempProjectID:         &tempProjectID,
		TempTTLSeconds:        req.TempTTLSeconds,
		TempExpiresAt:         tempExpiresAt,
		CreatedByType:         createdByType,
		CreatedByID:           createdByID,
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *service) ExpireTemp(ctx context.Context, orgID, agentID uuid.UUID, reason string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin temp expiration transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	agent, err := getAgentForUpdateTx(ctx, tx, orgID, agentID)
	if err != nil {
		return err
	}
	if err := s.expireTempLockedTx(ctx, tx, agent, reason); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit temp expiration transaction: %w", err)
	}
	return nil
}

func (s *service) PromoteTemp(ctx context.Context, orgID, agentID uuid.UUID, req PromoteTempRequest) (*Agent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin temp promotion transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	temp, err := getAgentForUpdateTx(ctx, tx, orgID, agentID)
	if err != nil {
		return nil, err
	}
	if temp.AgentClass != agentClassTemp {
		return nil, ErrInvalidForTempAgent
	}
	if err := validateTempTransition(temp.LifecycleStatus, statusPromoted); err != nil {
		return nil, err
	}

	createdByType, createdByID, err := normalizeCreatedBy(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}

	newStaff, err := insertAgentTx(ctx, tx, repo.Agent{
		OrganizationID:        temp.OrganizationID,
		DisplayName:           temp.DisplayName,
		AgentClass:            agentClassStaff,
		LifecycleStatus:       statusDraft,
		SystemPrompt:          temp.SystemPrompt,
		OperatorInstructions:  temp.OperatorInstructions,
		AgentType:             temp.AgentType,
		IsStarterTrio:         false,
		PrivateMemory:         temp.PrivateMemory,
		MemoryReadScopes:      cloneStringSlice(temp.MemoryReadScopes),
		ToolAllowList:         cloneStringSlice(temp.ToolAllowList),
		ToolDenyList:          cloneStringSlice(temp.ToolDenyList),
		DefaultModelProfileID: temp.DefaultModelProfileID,
		BudgetCapTokens:       temp.BudgetCapTokens,
		BudgetPeriod:          temp.BudgetPeriod,
		CreatedByType:         createdByType,
		CreatedByID:           createdByID,
	})
	if err != nil {
		return nil, err
	}

	if err := setTempPromotedTx(ctx, tx, temp.ID, temp.OrganizationID, newStaff.ID); err != nil {
		return nil, err
	}

	if err := s.publishDomainEventTx(ctx, tx, temp.OrganizationID, "agent.promoted", map[string]any{
		"temp_agent_id":           temp.ID,
		"promoted_staff_agent_id": newStaff.ID,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit temp promotion transaction: %w", err)
	}

	s.logger.Info("agent.promoted event emitted; Lori review handler not yet registered", "temp_agent_id", temp.ID, "promoted_staff_agent_id", newStaff.ID)

	return &newStaff, nil
}

func (s *service) EnforceMaxConcurrentTemps(ctx context.Context, orgID uuid.UUID) error {
	activeTemps, err := s.agents.CountActiveTemps(ctx, orgID)
	if err != nil {
		return err
	}

	limit, err := s.maxConcurrentTempsLookup(ctx, orgID)
	if err != nil {
		return err
	}

	// NOTE: This is intentionally a synchronous check-before-insert with no DB constraint.
	// Under very high concurrent writes, callers can still race this check.
	if activeTemps >= limit {
		return fmt.Errorf("%w: current=%d limit=%d", ErrConcurrentTempLimitReached, activeTemps, limit)
	}
	return nil
}

func (s *service) RetireExpiredTemps(ctx context.Context) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin expired temp retirement transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	expiredTemps, err := listExpiredTempsForUpdateTx(ctx, tx, s.retireExpiredBatchSize)
	if err != nil {
		return err
	}

	for _, temp := range expiredTemps {
		if err := s.expireTempLockedTx(ctx, tx, temp, "ttl_expired"); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit expired temp retirement transaction: %w", err)
	}
	return nil
}

func (s *service) transitionStaffLifecycle(ctx context.Context, orgID, agentID uuid.UUID, targetStatus string) (*Agent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin lifecycle transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	agent, err := getAgentForUpdateTx(ctx, tx, orgID, agentID)
	if err != nil {
		return nil, err
	}

	if err := ensureStaffClass(agent.AgentClass); err != nil {
		return nil, err
	}

	eventType, err := validateStaffTransition(agent.LifecycleStatus, targetStatus)
	if err != nil {
		return nil, err
	}

	if err := setLifecycleStatusTx(ctx, tx, agent.ID, agent.OrganizationID, targetStatus); err != nil {
		return nil, err
	}

	if err := s.publishDomainEventTx(ctx, tx, agent.OrganizationID, eventType, map[string]any{
		"agent_id": agent.ID,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit lifecycle transaction: %w", err)
	}

	updated := agent
	updated.LifecycleStatus = targetStatus
	return &updated, nil
}

func (s *service) expireTempLockedTx(ctx context.Context, tx pgx.Tx, agent Agent, reason string) error {
	if agent.AgentClass != agentClassTemp {
		return ErrInvalidForTempAgent
	}
	if err := validateTempTransition(agent.LifecycleStatus, statusExpired); err != nil {
		return err
	}

	summary, err := s.summary.Generate(ctx, &agent)
	if err != nil {
		return fmt.Errorf("generate archival summary: %w", err)
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unspecified"
	}

	if err := setTempExpiredTx(ctx, tx, agent.ID, agent.OrganizationID, summary); err != nil {
		return err
	}

	if err := s.publishDomainEventTx(ctx, tx, agent.OrganizationID, "agent.expired", map[string]any{
		"agent_id":         agent.ID,
		"reason":           reason,
		"archival_summary": summary,
	}); err != nil {
		return err
	}

	return nil
}

func (s *service) publishDomainEventTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, eventType string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal domain event payload: %w", err)
	}
	return s.events.Publish(ctx, tx, eventbus.DomainEvent{
		OrganizationID: orgID,
		EventType:      eventType,
		ActorType:      "system",
		Payload:        encoded,
	})
}

func (s *service) lookupMaxConcurrentTemps(ctx context.Context, orgID uuid.UUID) (int, error) {
	var limit int
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE((settings->'agents'->>'max_concurrent_temps')::int, 5)
		FROM organization
		WHERE id = $1
	`, orgID).Scan(&limit)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, repo.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if limit < 0 {
		limit = 0
	}
	return limit, nil
}

func normalizeCreatedBy(createdByType string, createdByID uuid.UUID) (string, uuid.UUID, error) {
	normalized := strings.ToLower(strings.TrimSpace(createdByType))
	switch normalized {
	case "", createdBySystem:
		if createdByID == uuid.Nil {
			createdByID = systemActorID
		}
		return createdBySystem, createdByID, nil
	case "human", createdByHuman:
		if createdByID == uuid.Nil {
			return "", uuid.Nil, ErrCreatedByIDRequired
		}
		return createdByHuman, createdByID, nil
	case createdByAgent:
		if createdByID == uuid.Nil {
			return "", uuid.Nil, ErrCreatedByIDRequired
		}
		return createdByAgent, createdByID, nil
	default:
		return "", uuid.Nil, fmt.Errorf("%w: %q", ErrInvalidCreatedByType, createdByType)
	}
}

func validateStaffTransition(currentStatus, targetStatus string) (string, error) {
	switch targetStatus {
	case statusActive:
		switch currentStatus {
		case statusDraft:
			return "agent.activated", nil
		case statusPaused:
			return "agent.unpaused", nil
		}
	case statusPaused:
		if currentStatus == statusActive {
			return "agent.paused", nil
		}
	case statusRetired:
		if currentStatus == statusActive {
			return "agent.retired", nil
		}
	case statusCancelled:
		if currentStatus == statusActive || currentStatus == statusDraft {
			return "agent.cancelled", nil
		}
	}

	return "", fmt.Errorf("%w: current=%s target=%s", ErrInvalidTransition, currentStatus, targetStatus)
}

func validateTempTransition(currentStatus, targetStatus string) error {
	if currentStatus == statusActive && (targetStatus == statusExpired || targetStatus == statusPromoted) {
		return nil
	}
	return fmt.Errorf("%w: current=%s target=%s", ErrInvalidTransition, currentStatus, targetStatus)
}

func ensureStaffClass(agentClass string) error {
	if agentClass == agentClassTemp {
		return ErrInvalidForTempAgent
	}
	return nil
}

func computeTempExpiresAt(now time.Time, ttlSeconds *int32) *time.Time {
	if ttlSeconds == nil {
		return nil
	}
	expiresAt := now.UTC().Add(time.Duration(*ttlSeconds) * time.Second)
	return &expiresAt
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

type staticArchivalSummaryService struct{}

func (staticArchivalSummaryService) Generate(context.Context, *Agent) (string, error) {
	return "Archival summary pending implementation", nil
}

func getAgentForUpdateTx(ctx context.Context, tx pgx.Tx, orgID, agentID uuid.UUID) (Agent, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM agent
		WHERE organization_id = $1
		  AND id = $2
		FOR UPDATE
	`, orgID, agentID)

	agent, err := scanAgent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, repo.ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func listExpiredTempsForUpdateTx(ctx context.Context, tx pgx.Tx, batchSize int) ([]Agent, error) {
	rows, err := tx.Query(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM agent
		WHERE agent_class = 'temp'
		  AND lifecycle_status = 'active'
		  AND temp_expires_at IS NOT NULL
		  AND temp_expires_at < now()
		ORDER BY temp_expires_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, batchSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := make([]Agent, 0, batchSize)
	for rows.Next() {
		agent, scanErr := scanAgent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		agents = append(agents, agent)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return agents, nil
}

func insertAgentTx(ctx context.Context, tx pgx.Tx, agent Agent) (Agent, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO agent (
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
		RETURNING
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		agent.OrganizationID,
		agent.DisplayName,
		agent.AgentClass,
		agent.LifecycleStatus,
		agent.SystemPrompt,
		agent.OperatorInstructions,
		agent.AgentType,
		agent.IsStarterTrio,
		agent.PrivateMemory,
		agent.MemoryReadScopes,
		agent.ToolAllowList,
		agent.ToolDenyList,
		agent.DefaultModelProfileID,
		agent.BudgetCapTokens,
		agent.BudgetPeriod,
		agent.TempProjectID,
		agent.TempTTLSeconds,
		agent.TempExpiresAt,
		agent.PromotedToAgentID,
		agent.CreatedByType,
		agent.CreatedByID,
	)

	inserted, err := scanAgent(row)
	if err != nil {
		return Agent{}, err
	}
	return inserted, nil
}

func setLifecycleStatusTx(ctx context.Context, tx pgx.Tx, agentID, orgID uuid.UUID, lifecycleStatus string) error {
	result, err := tx.Exec(ctx, `
		UPDATE agent
		SET lifecycle_status = $3
		WHERE id = $1
		  AND organization_id = $2
	`, agentID, orgID, lifecycleStatus)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func setTempExpiredTx(ctx context.Context, tx pgx.Tx, agentID, orgID uuid.UUID, archivalSummary string) error {
	result, err := tx.Exec(ctx, `
		UPDATE agent
		SET
			lifecycle_status = $3,
			metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{archival_summary}', to_jsonb($4::text), true)
		WHERE id = $1
		  AND organization_id = $2
	`, agentID, orgID, statusExpired, archivalSummary)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func setTempPromotedTx(ctx context.Context, tx pgx.Tx, tempAgentID, orgID, promotedToAgentID uuid.UUID) error {
	result, err := tx.Exec(ctx, `
		UPDATE agent
		SET
			lifecycle_status = $3,
			promoted_to_agent_id = $4
		WHERE id = $1
		  AND organization_id = $2
	`, tempAgentID, orgID, statusPromoted, promotedToAgentID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func scanAgent(row interface{ Scan(dest ...any) error }) (Agent, error) {
	var agent Agent
	if err := row.Scan(
		&agent.ID,
		&agent.OrganizationID,
		&agent.DisplayName,
		&agent.AgentClass,
		&agent.LifecycleStatus,
		&agent.SystemPrompt,
		&agent.OperatorInstructions,
		&agent.AgentType,
		&agent.IsStarterTrio,
		&agent.PrivateMemory,
		&agent.MemoryReadScopes,
		&agent.ToolAllowList,
		&agent.ToolDenyList,
		&agent.DefaultModelProfileID,
		&agent.BudgetCapTokens,
		&agent.BudgetPeriod,
		&agent.TempProjectID,
		&agent.TempTTLSeconds,
		&agent.TempExpiresAt,
		&agent.PromotedToAgentID,
		&agent.CreatedByType,
		&agent.CreatedByID,
		&agent.CreatedAt,
		&agent.UpdatedAt,
	); err != nil {
		return Agent{}, err
	}
	return agent, nil
}
