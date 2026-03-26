package budget

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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	budgetAnomalyScanJobType = "budget.anomaly_scan"

	createdBySystem = "system"
	createdByHuman  = "human_user"
)

var (
	systemActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

	ErrOrganizationIDRequired = errors.New("organization_id is required")
	ErrInvalidPeriod          = errors.New("period must be daily, weekly, or monthly")
	ErrInvalidCreatedByType   = errors.New("created_by_type must be human_user or system")
	ErrCreatedByIDRequired    = errors.New("created_by_id is required for non-system actors")
)

type TokenBudget = repo.TokenBudget

type CreateBudgetRequest struct {
	OrganizationID  uuid.UUID
	ProjectID       *uuid.UUID
	Period          string
	SoftLimitTokens *int64
	HardLimitTokens *int64
	AlertChannel    *string
	IsEnabled       *bool
	CreatedByType   string
	CreatedByID     uuid.UUID
}

type UpdateBudgetRequest struct {
	SoftLimitTokens *int64
	HardLimitTokens *int64
	AlertChannel    *string
	IsEnabled       *bool
}

type BudgetService interface {
	CheckBudget(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, estimatedTokens int64) (*BudgetCheckResult, error)
	RecordUsage(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, tokensUsed int64) error
	ScanForAnomalies(ctx context.Context) error
	RegisterJobs(registrar JobRegistrar)

	Create(ctx context.Context, req CreateBudgetRequest) (*TokenBudget, error)
	Update(ctx context.Context, budgetID uuid.UUID, req UpdateBudgetRequest) (*TokenBudget, error)
	Delete(ctx context.Context, budgetID uuid.UUID) error
	List(ctx context.Context, orgID uuid.UUID) ([]*TokenBudget, error)
}

type BudgetCheckResult struct {
	Allowed       bool
	SoftLimitHit  bool
	HardLimitHit  bool
	CurrentTokens int64
	LimitTokens   int64
}

type DailyRollup struct {
	DayStart time.Time
	Tokens   int64
}

type UsageQuerier interface {
	SumTokens(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, since time.Time) (int64, error)
	DailyRollups(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, days int) ([]DailyRollup, error)
}

type NotificationService interface {
	CreateSystemAlert(ctx context.Context, orgID uuid.UUID, alertChannel *string, description string) error
}

type TokenBudgetRepository interface {
	Create(ctx context.Context, budget repo.TokenBudget) (repo.TokenBudget, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.TokenBudget, error)
	GetByScope(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, period string) (repo.TokenBudget, error)
	List(ctx context.Context, orgID uuid.UUID) ([]repo.TokenBudget, error)
	ListAll(ctx context.Context) ([]repo.TokenBudget, error)
	Update(ctx context.Context, budget repo.TokenBudget) (repo.TokenBudget, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type eventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type JobRegistrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}

type Options struct {
	Pool          *pgxpool.Pool
	Budgets       TokenBudgetRepository
	Usage         UsageQuerier
	Notifications NotificationService
	Events        eventPublisher
	Clock         clock.Clock
	Logger        *slog.Logger
}

type service struct {
	pool          *pgxpool.Pool
	budgets       TokenBudgetRepository
	usage         UsageQuerier
	notifications NotificationService
	events        eventPublisher
	clock         clock.Clock
	logger        *slog.Logger
}

func NewService(opts Options) (BudgetService, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("budget service requires a database pool")
	}
	if opts.Budgets == nil {
		opts.Budgets = repo.NewTokenBudgetRepo(opts.Pool)
	}
	if opts.Usage == nil {
		opts.Usage = NullUsageQuerier{}
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Notifications == nil {
		opts.Notifications = stderrNotificationService{logger: opts.Logger}
	}

	return &service{
		pool:          opts.Pool,
		budgets:       opts.Budgets,
		usage:         opts.Usage,
		notifications: opts.Notifications,
		events:        opts.Events,
		clock:         opts.Clock,
		logger:        opts.Logger,
	}, nil
}

func (s *service) RegisterJobs(registrar JobRegistrar) {
	if registrar == nil {
		return
	}

	registrar.Register(budgetAnomalyScanJobType, func(ctx context.Context, _ jobqueue.Job) error {
		return s.ScanForAnomalies(ctx)
	})
}

func (s *service) Create(ctx context.Context, req CreateBudgetRequest) (*TokenBudget, error) {
	if req.OrganizationID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}

	period := strings.ToLower(strings.TrimSpace(req.Period))
	if _, _, err := periodWindow(period, s.clock.Now().UTC()); err != nil {
		return nil, err
	}

	createdByType, createdByID, err := normalizeCreatedBy(req.CreatedByType, req.CreatedByID)
	if err != nil {
		return nil, err
	}

	isEnabled := true
	if req.IsEnabled != nil {
		isEnabled = *req.IsEnabled
	}

	created, err := s.budgets.Create(ctx, repo.TokenBudget{
		OrganizationID:  req.OrganizationID,
		ProjectID:       req.ProjectID,
		Period:          period,
		SoftLimitTokens: req.SoftLimitTokens,
		HardLimitTokens: req.HardLimitTokens,
		AlertChannel:    trimStringPointer(req.AlertChannel),
		IsEnabled:       isEnabled,
		CreatedByType:   createdByType,
		CreatedByID:     createdByID,
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *service) Update(ctx context.Context, budgetID uuid.UUID, req UpdateBudgetRequest) (*TokenBudget, error) {
	current, err := s.budgets.GetByID(ctx, budgetID)
	if err != nil {
		return nil, err
	}

	if req.SoftLimitTokens != nil {
		current.SoftLimitTokens = req.SoftLimitTokens
	}
	if req.HardLimitTokens != nil {
		current.HardLimitTokens = req.HardLimitTokens
	}
	if req.AlertChannel != nil {
		current.AlertChannel = trimStringPointer(req.AlertChannel)
	}
	if req.IsEnabled != nil {
		current.IsEnabled = *req.IsEnabled
	}

	updated, err := s.budgets.Update(ctx, current)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *service) Delete(ctx context.Context, budgetID uuid.UUID) error {
	return s.budgets.Delete(ctx, budgetID)
}

func (s *service) List(ctx context.Context, orgID uuid.UUID) ([]*TokenBudget, error) {
	if orgID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}

	found, err := s.budgets.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	items := make([]*TokenBudget, 0, len(found))
	for i := range found {
		items = append(items, &found[i])
	}
	return items, nil
}

func (s *service) CheckBudget(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, estimatedTokens int64) (*BudgetCheckResult, error) {
	if orgID == uuid.Nil {
		return nil, ErrOrganizationIDRequired
	}
	if estimatedTokens < 0 {
		estimatedTokens = 0
	}

	allBudgets, err := s.budgets.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	now := s.clock.Now().UTC()
	result := &BudgetCheckResult{Allowed: true}

	for _, budget := range applicableBudgets(allBudgets, projectID) {
		if !budget.IsEnabled {
			continue
		}

		periodStart, periodKey, periodErr := periodWindow(budget.Period, now)
		if periodErr != nil {
			return nil, periodErr
		}

		currentUsage, usageErr := s.usage.SumTokens(ctx, orgID, budget.ProjectID, nil, periodStart)
		if usageErr != nil {
			return nil, usageErr
		}
		projected := currentUsage + estimatedTokens
		if projected > result.CurrentTokens {
			result.CurrentTokens = projected
		}

		if budget.HardLimitTokens != nil && projected >= *budget.HardLimitTokens {
			result.Allowed = false
			result.HardLimitHit = true
			result.LimitTokens = *budget.HardLimitTokens
			result.CurrentTokens = projected
			return result, nil
		}

		if budget.SoftLimitTokens != nil && projected >= *budget.SoftLimitTokens {
			result.SoftLimitHit = true
			if result.LimitTokens == 0 || *budget.SoftLimitTokens < result.LimitTokens {
				result.LimitTokens = *budget.SoftLimitTokens
			}
			if warnErr := s.warnSoftLimitIfNeeded(ctx, budget, periodKey, projected, *budget.SoftLimitTokens); warnErr != nil {
				return nil, warnErr
			}
		}
	}

	if agentID != nil {
		agentLimit, period, agentErr := s.lookupAgentBudget(ctx, orgID, *agentID)
		if agentErr != nil {
			return nil, agentErr
		}
		if agentLimit != nil && period != nil {
			periodStart, _, periodErr := periodWindow(*period, now)
			if periodErr != nil {
				return nil, periodErr
			}

			currentUsage, usageErr := s.usage.SumTokens(ctx, orgID, nil, agentID, periodStart)
			if usageErr != nil {
				return nil, usageErr
			}
			projected := currentUsage + estimatedTokens
			if projected > result.CurrentTokens {
				result.CurrentTokens = projected
			}

			if projected >= *agentLimit {
				result.Allowed = false
				result.HardLimitHit = true
				result.LimitTokens = *agentLimit
				result.CurrentTokens = projected
				return result, nil
			}
		}
	}

	return result, nil
}

func (s *service) RecordUsage(_ context.Context, _ uuid.UUID, _ *uuid.UUID, _ *uuid.UUID, _ int64) error {
	// Usage is sourced from model_invocation rollups. Explicit writes are not
	// performed until model invocation persistence is wired in tasks 035/036.
	return nil
}

func (s *service) ScanForAnomalies(ctx context.Context) error {
	now := s.clock.Now().UTC()

	budgets, err := s.budgets.ListAll(ctx)
	if err != nil {
		return err
	}
	for _, budget := range budgets {
		if !budget.IsEnabled {
			continue
		}

		periodStart, _, periodErr := periodWindow(budget.Period, now)
		if periodErr != nil {
			return periodErr
		}

		currentUsage, usageErr := s.usage.SumTokens(ctx, budget.OrganizationID, budget.ProjectID, nil, periodStart)
		if usageErr != nil {
			return usageErr
		}

		rollups, rollupErr := s.usage.DailyRollups(ctx, budget.OrganizationID, budget.ProjectID, 7)
		if rollupErr != nil {
			return rollupErr
		}

		avg := rollingAverage(rollups)
		if avg <= 0 {
			continue
		}

		ratio := float64(currentUsage) / avg
		if ratio <= 3 {
			continue
		}

		message := fmt.Sprintf(
			"Token budget anomaly detected for %s (%s): current period usage=%d, 7-day average=%.2f (ratio=%.2fx).",
			scopeLabel(budget.ProjectID),
			budget.Period,
			currentUsage,
			avg,
			ratio,
		)
		if notifyErr := s.notifications.CreateSystemAlert(ctx, budget.OrganizationID, budget.AlertChannel, message); notifyErr != nil {
			return notifyErr
		}

		if s.events != nil {
			payload, marshalErr := json.Marshal(map[string]any{
				"budget_id":               budget.ID,
				"organization_id":         budget.OrganizationID,
				"project_id":              budget.ProjectID,
				"period":                  budget.Period,
				"current_tokens":          currentUsage,
				"rolling_average_tokens":  avg,
				"rolling_average_ratio":   ratio,
				"anomaly_threshold_ratio": 3.0,
			})
			if marshalErr != nil {
				return fmt.Errorf("marshal anomaly event payload: %w", marshalErr)
			}

			if publishErr := s.events.Publish(ctx, nil, eventbus.DomainEvent{
				OrganizationID: budget.OrganizationID,
				EventType:      "budget.anomaly_detected",
				ActorType:      "system",
				ActorID:        &systemActorID,
				Payload:        payload,
			}); publishErr != nil {
				return publishErr
			}
		}
	}

	return nil
}

func (s *service) warnSoftLimitIfNeeded(ctx context.Context, budget repo.TokenBudget, periodKey string, currentUsage, softLimit int64) error {
	if budget.LastWarnedPeriod != nil && *budget.LastWarnedPeriod == periodKey {
		return nil
	}

	updated := budget
	updated.LastWarnedPeriod = &periodKey
	if _, err := s.budgets.Update(ctx, updated); err != nil {
		return err
	}

	message := fmt.Sprintf(
		"Token budget soft limit reached for %s (%s): %d/%d tokens.",
		scopeLabel(budget.ProjectID),
		budget.Period,
		currentUsage,
		softLimit,
	)
	if err := s.notifications.CreateSystemAlert(ctx, budget.OrganizationID, budget.AlertChannel, message); err != nil {
		return err
	}

	return nil
}

func (s *service) lookupAgentBudget(ctx context.Context, orgID, agentID uuid.UUID) (*int64, *string, error) {
	var (
		limit  *int64
		period *string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT budget_cap_tokens, budget_period
		FROM agent
		WHERE id = $1
		  AND organization_id = $2
	`, agentID, orgID).Scan(&limit, &period)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if period != nil {
		normalized := strings.ToLower(strings.TrimSpace(*period))
		period = &normalized
	}
	return limit, period, nil
}

func periodWindow(period string, now time.Time) (time.Time, string, error) {
	normalized := strings.ToLower(strings.TrimSpace(period))
	if normalized == "" {
		return time.Time{}, "", ErrInvalidPeriod
	}

	now = now.UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	switch normalized {
	case "daily":
		return dayStart, dayStart.Format("2006-01-02"), nil
	case "weekly":
		offset := (int(dayStart.Weekday()) + 6) % 7
		start := dayStart.AddDate(0, 0, -offset)
		year, week := start.ISOWeek()
		return start, fmt.Sprintf("%04d-W%02d", year, week), nil
	case "monthly":
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start, start.Format("2006-01"), nil
	default:
		return time.Time{}, "", fmt.Errorf("%w: %q", ErrInvalidPeriod, period)
	}
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
	default:
		return "", uuid.Nil, fmt.Errorf("%w: %q", ErrInvalidCreatedByType, createdByType)
	}
}

func applicableBudgets(all []repo.TokenBudget, projectID *uuid.UUID) []repo.TokenBudget {
	applicable := make([]repo.TokenBudget, 0, len(all))
	for _, budget := range all {
		if budget.ProjectID == nil {
			applicable = append(applicable, budget)
			continue
		}
		if projectID != nil && *budget.ProjectID == *projectID {
			applicable = append(applicable, budget)
		}
	}
	return applicable
}

func rollingAverage(rollups []DailyRollup) float64 {
	if len(rollups) == 0 {
		return 0
	}

	var total int64
	for _, rollup := range rollups {
		total += rollup.Tokens
	}
	return float64(total) / float64(len(rollups))
}

func scopeLabel(projectID *uuid.UUID) string {
	if projectID == nil {
		return "organization budget"
	}
	return fmt.Sprintf("project %s budget", projectID.String())
}

func trimStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

type stderrNotificationService struct {
	logger *slog.Logger
}

func (s stderrNotificationService) CreateSystemAlert(_ context.Context, orgID uuid.UUID, alertChannel *string, description string) error {
	s.logger.Warn(
		"budget alert",
		"organization_id", orgID,
		"alert_channel", alertChannel,
		"description", description,
	)
	return nil
}

type NullUsageQuerier struct{}

func (NullUsageQuerier) SumTokens(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID, time.Time) (int64, error) {
	return 0, nil
}

func (NullUsageQuerier) DailyRollups(context.Context, uuid.UUID, *uuid.UUID, int) ([]DailyRollup, error) {
	return nil, nil
}

type PostgresUsageQuerier struct {
	pool *pgxpool.Pool
}

func NewPostgresUsageQuerier(pool *pgxpool.Pool) *PostgresUsageQuerier {
	return &PostgresUsageQuerier{pool: pool}
}

func (q *PostgresUsageQuerier) SumTokens(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, since time.Time) (int64, error) {
	if q == nil || q.pool == nil {
		return 0, nil
	}

	args := []any{orgID, since.UTC()}
	var builder strings.Builder
	builder.WriteString(`
		SELECT COALESCE(SUM(COALESCE(input_tokens, 0) + COALESCE(output_tokens, 0) + COALESCE(cache_read_tokens, 0)), 0)
		FROM model_invocation
		WHERE organization_id = $1
		  AND created_at >= $2
	`)

	nextArg := 3
	if projectID != nil {
		builder.WriteString(fmt.Sprintf(" AND project_id = $%d", nextArg))
		args = append(args, *projectID)
		nextArg++
	}
	if agentID != nil {
		builder.WriteString(fmt.Sprintf(" AND agent_id = $%d", nextArg))
		args = append(args, *agentID)
	}

	var total int64
	err := q.pool.QueryRow(ctx, builder.String(), args...).Scan(&total)
	if isMissingUsageSchema(err) {
		return 0, nil
	}
	return total, err
}

func (q *PostgresUsageQuerier) DailyRollups(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, days int) ([]DailyRollup, error) {
	if q == nil || q.pool == nil || days <= 0 {
		return nil, nil
	}

	since := time.Now().UTC().AddDate(0, 0, -days)
	args := []any{orgID, since}

	var builder strings.Builder
	builder.WriteString(`
		SELECT
			date_trunc('day', created_at AT TIME ZONE 'UTC') AS day_start,
			COALESCE(SUM(COALESCE(input_tokens, 0) + COALESCE(output_tokens, 0) + COALESCE(cache_read_tokens, 0)), 0) AS total_tokens
		FROM model_invocation
		WHERE organization_id = $1
		  AND created_at >= $2
	`)

	nextArg := 3
	if projectID != nil {
		builder.WriteString(fmt.Sprintf(" AND project_id = $%d", nextArg))
		args = append(args, *projectID)
	}
	builder.WriteString(`
		GROUP BY day_start
		ORDER BY day_start DESC
	`)

	rows, err := q.pool.Query(ctx, builder.String(), args...)
	if isMissingUsageSchema(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rollups := make([]DailyRollup, 0, days)
	for rows.Next() {
		var rollup DailyRollup
		if scanErr := rows.Scan(&rollup.DayStart, &rollup.Tokens); scanErr != nil {
			return nil, scanErr
		}
		rollups = append(rollups, rollup)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return rollups, nil
}

func isMissingUsageSchema(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01" || pgErr.Code == "42703"
	}
	return false
}
