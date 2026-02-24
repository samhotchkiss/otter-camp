package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

const (
	defaultOrgSlug = "default"
	defaultOrgName = "OtterCamp"

	stepCount = 10
)

type MigrationRunner interface {
	Run(ctx context.Context) error
}

type OrganizationStore interface {
	GetBySlug(ctx context.Context, slug string) (repo.Organization, error)
	Create(ctx context.Context, org repo.Organization) (repo.Organization, error)
}

type UserStore interface {
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
	Create(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
}

type SkillStore interface {
	BulkUpsertBySlug(ctx context.Context, skills []repo.Skill) ([]repo.Skill, error)
}

type ProviderStore interface {
	GetBySlug(ctx context.Context, slug string) (repo.ModelProvider, error)
	Create(ctx context.Context, provider repo.ModelProvider) (repo.ModelProvider, error)
}

type ProfileStore interface {
	GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error)
	Create(ctx context.Context, profile repo.ModelProfile) (repo.ModelProfile, error)
}

type AssignmentStore interface {
	Upsert(ctx context.Context, assignment repo.ModelProfileAssignment) (repo.ModelProfileAssignment, error)
}

type AuditRecorder interface {
	Record(ctx context.Context, event audit.Event) error
}

type BootstrapAuditChecker interface {
	ExistsBootstrapEvent(ctx context.Context, organizationID uuid.UUID) (bool, error)
}

type BootstrapStepFn func(ctx context.Context, state *BootstrapState) error

type bootstrapStep struct {
	number int
	name   string
	fn     BootstrapStepFn
}

type BootstrapState struct {
	Organization repo.Organization
	AdminUser    *repo.HumanUser
	Providers    map[string]repo.ModelProvider
}

type Options struct {
	Pool *pgxpool.Pool

	Store storage.Store

	Logger  *slog.Logger
	Version string

	MigrationRunner MigrationRunner
	Organizations   OrganizationStore
	Users           UserStore
	Skills          SkillStore
	Providers       ProviderStore
	Profiles        ProfileStore
	Assignments     AssignmentStore
	AuditRecorder   AuditRecorder
	AuditChecker    BootstrapAuditChecker
}

type Bootstrapper struct {
	pool    *pgxpool.Pool
	store   storage.Store
	logger  *slog.Logger
	version string

	migrations    MigrationRunner
	organizations OrganizationStore
	users         UserStore
	skills        SkillStore
	providers     ProviderStore
	profiles      ProfileStore
	assignments   AssignmentStore
	auditRecorder AuditRecorder
	auditChecker  BootstrapAuditChecker

	steps []bootstrapStep
}

func New(opts Options) (*Bootstrapper, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("bootstrap requires a database pool")
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	storeImpl := opts.Store
	if storeImpl == nil {
		created, err := storage.New(storage.ConfigFromEnv(os.LookupEnv))
		if err != nil {
			return nil, fmt.Errorf("create storage store: %w", err)
		}
		storeImpl = created
	}

	migrations := opts.MigrationRunner
	if migrations == nil {
		migrations = migrate.NewRunner(opts.Pool, logger)
	}

	organizations := opts.Organizations
	if organizations == nil {
		organizations = repo.NewOrgRepo(opts.Pool)
	}

	users := opts.Users
	if users == nil {
		users = repo.NewHumanUserRepo(opts.Pool)
	}

	skills := opts.Skills
	if skills == nil {
		skills = repo.NewSkillRepo(opts.Pool)
	}

	providers := opts.Providers
	if providers == nil {
		providers = repo.NewModelProviderRepo(opts.Pool)
	}

	profiles := opts.Profiles
	if profiles == nil {
		profiles = repo.NewModelProfileRepo(opts.Pool)
	}

	assignments := opts.Assignments
	if assignments == nil {
		assignments = repo.NewModelProfileAssignmentRepo(opts.Pool)
	}

	auditRecorder := opts.AuditRecorder
	if auditRecorder == nil {
		auditRecorder = audit.NewService(repo.NewAuditEventRepo(opts.Pool), logger)
	}

	auditChecker := opts.AuditChecker
	if auditChecker == nil {
		auditChecker = &repoBootstrapAuditChecker{pool: opts.Pool}
	}

	b := &Bootstrapper{
		pool:          opts.Pool,
		store:         storeImpl,
		logger:        logger,
		version:       strings.TrimSpace(opts.Version),
		migrations:    migrations,
		organizations: organizations,
		users:         users,
		skills:        skills,
		providers:     providers,
		profiles:      profiles,
		assignments:   assignments,
		auditRecorder: auditRecorder,
		auditChecker:  auditChecker,
	}
	b.steps = b.defaultSteps()
	return b, nil
}

func (b *Bootstrapper) RegisterStep(name string, fn BootstrapStepFn) {
	if b == nil || fn == nil {
		return
	}

	normalizedName := strings.TrimSpace(name)
	for i := range b.steps {
		if b.steps[i].name == normalizedName {
			b.steps[i].fn = fn
			return
		}
	}

	b.steps = append(b.steps, bootstrapStep{
		number: len(b.steps) + 1,
		name:   normalizedName,
		fn:     fn,
	})
}

func (b *Bootstrapper) Run(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}

	state := &BootstrapState{Providers: make(map[string]repo.ModelProvider)}
	for _, step := range b.steps {
		if err := b.runStep(ctx, step, state); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bootstrapper) Reset(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}
	if err := b.truncateAllTables(ctx); err != nil {
		return err
	}
	return b.Run(ctx)
}

func (b *Bootstrapper) runStep(ctx context.Context, step bootstrapStep, state *BootstrapState) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("bootstrap step %d (%s) panicked: %v", step.number, step.name, recovered)
		}
	}()

	if step.fn == nil {
		return fmt.Errorf("bootstrap step %d (%s) has no implementation", step.number, step.name)
	}
	if err := step.fn(ctx, state); err != nil {
		return fmt.Errorf("bootstrap step %d (%s): %w", step.number, step.name, err)
	}
	return nil
}

func (b *Bootstrapper) defaultSteps() []bootstrapStep {
	return []bootstrapStep{
		{number: 1, name: "run-migrations", fn: b.stepRunMigrations},
		{number: 2, name: "create-organization", fn: b.stepCreateOrganization},
		{number: 3, name: "create-admin-user", fn: b.stepCreateAdminUser},
		{number: 4, name: "seed-default-skills", fn: b.stepSeedDefaultSkills},
		{number: 5, name: "seed-models-and-assignments", fn: b.stepSeedModelsAndAssignments},
		{number: 6, name: "seed-flow-templates", fn: b.stubStepFn(6, "seed-flow-templates", "flow_template")},
		{number: 7, name: "create-agents", fn: b.stubStepFn(7, "create-agents", "agent")},
		{number: 8, name: "create-general-session", fn: b.stubStepFn(8, "create-general-session", "chat_session")},
		{number: 9, name: "seed-org-capability-policy", fn: b.stubStepFn(9, "seed-org-capability-policy", "capability_policy")},
		{number: 10, name: "record-bootstrap-audit", fn: b.stepRecordBootstrapAuditEvent},
	}
}

func (b *Bootstrapper) stepRunMigrations(ctx context.Context, _ *BootstrapState) error {
	if b.migrations == nil {
		return fmt.Errorf("migration runner is not configured")
	}
	if err := b.migrations.Run(ctx); err != nil {
		return err
	}
	b.logger.Info("bootstrap step 1 (run-migrations): done")
	return nil
}

func (b *Bootstrapper) stepCreateOrganization(ctx context.Context, state *BootstrapState) error {
	slug := getenvDefault("OTTERCAMP_ORG_SLUG", defaultOrgSlug)
	displayName := getenvDefault("OTTERCAMP_ORG_NAME", defaultOrgName)

	org, exists, err := b.organizationBySlug(ctx, slug)
	if err != nil {
		return err
	}
	if !exists {
		org, err = b.organizations.Create(ctx, repo.Organization{Slug: slug, DisplayName: displayName})
		if err != nil {
			return err
		}
	}

	state.Organization = org
	b.logger.Info("bootstrap step 2 (create-organization): done", "organization_id", org.ID, "slug", org.Slug)
	return nil
}

func (b *Bootstrapper) stepCreateAdminUser(ctx context.Context, state *BootstrapState) error {
	if state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization must be available before creating admin user")
	}

	adminUser, exists, err := b.adminUserForOrg(ctx, state.Organization.ID)
	if err != nil {
		return err
	}
	if exists {
		state.AdminUser = &adminUser
		b.logger.Info("bootstrap step 3 (create-admin-user): done", "status", "already_exists", "user_id", adminUser.ID)
		return nil
	}

	email := strings.TrimSpace(os.Getenv("OTTERCAMP_ADMIN_EMAIL"))
	password := strings.TrimSpace(os.Getenv("OTTERCAMP_ADMIN_PASSWORD"))

	if email == "" && password == "" {
		b.logger.Info("bootstrap step 3 (create-admin-user): skipped - OTTERCAMP_ADMIN_EMAIL and OTTERCAMP_ADMIN_PASSWORD are not set")
		return nil
	}
	if email == "" || password == "" {
		return fmt.Errorf("both OTTERCAMP_ADMIN_EMAIL and OTTERCAMP_ADMIN_PASSWORD are required when either is set")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	passwordHash := string(hash)

	created, err := b.users.Create(ctx, repo.HumanUser{
		OrganizationID: state.Organization.ID,
		Email:          email,
		DisplayName:    email,
		PasswordHash:   &passwordHash,
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		return err
	}

	state.AdminUser = &created
	b.logger.Info("bootstrap step 3 (create-admin-user): done", "user_id", created.ID, "email", created.Email)
	return nil
}

func (b *Bootstrapper) stepSeedDefaultSkills(ctx context.Context, state *BootstrapState) error {
	if state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization must be available before seeding skills")
	}
	if b.store == nil {
		return fmt.Errorf("storage store is not configured")
	}

	embeddedSkills, err := loadDefaultSkills()
	if err != nil {
		return err
	}

	skills := make([]repo.Skill, 0, len(embeddedSkills))
	for _, skill := range embeddedSkills {
		storePath := path.Join("skills", skill.FileName)
		if err := b.store.Put(ctx, storePath, bytes.NewReader(skill.Content), storage.PutOptions{
			ContentType:   "text/markdown",
			ContentLength: int64(len(skill.Content)),
		}); err != nil {
			return fmt.Errorf("write default skill %s: %w", skill.Slug, err)
		}

		skills = append(skills, repo.Skill{
			OrganizationID: state.Organization.ID,
			ProjectID:      nil,
			Slug:           skill.Slug,
			DisplayName:    skill.DisplayName,
			Description:    skill.Description,
			FilePath:       storePath,
			Version:        1,
			IsActive:       true,
			CreatedByType:  "system",
			CreatedByID:    audit.SystemPrincipalID,
		})
	}

	if _, err := b.skills.BulkUpsertBySlug(ctx, skills); err != nil {
		return err
	}

	b.logger.Info("bootstrap step 4 (seed-default-skills): done", "skills_seeded", len(skills))
	return nil
}

func (b *Bootstrapper) stepSeedModelsAndAssignments(ctx context.Context, state *BootstrapState) error {
	if state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization must be available before seeding models")
	}

	anthropic, err := b.ensureProvider(ctx, "anthropic", "Anthropic", "https://api.anthropic.com")
	if err != nil {
		return err
	}
	openai, err := b.ensureProvider(ctx, "openai", "OpenAI", "https://api.openai.com/v1")
	if err != nil {
		return err
	}
	state.Providers[anthropic.Slug] = anthropic
	state.Providers[openai.Slug] = openai

	highTemperature := 0.7
	haikuTemperature := 0.3
	fallbackToHigh := "high-capability"

	if _, err := b.ensureProfile(ctx, state.Organization.ID, repo.ModelProfile{
		LogicalProfileID:    "high-capability",
		OrganizationID:      ptrUUID(state.Organization.ID),
		Version:             1,
		IsCurrent:           true,
		ProviderID:          anthropic.ID,
		ModelName:           "claude-opus-4-5",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      true,
		Temperature:         &highTemperature,
		InvocationPurpose:   "agent_turn",
		FallbackProfileID:   nil,
	}); err != nil {
		return err
	}

	if _, err := b.ensureProfile(ctx, state.Organization.ID, repo.ModelProfile{
		LogicalProfileID:    "standard",
		OrganizationID:      ptrUUID(state.Organization.ID),
		Version:             1,
		IsCurrent:           true,
		ProviderID:          anthropic.ID,
		ModelName:           "claude-sonnet-4-5",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      true,
		Temperature:         &highTemperature,
		InvocationPurpose:   "agent_turn",
		FallbackProfileID:   &fallbackToHigh,
	}); err != nil {
		return err
	}

	if _, err := b.ensureProfile(ctx, state.Organization.ID, repo.ModelProfile{
		LogicalProfileID:    "haiku",
		OrganizationID:      ptrUUID(state.Organization.ID),
		Version:             1,
		IsCurrent:           true,
		ProviderID:          anthropic.ID,
		ModelName:           "claude-haiku-3-5",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     4096,
		SupportsStreaming:   false,
		SupportsVision:      false,
		Temperature:         &haikuTemperature,
		InvocationPurpose:   "agent_turn",
		FallbackProfileID:   nil,
	}); err != nil {
		return err
	}

	// model_profile_assignment enforces one profile per invocation purpose at org scope.
	assignments := map[string]string{
		"agent_turn":              "high-capability",
		"listening_eval":          "haiku",
		"summarization":           "standard",
		"skill_summarization":     "standard",
		"memory_extraction":       "haiku",
		"memory_distillation":     "haiku",
		"memory_entity_synthesis": "haiku",
		"replay":                  "standard",
	}

	purposes := make([]string, 0, len(assignments))
	for purpose := range assignments {
		purposes = append(purposes, purpose)
	}
	sort.Strings(purposes)

	for _, purpose := range purposes {
		if _, err := b.assignments.Upsert(ctx, repo.ModelProfileAssignment{
			OrganizationID:    state.Organization.ID,
			ScopeType:         "organization",
			ScopeID:           state.Organization.ID,
			LogicalProfileID:  assignments[purpose],
			InvocationPurpose: purpose,
		}); err != nil {
			return err
		}
	}

	b.logger.Info("bootstrap step 5 (seed-models-and-assignments): done", "assignments", len(purposes))
	return nil
}

func (b *Bootstrapper) stubStepFn(number int, name, table string) BootstrapStepFn {
	return func(context.Context, *BootstrapState) error {
		b.logger.Info(fmt.Sprintf("bootstrap step %d (%s): skipped - waiting for table %s to be registered", number, name, table))
		return nil
	}
}

func (b *Bootstrapper) stepRecordBootstrapAuditEvent(ctx context.Context, state *BootstrapState) error {
	if state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization must be available before recording bootstrap event")
	}

	alreadyBootstrapped, err := b.bootstrapAuditEventExists(ctx, state.Organization.ID)
	if err != nil {
		return err
	}
	if alreadyBootstrapped {
		b.logger.Info("bootstrap step 10 (record-bootstrap-audit): skipped - already bootstrapped")
		return nil
	}

	if err := b.auditRecorder.Record(ctx, audit.Event{
		OrgID:         state.Organization.ID,
		EventType:     "system.bootstrap",
		PrincipalType: "system",
		PrincipalID:   audit.SystemPrincipalID,
		Metadata: map[string]any{
			"version":    b.version,
			"step_count": stepCount,
			"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
		},
	}); err != nil {
		return err
	}

	b.logger.Info("bootstrap step 10 (record-bootstrap-audit): done")
	return nil
}

func (b *Bootstrapper) organizationBySlug(ctx context.Context, slug string) (repo.Organization, bool, error) {
	org, err := b.organizations.GetBySlug(ctx, strings.TrimSpace(slug))
	if errors.Is(err, repo.ErrNotFound) {
		return repo.Organization{}, false, nil
	}
	if err != nil {
		return repo.Organization{}, false, err
	}
	return org, true, nil
}

func (b *Bootstrapper) adminUserForOrg(ctx context.Context, orgID uuid.UUID) (repo.HumanUser, bool, error) {
	users, err := b.users.List(ctx, orgID)
	if err != nil {
		return repo.HumanUser{}, false, err
	}
	for _, user := range users {
		if user.Role == "admin" {
			return user, true, nil
		}
	}
	return repo.HumanUser{}, false, nil
}

func (b *Bootstrapper) ensureProvider(ctx context.Context, slug, displayName, apiBaseURL string) (repo.ModelProvider, error) {
	found, err := b.providers.GetBySlug(ctx, slug)
	if errors.Is(err, repo.ErrNotFound) {
		return b.providers.Create(ctx, repo.ModelProvider{
			Slug:        slug,
			DisplayName: displayName,
			APIBaseURL:  apiBaseURL,
			IsEnabled:   true,
		})
	}
	if err != nil {
		return repo.ModelProvider{}, err
	}
	return found, nil
}

func (b *Bootstrapper) ensureProfile(ctx context.Context, orgID uuid.UUID, profile repo.ModelProfile) (repo.ModelProfile, error) {
	found, err := b.profiles.GetCurrentByLogicalID(ctx, orgID, profile.LogicalProfileID)
	if errors.Is(err, repo.ErrNotFound) {
		return b.profiles.Create(ctx, profile)
	}
	if err != nil {
		return repo.ModelProfile{}, err
	}
	return found, nil
}

func (b *Bootstrapper) bootstrapAuditEventExists(ctx context.Context, orgID uuid.UUID) (bool, error) {
	if b.auditChecker == nil {
		return false, fmt.Errorf("bootstrap audit checker is not configured")
	}
	return b.auditChecker.ExistsBootstrapEvent(ctx, orgID)
}

func (b *Bootstrapper) truncateAllTables(ctx context.Context) error {
	rows, err := b.pool.Query(ctx, `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename <> 'schema_migrations'
		ORDER BY tablename
	`)
	if err != nil {
		return fmt.Errorf("list tables for reset: %w", err)
	}
	defer rows.Close()

	tables := make([]string, 0, 32)
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table names: %w", err)
	}
	if len(tables) == 0 {
		return nil
	}

	quoted := make([]string, 0, len(tables))
	for _, table := range tables {
		quoted = append(quoted, pgx.Identifier{table}.Sanitize())
	}

	statement := fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", strings.Join(quoted, ", "))
	if _, err := b.pool.Exec(ctx, statement); err != nil {
		return fmt.Errorf("truncate tables: %w", err)
	}
	return nil
}

func getenvDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func ptrUUID(id uuid.UUID) *uuid.UUID {
	return &id
}

type repoBootstrapAuditChecker struct {
	pool *pgxpool.Pool
}

func (r *repoBootstrapAuditChecker) ExistsBootstrapEvent(ctx context.Context, organizationID uuid.UUID) (bool, error) {
	if r == nil || r.pool == nil {
		return false, fmt.Errorf("audit checker pool is not configured")
	}

	var marker int
	err := r.pool.QueryRow(ctx, `
		SELECT 1
		FROM audit_event
		WHERE event_type = 'system.bootstrap'
		  AND organization_id = $1
		LIMIT 1
	`, organizationID).Scan(&marker)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
