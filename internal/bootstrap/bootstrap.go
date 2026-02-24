package bootstrap

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	defaultOrgSlug    = "default"
	defaultOrgName    = "OtterCamp"
	defaultSkillsDir  = "skills"
	bootstrapEvent    = "system.bootstrap"
	stepCount         = 10
	organizationScope = "organization"
)

var systemActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

//go:embed defaults/skills/*.md
var defaultSkillAssets embed.FS

type BootstrapState struct {
	OrganizationID uuid.UUID
}

// State is kept as an alias for compatibility with already-written integrations.
type State = BootstrapState

type StepFunc func(ctx context.Context, state *BootstrapState) error

type BootstrapStep struct {
	Number int
	Name   string
	fn     StepFunc
}

type MigrationRunner interface {
	Run(ctx context.Context) error
}

type AuditRecorder interface {
	Record(ctx context.Context, event audit.Event) error
}

type Options struct {
	Pool            *pgxpool.Pool
	Logger          *slog.Logger
	MigrationRunner MigrationRunner
	AuditRecorder   AuditRecorder
	OrgSlug         string
	OrgName         string
	AdminEmail      string
	AdminPassword   string
	SkillsDir       string
	SkillAssetFS    fs.FS
	AppVersion      string
	ProgressWriter  io.Writer
	Now             func() time.Time
}

type Bootstrapper struct {
	mu sync.RWMutex

	logger         *slog.Logger
	pool           *pgxpool.Pool
	migration      MigrationRunner
	auditRecorder  AuditRecorder
	orgRepo        *repo.OrgRepo
	userRepo       *repo.HumanUserRepo
	skillRepo      *repo.SkillRepo
	providerRepo   *repo.ModelProviderRepo
	profileRepo    *repo.ModelProfileRepo
	assignmentRepo *repo.ModelProfileAssignmentRepo

	orgSlug       string
	orgName       string
	adminEmail    string
	adminPassword string
	skillsDir     string
	skillAssetFS  fs.FS
	appVersion    string
	progress      io.Writer
	now           func() time.Time

	steps []BootstrapStep
}

type defaultSkillSeed struct {
	Slug        string
	DisplayName string
	Description string
	FileName    string
	Content     []byte
}

type modelProfileSeed struct {
	LogicalProfileID string
	ModelName        string
	ProviderSlug     string
}

type assignmentSeed struct {
	InvocationPurpose string
	LogicalProfileID  string
}

func NewBootstrapper(opts Options) *Bootstrapper {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	skillAssetFS := opts.SkillAssetFS
	if skillAssetFS == nil {
		skillAssetFS = defaultSkillAssets
	}

	b := &Bootstrapper{
		logger:        logger,
		pool:          opts.Pool,
		migration:     opts.MigrationRunner,
		auditRecorder: opts.AuditRecorder,
		orgSlug:       firstNonEmpty(opts.OrgSlug, os.Getenv("OTTERCAMP_ORG_SLUG"), defaultOrgSlug),
		orgName:       firstNonEmpty(opts.OrgName, os.Getenv("OTTERCAMP_ORG_NAME"), defaultOrgName),
		adminEmail:    firstNonEmpty(opts.AdminEmail, os.Getenv("OTTERCAMP_ADMIN_EMAIL")),
		adminPassword: firstNonEmpty(opts.AdminPassword, os.Getenv("OTTERCAMP_ADMIN_PASSWORD")),
		skillsDir:     firstNonEmpty(opts.SkillsDir, os.Getenv("OTTERCAMP_SKILLS_DIR"), defaultSkillsDir),
		skillAssetFS:  skillAssetFS,
		appVersion:    firstNonEmpty(opts.AppVersion, "dev"),
		progress:      opts.ProgressWriter,
		now:           now,
	}

	if b.pool != nil {
		b.orgRepo = repo.NewOrgRepo(b.pool)
		b.userRepo = repo.NewHumanUserRepo(b.pool)
		b.skillRepo = repo.NewSkillRepo(b.pool)
		b.providerRepo = repo.NewModelProviderRepo(b.pool)
		b.profileRepo = repo.NewModelProfileRepo(b.pool)
		b.assignmentRepo = repo.NewModelProfileAssignmentRepo(b.pool)
		if b.migration == nil {
			b.migration = migrate.NewRunner(b.pool, b.logger)
		}
		if b.auditRecorder == nil {
			b.auditRecorder = audit.NewService(repo.NewAuditEventRepo(b.pool), b.logger)
		}
	}

	b.steps = b.defaultSteps()
	return b
}

func (b *Bootstrapper) defaultSteps() []BootstrapStep {
	return []BootstrapStep{
		{Number: 1, Name: "run-migrations", fn: b.stepRunMigrations},
		{Number: 2, Name: "create-organization", fn: b.stepCreateOrganization},
		{Number: 3, Name: "create-first-human-user", fn: b.stepCreateFirstHumanUser},
		{Number: 4, Name: "seed-default-skills", fn: b.stepSeedDefaultSkills},
		{Number: 5, Name: "seed-model-registry", fn: b.stepSeedModelRegistry},
		{Number: 6, Name: "seed-default-flow-templates", fn: b.stubStep(6, "seed-default-flow-templates", "flow_template")},
		{Number: 7, Name: "create-agents", fn: b.stubStep(7, "create-agents", "agent")},
		{Number: 8, Name: "create-general-session", fn: b.stubStep(8, "create-general-session", "chat_session")},
		{Number: 9, Name: "seed-capability-policy", fn: b.stubStep(9, "seed-capability-policy", "capability_policy")},
		{Number: 10, Name: "record-bootstrap-audit-event", fn: b.stepRecordBootstrapAuditEvent},
	}
}

func (b *Bootstrapper) RegisterStep(name string, fn StepFunc) {
	if b == nil || strings.TrimSpace(name) == "" || fn == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	normalized := strings.TrimSpace(name)
	for i := range b.steps {
		if b.steps[i].Name == normalized {
			b.steps[i].fn = fn
			return
		}
	}

	b.steps = append(b.steps, BootstrapStep{Number: len(b.steps) + 1, Name: normalized, fn: fn})
}

func (b *Bootstrapper) Run(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	state := &BootstrapState{}
	for _, step := range b.snapshot() {
		err := runStep(ctx, step.fn, state)
		if err == nil {
			b.report(step, "done", "")
			continue
		}

		var skipped *stepSkippedError
		if errors.As(err, &skipped) {
			b.report(step, "skipped", skipped.reason)
			continue
		}
		return fmt.Errorf("bootstrap step %d (%s): %w", step.Number, step.Name, err)
	}

	return nil
}

func (b *Bootstrapper) Reset(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}
	if b.pool == nil {
		return fmt.Errorf("database pool is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if err := b.truncateApplicationTables(ctx); err != nil {
		return err
	}
	return b.Run(ctx)
}

func (b *Bootstrapper) StepNames() []string {
	if b == nil {
		return nil
	}

	names := make([]string, 0, len(b.steps))
	for _, step := range b.snapshot() {
		names = append(names, step.Name)
	}
	return names
}

func (b *Bootstrapper) snapshot() []BootstrapStep {
	b.mu.RLock()
	defer b.mu.RUnlock()

	copied := make([]BootstrapStep, len(b.steps))
	copy(copied, b.steps)
	return copied
}

func (b *Bootstrapper) report(step BootstrapStep, status, detail string) {
	message := fmt.Sprintf("bootstrap step %d (%s): %s", step.Number, step.Name, status)
	if status == "skipped" && strings.TrimSpace(detail) != "" {
		message = fmt.Sprintf("bootstrap step %d (%s): skipped — %s", step.Number, step.Name, strings.TrimSpace(detail))
	}

	b.logger.Info(message, "step", step.Number, "name", step.Name, "status", status)
	if b.progress != nil {
		_, _ = fmt.Fprintln(b.progress, message)
	}
}

func runStep(ctx context.Context, fn StepFunc, state *BootstrapState) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return fn(ctx, state)
}

func (b *Bootstrapper) stubStep(stepNumber int, stepName, tableName string) StepFunc {
	return func(context.Context, *BootstrapState) error {
		return skipStep(fmt.Sprintf("waiting for table %s to be registered", tableName))
	}
}

func (b *Bootstrapper) stepRunMigrations(ctx context.Context, _ *BootstrapState) error {
	if b.migration == nil {
		return fmt.Errorf("migration runner is not configured")
	}
	return b.migration.Run(ctx)
}

func (b *Bootstrapper) stepCreateOrganization(ctx context.Context, state *BootstrapState) error {
	if b.orgRepo == nil {
		return fmt.Errorf("organization repository is not configured")
	}

	org, err := b.orgRepo.GetBySlug(ctx, b.orgSlug)
	if err == nil {
		state.OrganizationID = org.ID
		return skipStep("organization already exists")
	}
	if !errors.Is(err, repo.ErrNotFound) {
		return err
	}

	created, err := b.orgRepo.Create(ctx, repo.Organization{
		Slug:        b.orgSlug,
		DisplayName: b.orgName,
	})
	if err != nil {
		return err
	}
	state.OrganizationID = created.ID
	return nil
}

func (b *Bootstrapper) stepCreateFirstHumanUser(ctx context.Context, state *BootstrapState) error {
	if b.userRepo == nil {
		return fmt.Errorf("human user repository is not configured")
	}

	orgID, err := requiredOrganizationID(state)
	if err != nil {
		return err
	}

	email := strings.TrimSpace(b.adminEmail)
	password := strings.TrimSpace(b.adminPassword)
	if email == "" && password == "" {
		return skipStep("admin credentials not provided")
	}
	if email == "" || password == "" {
		return fmt.Errorf("OTTERCAMP_ADMIN_EMAIL and OTTERCAMP_ADMIN_PASSWORD must both be set")
	}

	exists, err := b.adminUserExists(ctx, orgID)
	if err != nil {
		return err
	}
	if exists {
		return skipStep("admin user already exists")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	passwordHash := string(hash)

	_, err = b.userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: orgID,
		Email:          strings.ToLower(email),
		DisplayName:    "Administrator",
		PasswordHash:   &passwordHash,
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	return err
}

func (b *Bootstrapper) stepSeedDefaultSkills(ctx context.Context, state *BootstrapState) error {
	if b.skillRepo == nil {
		return fmt.Errorf("skill repository is not configured")
	}

	orgID, err := requiredOrganizationID(state)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(b.skillsDir, 0o755); err != nil {
		return fmt.Errorf("create skills directory: %w", err)
	}

	seeds, err := loadDefaultSkillSeeds(b.skillAssetFS)
	if err != nil {
		return err
	}

	toSeed := make([]repo.Skill, 0, len(seeds))
	for _, seed := range seeds {
		if err := writeSkillFile(b.skillsDir, seed); err != nil {
			return err
		}

		_, getErr := b.skillRepo.GetBySlug(ctx, orgID, nil, seed.Slug)
		if getErr == nil {
			continue
		}
		if !errors.Is(getErr, repo.ErrNotFound) {
			return getErr
		}

		toSeed = append(toSeed, repo.Skill{
			OrganizationID: orgID,
			Slug:           seed.Slug,
			DisplayName:    seed.DisplayName,
			Description:    seed.Description,
			FilePath:       filepath.ToSlash(filepath.Join("skills", seed.FileName)),
			Version:        1,
			IsActive:       true,
			CreatedByType:  "system",
			CreatedByID:    systemActorID,
		})
	}

	if len(toSeed) == 0 {
		return skipStep("default skills already seeded")
	}

	_, err = b.skillRepo.BulkUpsertBySlug(ctx, toSeed)
	return err
}

func (b *Bootstrapper) stepSeedModelRegistry(ctx context.Context, state *BootstrapState) error {
	if b.providerRepo == nil || b.profileRepo == nil || b.assignmentRepo == nil {
		return fmt.Errorf("model repositories are not configured")
	}

	orgID, err := requiredOrganizationID(state)
	if err != nil {
		return err
	}

	changed := false
	providers, err := b.ensureModelProviders(ctx)
	if err != nil {
		return err
	}
	if providers.created > 0 {
		changed = true
	}

	profilesCreated, err := b.ensureModelProfiles(ctx, orgID, providers.bySlug)
	if err != nil {
		return err
	}
	if profilesCreated > 0 {
		changed = true
	}

	assignmentsCreated, err := b.ensureModelProfileAssignments(ctx, orgID)
	if err != nil {
		return err
	}
	if assignmentsCreated > 0 {
		changed = true
	}

	if !changed {
		return skipStep("model providers, profiles, and assignments already seeded")
	}
	return nil
}

func (b *Bootstrapper) stepRecordBootstrapAuditEvent(ctx context.Context, state *BootstrapState) error {
	if b.pool == nil {
		return fmt.Errorf("database pool is required")
	}
	if b.auditRecorder == nil {
		return fmt.Errorf("audit recorder is not configured")
	}

	orgID, err := requiredOrganizationID(state)
	if err != nil {
		return err
	}

	exists, err := b.bootstrapAuditEventExists(ctx, orgID)
	if err != nil {
		return err
	}
	if exists {
		return skipStep("already bootstrapped")
	}

	return b.auditRecorder.Record(ctx, audit.Event{
		OrgID:         orgID,
		EventType:     bootstrapEvent,
		PrincipalType: "system",
		PrincipalID:   audit.SystemPrincipalID,
		Metadata: map[string]any{
			"version":    b.appVersion,
			"step_count": stepCount,
			"timestamp":  b.now().UTC().Format(time.RFC3339Nano),
		},
	})
}

type providerSeedResult struct {
	bySlug  map[string]repo.ModelProvider
	created int
}

func (b *Bootstrapper) ensureModelProviders(ctx context.Context) (providerSeedResult, error) {
	seeds := []repo.ModelProvider{
		{
			Slug:              "anthropic",
			DisplayName:       "Anthropic",
			APIBaseURL:        "https://api.anthropic.com",
			SupportedFeatures: []string{"streaming", "tools"},
			IsEnabled:         true,
			Metadata:          json.RawMessage(`{"seeded_by":"bootstrap"}`),
		},
		{
			Slug:              "openai",
			DisplayName:       "OpenAI",
			APIBaseURL:        "https://api.openai.com/v1",
			SupportedFeatures: []string{"streaming", "tools"},
			IsEnabled:         true,
			Metadata:          json.RawMessage(`{"seeded_by":"bootstrap"}`),
		},
	}

	result := providerSeedResult{bySlug: make(map[string]repo.ModelProvider, len(seeds))}
	for _, seed := range seeds {
		provider, err := b.providerRepo.GetBySlug(ctx, seed.Slug)
		if err == nil {
			result.bySlug[seed.Slug] = provider
			continue
		}
		if !errors.Is(err, repo.ErrNotFound) {
			return providerSeedResult{}, err
		}

		created, createErr := b.providerRepo.Create(ctx, seed)
		if createErr != nil {
			return providerSeedResult{}, createErr
		}
		result.bySlug[seed.Slug] = created
		result.created++
	}

	return result, nil
}

func (b *Bootstrapper) ensureModelProfiles(ctx context.Context, orgID uuid.UUID, providers map[string]repo.ModelProvider) (int, error) {
	seeds := []modelProfileSeed{
		{LogicalProfileID: "high-capability", ModelName: "claude-opus-4-5", ProviderSlug: "anthropic"},
		{LogicalProfileID: "standard", ModelName: "claude-sonnet-4-5", ProviderSlug: "anthropic"},
		{LogicalProfileID: "haiku", ModelName: "claude-haiku-3-5", ProviderSlug: "anthropic"},
	}

	createdCount := 0
	for _, seed := range seeds {
		exists, err := b.currentOrgProfileExists(ctx, orgID, seed.LogicalProfileID)
		if err != nil {
			return 0, err
		}
		if exists {
			continue
		}

		provider, ok := providers[seed.ProviderSlug]
		if !ok {
			return 0, fmt.Errorf("provider %q not found", seed.ProviderSlug)
		}

		_, err = b.profileRepo.Create(ctx, repo.ModelProfile{
			LogicalProfileID:    seed.LogicalProfileID,
			OrganizationID:      &orgID,
			Version:             1,
			IsCurrent:           true,
			ProviderID:          provider.ID,
			ModelName:           seed.ModelName,
			ContextWindowTokens: 200000,
			MaxOutputTokens:     8192,
			SupportsStreaming:   true,
			SupportsVision:      false,
			InvocationPurpose:   "agent_turn",
		})
		if err != nil {
			return 0, err
		}
		createdCount++
	}

	return createdCount, nil
}

func (b *Bootstrapper) ensureModelProfileAssignments(ctx context.Context, orgID uuid.UUID) (int, error) {
	// The current assignment schema supports one profile per purpose at a scope.
	// We seed a single org-level default for each invocation purpose.
	seeds := []assignmentSeed{
		{InvocationPurpose: "agent_turn", LogicalProfileID: "high-capability"},
		{InvocationPurpose: "listening_eval", LogicalProfileID: "haiku"},
		{InvocationPurpose: "summarization", LogicalProfileID: "standard"},
		{InvocationPurpose: "skill_summarization", LogicalProfileID: "standard"},
		{InvocationPurpose: "memory_extraction", LogicalProfileID: "haiku"},
		{InvocationPurpose: "memory_distillation", LogicalProfileID: "haiku"},
		{InvocationPurpose: "memory_entity_synthesis", LogicalProfileID: "haiku"},
		{InvocationPurpose: "replay", LogicalProfileID: "haiku"},
	}

	createdOrUpdated := 0
	for _, seed := range seeds {
		existing, err := b.assignmentRepo.GetByScope(ctx, orgID, organizationScope, orgID, seed.InvocationPurpose)
		if err == nil {
			if existing.LogicalProfileID == seed.LogicalProfileID {
				continue
			}
		} else if !errors.Is(err, repo.ErrNotFound) {
			return 0, err
		}

		_, err = b.assignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
			OrganizationID:    orgID,
			ScopeType:         organizationScope,
			ScopeID:           orgID,
			LogicalProfileID:  seed.LogicalProfileID,
			InvocationPurpose: seed.InvocationPurpose,
		})
		if err != nil {
			return 0, err
		}
		createdOrUpdated++
	}

	return createdOrUpdated, nil
}

func (b *Bootstrapper) adminUserExists(ctx context.Context, orgID uuid.UUID) (bool, error) {
	if b.pool == nil {
		return false, fmt.Errorf("database pool is required")
	}

	var exists bool
	err := b.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM human_user
			WHERE organization_id = $1
			  AND role = 'admin'
			LIMIT 1
		)
	`, orgID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (b *Bootstrapper) currentOrgProfileExists(ctx context.Context, orgID uuid.UUID, logicalProfileID string) (bool, error) {
	if b.pool == nil {
		return false, fmt.Errorf("database pool is required")
	}

	var exists bool
	err := b.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM model_profile
			WHERE organization_id = $1
			  AND logical_profile_id = $2
			  AND is_current = true
			LIMIT 1
		)
	`, orgID, logicalProfileID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (b *Bootstrapper) bootstrapAuditEventExists(ctx context.Context, orgID uuid.UUID) (bool, error) {
	var exists bool
	err := b.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM audit_event
			WHERE organization_id = $1
			  AND event_type = $2
			LIMIT 1
		)
	`, orgID, bootstrapEvent).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (b *Bootstrapper) truncateApplicationTables(ctx context.Context) error {
	rows, err := b.pool.Query(ctx, `
		SELECT tablename
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename <> 'schema_migrations'
	`)
	if err != nil {
		return fmt.Errorf("load table list: %w", err)
	}
	defer rows.Close()

	tables := make([]string, 0, 32)
	for rows.Next() {
		var table string
		if scanErr := rows.Scan(&table); scanErr != nil {
			return fmt.Errorf("scan table list: %w", scanErr)
		}
		tables = append(tables, table)
	}
	if rows.Err() != nil {
		return fmt.Errorf("iterate table list: %w", rows.Err())
	}
	if len(tables) == 0 {
		return nil
	}

	sort.Strings(tables)
	sanitized := make([]string, len(tables))
	for i, table := range tables {
		sanitized[i] = pgx.Identifier{table}.Sanitize()
	}

	statement := fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE", strings.Join(sanitized, ", "))
	if _, err := b.pool.Exec(ctx, statement); err != nil {
		return fmt.Errorf("truncate application tables: %w", err)
	}
	return nil
}

func loadDefaultSkillSeeds(fsys fs.FS) ([]defaultSkillSeed, error) {
	entries, err := fs.ReadDir(fsys, "defaults/skills")
	if err != nil {
		return nil, fmt.Errorf("read embedded default skills: %w", err)
	}

	displayNames := map[string]string{
		"summarize":   "Summarize",
		"code-review": "Code Review",
		"plan-task":   "Plan Task",
	}
	descriptions := map[string]string{
		"summarize":   "Summarize long text into concise, actionable notes.",
		"code-review": "Review code for correctness, risks, and testing gaps.",
		"plan-task":   "Break down requested work into clear implementation steps.",
	}

	seeds := make([]defaultSkillSeed, 0, len(entries))
	found := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}

		fileName := entry.Name()
		slug := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		raw, err := fs.ReadFile(fsys, filepath.ToSlash(filepath.Join("defaults", "skills", fileName)))
		if err != nil {
			return nil, fmt.Errorf("read embedded default skill %q: %w", fileName, err)
		}

		displayName, ok := displayNames[slug]
		if !ok {
			displayName = humanizeSlug(slug)
		}
		description, ok := descriptions[slug]
		if !ok {
			description = fmt.Sprintf("Built-in %s skill.", slug)
		}

		seeds = append(seeds, defaultSkillSeed{
			Slug:        slug,
			DisplayName: displayName,
			Description: description,
			FileName:    fileName,
			Content:     raw,
		})
		found[slug] = struct{}{}
	}

	required := []string{"summarize", "code-review", "plan-task"}
	for _, slug := range required {
		if _, ok := found[slug]; !ok {
			return nil, fmt.Errorf("missing required embedded default skill %q", slug)
		}
	}

	sort.Slice(seeds, func(i, j int) bool {
		return seeds[i].Slug < seeds[j].Slug
	})
	return seeds, nil
}

func writeSkillFile(skillsDir string, seed defaultSkillSeed) error {
	fullPath := filepath.Join(skillsDir, seed.FileName)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create skill directory for %q: %w", seed.FileName, err)
	}
	if err := os.WriteFile(fullPath, seed.Content, 0o644); err != nil {
		return fmt.Errorf("write default skill file %q: %w", seed.FileName, err)
	}
	return nil
}

func requiredOrganizationID(state *BootstrapState) (uuid.UUID, error) {
	if state == nil || state.OrganizationID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("organization id is required")
	}
	return state.OrganizationID, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func humanizeSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}

type stepSkippedError struct {
	reason string
}

func (s *stepSkippedError) Error() string {
	if s == nil {
		return "step skipped"
	}
	if strings.TrimSpace(s.reason) == "" {
		return "step skipped"
	}
	return s.reason
}

func skipStep(reason string) error {
	return &stepSkippedError{reason: strings.TrimSpace(reason)}
}
