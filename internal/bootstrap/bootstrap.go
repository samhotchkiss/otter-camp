package bootstrap

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"golang.org/x/crypto/bcrypt"
)

const (
	bootstrapEventType = "system.bootstrap"
	bootstrapStepCount = 10
)

//go:embed defaults/skills/*.md
var defaultSkillAssets embed.FS

type StepStatus string

const (
	StepStatusDone    StepStatus = "done"
	StepStatusSkipped StepStatus = "skipped"
	StepStatusFailed  StepStatus = "failed"
)

type ProgressEvent struct {
	Number  int
	Name    string
	Status  StepStatus
	Message string
}

type Options struct {
	Pool     *pgxpool.Pool
	Logger   *slog.Logger
	Version  string
	Config   Config
	Now      func() time.Time
	Progress func(ProgressEvent)
}

type BootstrapState struct {
	Organization repo.Organization
}

type BootstrapStep struct {
	Number int
	Name   string
	Fn     func(ctx context.Context, state *BootstrapState) error
}

type Bootstrapper struct {
	pool     *pgxpool.Pool
	logger   *slog.Logger
	version  string
	config   Config
	now      func() time.Time
	progress func(ProgressEvent)
	steps    []BootstrapStep
}

type skippedStepError struct {
	reason string
}

func (e *skippedStepError) Error() string {
	if e == nil {
		return "step skipped"
	}
	return e.reason
}

func skippedf(format string, args ...any) error {
	return &skippedStepError{reason: fmt.Sprintf(format, args...)}
}

func New(opts Options) *Bootstrapper {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	cfg := normalizeConfig(opts.Config)
	b := &Bootstrapper{
		pool:     opts.Pool,
		logger:   logger,
		version:  strings.TrimSpace(opts.Version),
		config:   cfg,
		now:      nowFn,
		progress: opts.Progress,
	}
	b.steps = b.defaultSteps()
	return b
}

func NewFromEnv(pool *pgxpool.Pool, logger *slog.Logger, version string) *Bootstrapper {
	return New(Options{
		Pool:    pool,
		Logger:  logger,
		Version: version,
		Config:  ConfigFromEnv(),
	})
}

func (b *Bootstrapper) RegisterStep(name string, fn func(ctx context.Context, state *BootstrapState) error) {
	if b == nil || fn == nil {
		return
	}

	stepName := strings.TrimSpace(name)
	if stepName == "" {
		return
	}

	for i := range b.steps {
		if b.steps[i].Name == stepName {
			b.steps[i].Fn = fn
			return
		}
	}

	b.steps = append(b.steps, BootstrapStep{
		Number: len(b.steps) + 1,
		Name:   stepName,
		Fn:     fn,
	})
}

func (b *Bootstrapper) Run(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}

	state := &BootstrapState{}
	for _, step := range b.steps {
		status, message, err := b.runStep(ctx, step, state)
		if b.progress != nil {
			b.progress(ProgressEvent{
				Number:  step.Number,
				Name:    step.Name,
				Status:  status,
				Message: message,
			})
		}
		if err != nil {
			b.logger.Error("bootstrap step failed", "step", step.Number, "name", step.Name, "error", err)
			return fmt.Errorf("bootstrap step %d (%s): %w", step.Number, step.Name, err)
		}

		switch status {
		case StepStatusSkipped:
			b.logger.Info(fmt.Sprintf("bootstrap step %d (%s): skipped - %s", step.Number, step.Name, message))
		default:
			b.logger.Info(fmt.Sprintf("bootstrap step %d (%s): done", step.Number, step.Name))
		}
	}

	return nil
}

func (b *Bootstrapper) Reset(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}
	if b.pool == nil {
		return fmt.Errorf("bootstrap requires a database pool")
	}

	if err := truncateAllApplicationTables(ctx, b.pool); err != nil {
		return err
	}
	return b.Run(ctx)
}

func (b *Bootstrapper) runStep(ctx context.Context, step BootstrapStep, state *BootstrapState) (status StepStatus, message string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			status = StepStatusFailed
			err = fmt.Errorf("panic recovered: %v", recovered)
		}
	}()

	err = step.Fn(ctx, state)
	if err == nil {
		return StepStatusDone, "", nil
	}

	var skipped *skippedStepError
	if errors.As(err, &skipped) {
		return StepStatusSkipped, skipped.reason, nil
	}

	return StepStatusFailed, "", err
}

func (b *Bootstrapper) defaultSteps() []BootstrapStep {
	return []BootstrapStep{
		{Number: 1, Name: "run-migrations", Fn: b.stepRunMigrations},
		{Number: 2, Name: "create-organization", Fn: b.stepCreateOrganization},
		{Number: 3, Name: "create-first-admin", Fn: b.stepCreateFirstAdmin},
		{Number: 4, Name: "seed-default-skills", Fn: b.stepSeedDefaultSkills},
		{Number: 5, Name: "seed-model-profiles-and-assignments", Fn: b.stepSeedModelProfilesAndAssignments},
		{Number: 6, Name: "seed-flow-templates", Fn: b.makeDeferredTableStep("flow_template")},
		{Number: 7, Name: "create-agents", Fn: b.makeDeferredTableStep("agent")},
		{Number: 8, Name: "create-general-session", Fn: b.makeDeferredTableStep("chat_session")},
		{Number: 9, Name: "seed-org-capability-policy", Fn: b.makeDeferredTableStep("capability_policy")},
		{Number: 10, Name: "record-bootstrap-audit-event", Fn: b.stepRecordBootstrapAuditEvent},
	}
}

func (b *Bootstrapper) makeDeferredTableStep(tableName string) func(ctx context.Context, state *BootstrapState) error {
	return func(_ context.Context, _ *BootstrapState) error {
		return skippedf("waiting for table %s to be registered", tableName)
	}
}

func (b *Bootstrapper) stepRunMigrations(ctx context.Context, _ *BootstrapState) error {
	if b.pool == nil {
		return fmt.Errorf("bootstrap requires a database pool")
	}
	runner := migrate.NewRunner(b.pool, b.logger)
	return runner.Run(ctx)
}

func (b *Bootstrapper) stepCreateOrganization(ctx context.Context, state *BootstrapState) error {
	if b.pool == nil {
		return fmt.Errorf("bootstrap requires a database pool")
	}
	if state == nil {
		return fmt.Errorf("bootstrap state is required")
	}

	orgRepo := repo.NewOrgRepo(b.pool)
	existing, exists, err := organizationBySlug(ctx, orgRepo, b.config.OrgSlug)
	if err != nil {
		return err
	}
	if exists {
		state.Organization = existing
		return skippedf("organization %q already exists", b.config.OrgSlug)
	}

	created, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        b.config.OrgSlug,
		DisplayName: b.config.OrgName,
	})
	if err != nil {
		return err
	}

	state.Organization = created
	return nil
}

func (b *Bootstrapper) stepCreateFirstAdmin(ctx context.Context, state *BootstrapState) error {
	if b.pool == nil {
		return fmt.Errorf("bootstrap requires a database pool")
	}
	if state == nil || state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization must be initialized before creating admin user")
	}

	userRepo := repo.NewHumanUserRepo(b.pool)
	exists, err := adminUserExists(ctx, userRepo, state.Organization.ID)
	if err != nil {
		return err
	}
	if exists {
		return skippedf("admin user already exists")
	}

	if strings.TrimSpace(b.config.AdminEmail) == "" && strings.TrimSpace(b.config.AdminPassword) == "" {
		return skippedf("admin credentials not provided")
	}
	if strings.TrimSpace(b.config.AdminEmail) == "" || strings.TrimSpace(b.config.AdminPassword) == "" {
		return fmt.Errorf("both OTTERCAMP_ADMIN_EMAIL and OTTERCAMP_ADMIN_PASSWORD are required")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(b.config.AdminPassword), 12)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}
	passwordHash := string(hashed)

	_, err = userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: state.Organization.ID,
		Email:          strings.TrimSpace(strings.ToLower(b.config.AdminEmail)),
		DisplayName:    "Administrator",
		PasswordHash:   &passwordHash,
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if errors.Is(err, repo.ErrConflict) {
		return skippedf("admin user already exists")
	}
	return err
}

func (b *Bootstrapper) stepSeedDefaultSkills(ctx context.Context, state *BootstrapState) error {
	if b.pool == nil {
		return fmt.Errorf("bootstrap requires a database pool")
	}
	if state == nil || state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization must be initialized before seeding skills")
	}

	if err := os.MkdirAll(b.config.SkillsDir, 0o755); err != nil {
		return fmt.Errorf("create skills directory: %w", err)
	}

	seedSkills, err := loadSeedSkills()
	if err != nil {
		return err
	}

	for _, seedSkill := range seedSkills {
		path := filepath.Join(b.config.SkillsDir, seedSkill.FileName)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create skill parent directory: %w", err)
		}
		if err := os.WriteFile(path, seedSkill.Content, 0o644); err != nil {
			return fmt.Errorf("write skill file %s: %w", path, err)
		}
	}

	rows := make([]repo.Skill, 0, len(seedSkills))
	for _, seedSkill := range seedSkills {
		rows = append(rows, repo.Skill{
			OrganizationID: state.Organization.ID,
			ProjectID:      nil,
			Slug:           seedSkill.Slug,
			DisplayName:    seedSkill.DisplayName,
			Description:    seedSkill.Description,
			FilePath:       filepath.ToSlash(filepath.Join("skills", seedSkill.FileName)),
			Version:        1,
			IsActive:       true,
			CreatedByType:  "system",
			CreatedByID:    audit.SystemPrincipalID,
		})
	}

	_, err = repo.NewSkillRepo(b.pool).BulkUpsertBySlug(ctx, rows)
	return err
}

func (b *Bootstrapper) stepSeedModelProfilesAndAssignments(ctx context.Context, state *BootstrapState) error {
	if b.pool == nil {
		return fmt.Errorf("bootstrap requires a database pool")
	}
	if state == nil || state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization must be initialized before seeding model profiles")
	}

	providerRepo := repo.NewModelProviderRepo(b.pool)
	profileRepo := repo.NewModelProfileRepo(b.pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(b.pool)

	providerSpecs := []providerSeed{
		{
			Slug:              "anthropic",
			DisplayName:       "Anthropic",
			APIBaseURL:        "https://api.anthropic.com",
			SupportedFeatures: []string{"chat.completions", "streaming"},
		},
		{
			Slug:              "openai",
			DisplayName:       "OpenAI",
			APIBaseURL:        "https://api.openai.com/v1",
			SupportedFeatures: []string{"chat.completions", "streaming"},
		},
	}

	providerIDs := make(map[string]uuid.UUID, len(providerSpecs))
	for _, provider := range providerSpecs {
		upserted, err := upsertProviderBySlug(ctx, providerRepo, provider)
		if err != nil {
			return err
		}
		providerIDs[provider.Slug] = upserted.ID
	}

	profileSpecs := []profileSeed{
		{
			LogicalProfileID:    "high-capability",
			ProviderSlug:        "anthropic",
			ModelName:           "claude-opus-4-5",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     8192,
			SupportsStreaming:   true,
			SupportsVision:      true,
			InvocationPurpose:   "agent_turn",
		},
		{
			LogicalProfileID:    "standard",
			ProviderSlug:        "anthropic",
			ModelName:           "claude-sonnet-4-5",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     8192,
			SupportsStreaming:   true,
			SupportsVision:      true,
			InvocationPurpose:   "agent_turn",
		},
		{
			LogicalProfileID:    "haiku",
			ProviderSlug:        "anthropic",
			ModelName:           "claude-haiku-3-5",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     4096,
			SupportsStreaming:   true,
			SupportsVision:      false,
			InvocationPurpose:   "agent_turn",
		},
	}

	for _, profile := range profileSpecs {
		exists, err := orgCurrentProfileExists(ctx, profileRepo, state.Organization.ID, profile.LogicalProfileID)
		if err != nil {
			return err
		}
		if exists {
			continue
		}

		providerID, ok := providerIDs[profile.ProviderSlug]
		if !ok {
			return fmt.Errorf("provider %q not found for model profile %q", profile.ProviderSlug, profile.LogicalProfileID)
		}
		orgID := state.Organization.ID
		_, err = profileRepo.Create(ctx, repo.ModelProfile{
			LogicalProfileID:    profile.LogicalProfileID,
			OrganizationID:      &orgID,
			Version:             1,
			IsCurrent:           true,
			ProviderID:          providerID,
			ModelName:           profile.ModelName,
			ContextWindowTokens: profile.ContextWindowTokens,
			MaxOutputTokens:     profile.MaxOutputTokens,
			SupportsStreaming:   profile.SupportsStreaming,
			SupportsVision:      profile.SupportsVision,
			InvocationPurpose:   profile.InvocationPurpose,
		})
		if err != nil && !errors.Is(err, repo.ErrConflict) {
			return err
		}
	}

	assignments := []assignmentSeed{
		{InvocationPurpose: "agent_turn", LogicalProfileID: "high-capability"},
		{InvocationPurpose: "listening_eval", LogicalProfileID: "haiku"},
		{InvocationPurpose: "summarization", LogicalProfileID: "standard"},
		{InvocationPurpose: "memory_extraction", LogicalProfileID: "haiku"},
		{InvocationPurpose: "memory_distillation", LogicalProfileID: "haiku"},
		{InvocationPurpose: "memory_entity_synthesis", LogicalProfileID: "haiku"},
	}

	for _, assignment := range assignments {
		_, err := assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
			OrganizationID:    state.Organization.ID,
			ScopeType:         "organization",
			ScopeID:           state.Organization.ID,
			LogicalProfileID:  assignment.LogicalProfileID,
			InvocationPurpose: assignment.InvocationPurpose,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (b *Bootstrapper) stepRecordBootstrapAuditEvent(ctx context.Context, state *BootstrapState) error {
	if b.pool == nil {
		return fmt.Errorf("bootstrap requires a database pool")
	}
	if state == nil || state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization must be initialized before recording bootstrap audit event")
	}

	exists, err := bootstrapAuditEventExists(ctx, b.pool, state.Organization.ID)
	if err != nil {
		return err
	}
	if exists {
		return skippedf("already bootstrapped")
	}

	service := audit.NewService(repo.NewAuditEventRepo(b.pool), b.logger)
	return service.Record(ctx, audit.Event{
		OrgID:         state.Organization.ID,
		EventType:     bootstrapEventType,
		PrincipalType: "system",
		PrincipalID:   audit.SystemPrincipalID,
		Metadata: map[string]any{
			"version":    b.version,
			"step_count": bootstrapStepCount,
			"timestamp":  b.now().UTC().Format(time.RFC3339Nano),
		},
	})
}

func normalizeConfig(cfg Config) Config {
	if cfg == (Config{}) {
		cfg = ConfigFromEnv()
	}

	cfg.OrgSlug = strings.TrimSpace(cfg.OrgSlug)
	if cfg.OrgSlug == "" {
		cfg.OrgSlug = defaultOrgSlug
	}

	cfg.OrgName = strings.TrimSpace(cfg.OrgName)
	if cfg.OrgName == "" {
		cfg.OrgName = defaultOrgName
	}

	cfg.AdminEmail = strings.TrimSpace(cfg.AdminEmail)

	cfg.SkillsDir = strings.TrimSpace(cfg.SkillsDir)
	if cfg.SkillsDir == "" {
		cfg.SkillsDir = defaultSkillsDir
	}

	return cfg
}

type providerSeed struct {
	Slug              string
	DisplayName       string
	APIBaseURL        string
	SupportedFeatures []string
}

func upsertProviderBySlug(ctx context.Context, providerRepo *repo.ModelProviderRepo, seed providerSeed) (repo.ModelProvider, error) {
	existing, err := providerRepo.GetBySlug(ctx, seed.Slug)
	if errors.Is(err, repo.ErrNotFound) {
		return providerRepo.Create(ctx, repo.ModelProvider{
			Slug:              seed.Slug,
			DisplayName:       seed.DisplayName,
			APIBaseURL:        seed.APIBaseURL,
			SupportedFeatures: seed.SupportedFeatures,
			IsEnabled:         true,
		})
	}
	if err != nil {
		return repo.ModelProvider{}, err
	}
	if !existing.IsEnabled {
		return providerRepo.SetEnabled(ctx, existing.ID, true)
	}
	return existing, nil
}

type profileSeed struct {
	LogicalProfileID    string
	ProviderSlug        string
	ModelName           string
	ContextWindowTokens int
	MaxOutputTokens     int
	SupportsStreaming   bool
	SupportsVision      bool
	InvocationPurpose   string
}

func orgCurrentProfileExists(ctx context.Context, repo *repo.ModelProfileRepo, orgID uuid.UUID, logicalProfileID string) (bool, error) {
	profiles, err := repo.ListCurrent(ctx, orgID)
	if err != nil {
		return false, err
	}
	for _, profile := range profiles {
		if profile.OrganizationID == nil {
			continue
		}
		if *profile.OrganizationID != orgID {
			continue
		}
		if profile.LogicalProfileID == logicalProfileID {
			return true, nil
		}
	}
	return false, nil
}

type assignmentSeed struct {
	InvocationPurpose string
	LogicalProfileID  string
}

type seedSkill struct {
	Slug        string
	DisplayName string
	Description string
	FileName    string
	Content     []byte
}

type seedSkillSpec struct {
	Slug        string
	DisplayName string
	Description string
	FileName    string
}

func loadSeedSkills() ([]seedSkill, error) {
	specs := []seedSkillSpec{
		{
			Slug:        "summarize",
			DisplayName: "Summarize",
			Description: "Create concise summaries that preserve critical context and action items.",
			FileName:    "summarize.md",
		},
		{
			Slug:        "code-review",
			DisplayName: "Code Review",
			Description: "Review code for correctness, regressions, and missing tests.",
			FileName:    "code-review.md",
		},
		{
			Slug:        "plan-task",
			DisplayName: "Plan Task",
			Description: "Break work into ordered, testable implementation steps.",
			FileName:    "plan-task.md",
		},
	}

	seeds := make([]seedSkill, 0, len(specs))
	for _, spec := range specs {
		assetPath := filepath.ToSlash(filepath.Join("defaults", "skills", spec.FileName))
		content, err := defaultSkillAssets.ReadFile(assetPath)
		if err != nil {
			return nil, fmt.Errorf("load embedded skill %s: %w", assetPath, err)
		}
		seeds = append(seeds, seedSkill{
			Slug:        spec.Slug,
			DisplayName: spec.DisplayName,
			Description: spec.Description,
			FileName:    spec.FileName,
			Content:     content,
		})
	}

	sort.Slice(seeds, func(i, j int) bool {
		return seeds[i].Slug < seeds[j].Slug
	})
	return seeds, nil
}

type orgLookup interface {
	GetBySlug(ctx context.Context, slug string) (repo.Organization, error)
}

func organizationBySlug(ctx context.Context, lookup orgLookup, slug string) (repo.Organization, bool, error) {
	org, err := lookup.GetBySlug(ctx, slug)
	if errors.Is(err, repo.ErrNotFound) {
		return repo.Organization{}, false, nil
	}
	if err != nil {
		return repo.Organization{}, false, err
	}
	return org, true, nil
}

type userLister interface {
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
}

func adminUserExists(ctx context.Context, users userLister, orgID uuid.UUID) (bool, error) {
	list, err := users.List(ctx, orgID)
	if err != nil {
		return false, err
	}
	for _, user := range list {
		if user.Role == "admin" {
			return true, nil
		}
	}
	return false, nil
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func bootstrapAuditEventExists(ctx context.Context, queryer queryRower, organizationID uuid.UUID) (bool, error) {
	var found int
	err := queryer.QueryRow(ctx, `
		SELECT 1
		FROM audit_event
		WHERE event_type = 'system.bootstrap'
		  AND organization_id = $1
		LIMIT 1
	`, organizationID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func truncateAllApplicationTables(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("database pool is nil")
	}

	rows, err := pool.Query(ctx, `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename <> 'schema_migrations'
		ORDER BY tablename
	`)
	if err != nil {
		return fmt.Errorf("list application tables: %w", err)
	}
	defer rows.Close()

	tables := make([]string, 0)
	for rows.Next() {
		var table string
		if scanErr := rows.Scan(&table); scanErr != nil {
			return fmt.Errorf("scan table name: %w", scanErr)
		}
		tables = append(tables, pgx.Identifier{table}.Sanitize())
	}
	if rows.Err() != nil {
		return fmt.Errorf("iterate application tables: %w", rows.Err())
	}
	if len(tables) == 0 {
		return nil
	}

	statement := fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))
	if _, err := pool.Exec(ctx, statement); err != nil {
		return fmt.Errorf("truncate tables: %w", err)
	}
	return nil
}
