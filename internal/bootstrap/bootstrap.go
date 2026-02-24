package bootstrap

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"golang.org/x/crypto/bcrypt"
)

const (
	stepNameRunMigrations      = "run-migrations"
	stepNameCreateOrganization = "create-organization"
	stepNameCreateFirstUser    = "create-first-human-user"
	stepNameSeedSkills         = "seed-default-skills"
	stepNameSeedModels         = "seed-model-registry"
	stepNameSeedFlowTemplates  = "seed-flow-templates"
	stepNameCreateAgents       = "create-agents"
	stepNameCreateSession      = "create-general-session"
	stepNameSeedOrgPolicy      = "seed-org-policy"
	stepNameRecordAuditEvent   = "record-bootstrap-audit-event"

	defaultOrgSlug   = "default"
	defaultOrgName   = "OtterCamp"
	defaultSkillsDir = "./skills"
)

var defaultStepOrder = []string{
	stepNameRunMigrations,
	stepNameCreateOrganization,
	stepNameCreateFirstUser,
	stepNameSeedSkills,
	stepNameSeedModels,
	stepNameSeedFlowTemplates,
	stepNameCreateAgents,
	stepNameCreateSession,
	stepNameSeedOrgPolicy,
	stepNameRecordAuditEvent,
}

var stepAliases = map[string]string{
	"create-starter-trio": stepNameCreateAgents,
}

//go:embed defaults/skills/*.md
var defaultSkillsFS embed.FS

type Migrator interface {
	Run(ctx context.Context) error
}

type OrganizationRepository interface {
	GetBySlug(ctx context.Context, slug string) (repo.Organization, error)
	Create(ctx context.Context, org repo.Organization) (repo.Organization, error)
}

type UserRepository interface {
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
	Create(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
}

type SkillRepository interface {
	GetBySlug(ctx context.Context, organizationID uuid.UUID, projectID *uuid.UUID, slug string) (repo.Skill, error)
	BulkUpsertBySlug(ctx context.Context, skills []repo.Skill) ([]repo.Skill, error)
}

type ModelProviderRepository interface {
	GetBySlug(ctx context.Context, slug string) (repo.ModelProvider, error)
	Create(ctx context.Context, provider repo.ModelProvider) (repo.ModelProvider, error)
	SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (repo.ModelProvider, error)
}

type ModelProfileRepository interface {
	Create(ctx context.Context, profile repo.ModelProfile) (repo.ModelProfile, error)
	Deprecate(ctx context.Context, currentID uuid.UUID, next repo.ModelProfile) (repo.ModelProfile, error)
}

type ModelProfileAssignmentRepository interface {
	Upsert(ctx context.Context, assignment repo.ModelProfileAssignment) (repo.ModelProfileAssignment, error)
}

type StepFunc func(ctx context.Context, state *BootstrapState) error

type bootstrapStep struct {
	name      string
	canonical string
	fn        StepFunc
}

type BootstrapState struct {
	Organization repo.Organization
}

type Options struct {
	Logger                     *slog.Logger
	Clock                      clock.Clock
	Pool                       *pgxpool.Pool
	Migrator                   Migrator
	OrgRepo                    OrganizationRepository
	UserRepo                   UserRepository
	SkillRepo                  SkillRepository
	ModelProviderRepo          ModelProviderRepository
	ModelProfileRepo           ModelProfileRepository
	ModelProfileAssignmentRepo ModelProfileAssignmentRepository
	AuditRecorder              audit.AuditRecorder
	SkillsDir                  string
	AppVersion                 string
}

type Bootstrapper struct {
	logger                     *slog.Logger
	clock                      clock.Clock
	pool                       *pgxpool.Pool
	migrator                   Migrator
	orgRepo                    OrganizationRepository
	userRepo                   UserRepository
	skillRepo                  SkillRepository
	modelProviderRepo          ModelProviderRepository
	modelProfileRepo           ModelProfileRepository
	modelProfileAssignmentRepo ModelProfileAssignmentRepository
	auditRecorder              audit.AuditRecorder
	skillsDir                  string
	appVersion                 string
	steps                      []bootstrapStep
}

func New(opts Options) *Bootstrapper {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	clk := opts.Clock
	if clk == nil {
		clk = clock.Real{}
	}

	skillsDir := strings.TrimSpace(opts.SkillsDir)
	if skillsDir == "" {
		skillsDir = defaultSkillsDir
	}

	appVersion := strings.TrimSpace(opts.AppVersion)
	if appVersion == "" {
		appVersion = "dev"
	}

	b := &Bootstrapper{
		logger:                     logger,
		clock:                      clk,
		pool:                       opts.Pool,
		migrator:                   opts.Migrator,
		orgRepo:                    opts.OrgRepo,
		userRepo:                   opts.UserRepo,
		skillRepo:                  opts.SkillRepo,
		modelProviderRepo:          opts.ModelProviderRepo,
		modelProfileRepo:           opts.ModelProfileRepo,
		modelProfileAssignmentRepo: opts.ModelProfileAssignmentRepo,
		auditRecorder:              opts.AuditRecorder,
		skillsDir:                  skillsDir,
		appVersion:                 appVersion,
	}
	b.steps = []bootstrapStep{
		{name: stepNameRunMigrations, canonical: stepNameRunMigrations, fn: b.stepRunMigrations},
		{name: stepNameCreateOrganization, canonical: stepNameCreateOrganization, fn: b.stepCreateOrganization},
		{name: stepNameCreateFirstUser, canonical: stepNameCreateFirstUser, fn: b.stepCreateFirstHumanUser},
		{name: stepNameSeedSkills, canonical: stepNameSeedSkills, fn: b.stepSeedDefaultSkills},
		{name: stepNameSeedModels, canonical: stepNameSeedModels, fn: b.stepSeedModelRegistry},
		{name: stepNameSeedFlowTemplates, canonical: stepNameSeedFlowTemplates, fn: b.stepSeedFlowTemplates},
		{name: stepNameCreateAgents, canonical: stepNameCreateAgents, fn: b.stepCreateAgents},
		{name: stepNameCreateSession, canonical: stepNameCreateSession, fn: b.stepCreateGeneralSession},
		{name: stepNameSeedOrgPolicy, canonical: stepNameSeedOrgPolicy, fn: b.stepSeedOrgPolicy},
		{name: stepNameRecordAuditEvent, canonical: stepNameRecordAuditEvent, fn: b.stepRecordBootstrapAuditEvent},
	}
	return b
}

func (b *Bootstrapper) RegisterStep(name string, fn StepFunc) {
	if b == nil || fn == nil {
		return
	}

	raw := strings.TrimSpace(name)
	if raw == "" {
		return
	}
	canonical := canonicalStepName(raw)

	for i := range b.steps {
		if b.steps[i].canonical == canonical {
			b.steps[i].fn = fn
			return
		}
	}

	b.steps = append(b.steps, bootstrapStep{
		name:      raw,
		canonical: canonical,
		fn:        fn,
	})
}

func (b *Bootstrapper) Run(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}

	state := &BootstrapState{}
	for i, step := range b.steps {
		err := b.runStepSafely(ctx, state, step)
		if err != nil {
			var skipped *stepSkippedError
			if errors.As(err, &skipped) {
				b.logger.Info(fmt.Sprintf("bootstrap step %d (%s): skipped — %s", i+1, step.name, skipped.reason))
				continue
			}
			return fmt.Errorf("step %d (%s): %w", i+1, step.name, err)
		}

		b.logger.Info(fmt.Sprintf("bootstrap step %d (%s): done", i+1, step.name))
	}
	return nil
}

func (b *Bootstrapper) runStepSafely(ctx context.Context, state *BootstrapState, step bootstrapStep) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic recovered in bootstrap step: %v", recovered)
		}
	}()

	if step.fn == nil {
		return fmt.Errorf("step function is nil")
	}
	return step.fn(ctx, state)
}

func (b *Bootstrapper) stepRunMigrations(ctx context.Context, _ *BootstrapState) error {
	if b.migrator == nil {
		return fmt.Errorf("migrator is not configured")
	}
	return b.migrator.Run(ctx)
}

func (b *Bootstrapper) stepCreateOrganization(ctx context.Context, state *BootstrapState) error {
	if b.orgRepo == nil {
		return fmt.Errorf("organization repository is not configured")
	}

	org, err := b.orgRepo.GetBySlug(ctx, bootstrapOrgSlug())
	if err == nil {
		state.Organization = org
		return nil
	}
	if !errors.Is(err, repo.ErrNotFound) {
		return err
	}

	created, err := b.orgRepo.Create(ctx, repo.Organization{
		Slug:        bootstrapOrgSlug(),
		DisplayName: bootstrapOrgName(),
		Settings:    repo.OrganizationSettings{},
	})
	if err != nil {
		return err
	}
	state.Organization = created
	return nil
}

func (b *Bootstrapper) stepCreateFirstHumanUser(ctx context.Context, state *BootstrapState) error {
	if b.userRepo == nil {
		return fmt.Errorf("user repository is not configured")
	}
	if state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization is not initialized")
	}

	users, err := b.userRepo.List(ctx, state.Organization.ID)
	if err != nil {
		return err
	}
	if adminUserExists(users) {
		return skipStep("admin user already exists")
	}

	email, password, configured, err := readAdminCredentials()
	if err != nil {
		return err
	}
	if !configured {
		return skipStep("admin credentials not provided")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	passwordHash := string(hashedPassword)

	_, err = b.userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: state.Organization.ID,
		Email:          email,
		DisplayName:    "Admin",
		PasswordHash:   &passwordHash,
		Role:           "admin",
		IsActive:       true,
		Settings:       []byte(`{}`),
	})
	if err != nil {
		return err
	}
	return nil
}

func (b *Bootstrapper) stepSeedDefaultSkills(ctx context.Context, state *BootstrapState) error {
	if b.skillRepo == nil {
		return fmt.Errorf("skill repository is not configured")
	}
	if state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization is not initialized")
	}

	defaultSkills, err := loadDefaultSkills()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(b.skillsDir, 0o755); err != nil {
		return fmt.Errorf("ensure skills directory: %w", err)
	}

	upserts := make([]repo.Skill, 0, len(defaultSkills))
	for _, skill := range defaultSkills {
		absolutePath := filepath.Join(b.skillsDir, skill.FileName)
		if err := writeFileIfDifferent(absolutePath, skill.Content); err != nil {
			return err
		}

		desired := repo.Skill{
			OrganizationID: state.Organization.ID,
			Slug:           skill.Slug,
			DisplayName:    skill.DisplayName,
			Description:    skill.Description,
			FilePath:       filepath.ToSlash(filepath.Join("skills", skill.FileName)),
			Version:        1,
			IsActive:       true,
			CreatedByType:  "system",
			CreatedByID:    audit.SystemPrincipalID,
		}

		existing, err := b.skillRepo.GetBySlug(ctx, state.Organization.ID, nil, skill.Slug)
		if errors.Is(err, repo.ErrNotFound) {
			upserts = append(upserts, desired)
			continue
		}
		if err != nil {
			return err
		}
		if skillNeedsUpsert(existing, desired) {
			upserts = append(upserts, desired)
		}
	}

	if len(upserts) == 0 {
		return nil
	}

	_, err = b.skillRepo.BulkUpsertBySlug(ctx, upserts)
	return err
}

func (b *Bootstrapper) stepSeedModelRegistry(ctx context.Context, state *BootstrapState) error {
	if b.modelProviderRepo == nil || b.modelProfileRepo == nil || b.modelProfileAssignmentRepo == nil {
		return fmt.Errorf("model repositories are not configured")
	}
	if b.pool == nil {
		return fmt.Errorf("database pool is not configured")
	}
	if state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization is not initialized")
	}

	anthropic, err := b.ensureModelProvider(ctx, modelProviderSeed{
		Slug:              "anthropic",
		DisplayName:       "Anthropic",
		APIBaseURL:        "https://api.anthropic.com/v1",
		SupportedFeatures: []string{"streaming", "tool_use"},
	})
	if err != nil {
		return err
	}

	if _, err := b.ensureModelProvider(ctx, modelProviderSeed{
		Slug:              "openai",
		DisplayName:       "OpenAI",
		APIBaseURL:        "https://api.openai.com/v1",
		SupportedFeatures: []string{"streaming", "tool_use"},
	}); err != nil {
		return err
	}

	highCapability := modelProfileSeed{
		LogicalProfileID:    "high-capability",
		ProviderID:          anthropic.ID,
		ModelName:           "claude-opus-4-5",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      true,
		Temperature:         ptrFloat64(0.7),
		InvocationPurpose:   "agent_turn",
	}
	standard := modelProfileSeed{
		LogicalProfileID:    "standard",
		ProviderID:          anthropic.ID,
		ModelName:           "claude-sonnet-4-5",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      true,
		Temperature:         ptrFloat64(0.7),
		InvocationPurpose:   "agent_turn",
		FallbackProfileID:   ptrString("high-capability"),
	}
	haiku := modelProfileSeed{
		LogicalProfileID:    "haiku",
		ProviderID:          anthropic.ID,
		ModelName:           "claude-haiku-3-5",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     4096,
		SupportsStreaming:   false,
		SupportsVision:      false,
		Temperature:         ptrFloat64(0.3),
		InvocationPurpose:   "agent_turn",
	}

	if _, err := b.ensureModelProfile(ctx, state.Organization.ID, highCapability); err != nil {
		return err
	}
	if _, err := b.ensureModelProfile(ctx, state.Organization.ID, standard); err != nil {
		return err
	}
	if _, err := b.ensureModelProfile(ctx, state.Organization.ID, haiku); err != nil {
		return err
	}

	assignments := []struct {
		Purpose string
		Profile string
	}{
		{Purpose: "agent_turn", Profile: "high-capability"},
		{Purpose: "listening_eval", Profile: "haiku"},
		{Purpose: "summarization", Profile: "standard"},
		{Purpose: "skill_summarization", Profile: "standard"},
		{Purpose: "memory_extraction", Profile: "haiku"},
		{Purpose: "memory_distillation", Profile: "haiku"},
		{Purpose: "memory_entity_synthesis", Profile: "haiku"},
		{Purpose: "replay", Profile: "haiku"},
	}

	for _, assignment := range assignments {
		_, err := b.modelProfileAssignmentRepo.Upsert(ctx, repo.ModelProfileAssignment{
			OrganizationID:    state.Organization.ID,
			ScopeType:         "organization",
			ScopeID:           state.Organization.ID,
			LogicalProfileID:  assignment.Profile,
			InvocationPurpose: assignment.Purpose,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (b *Bootstrapper) stepSeedFlowTemplates(_ context.Context, _ *BootstrapState) error {
	return skipStep(waitingForTableMessage("flow_template"))
}

func (b *Bootstrapper) stepCreateAgents(_ context.Context, _ *BootstrapState) error {
	return skipStep(waitingForTableMessage("agent"))
}

func (b *Bootstrapper) stepCreateGeneralSession(_ context.Context, _ *BootstrapState) error {
	return skipStep(waitingForTableMessage("chat_session"))
}

func (b *Bootstrapper) stepSeedOrgPolicy(_ context.Context, _ *BootstrapState) error {
	return skipStep(waitingForTableMessage("capability_policy"))
}

func (b *Bootstrapper) stepRecordBootstrapAuditEvent(ctx context.Context, state *BootstrapState) error {
	if b.pool == nil {
		return fmt.Errorf("database pool is not configured")
	}
	if b.auditRecorder == nil {
		return fmt.Errorf("audit recorder is not configured")
	}
	if state.Organization.ID == uuid.Nil {
		return fmt.Errorf("organization is not initialized")
	}

	exists, err := b.bootstrapAuditEventExists(ctx, state.Organization.ID)
	if err != nil {
		return err
	}
	if exists {
		return skipStep("already bootstrapped")
	}

	return b.auditRecorder.Record(ctx, audit.Event{
		OrgID:         state.Organization.ID,
		EventType:     "system.bootstrap",
		PrincipalType: "system",
		PrincipalID:   audit.SystemPrincipalID,
		Metadata: map[string]any{
			"version":    b.appVersion,
			"step_count": len(defaultStepOrder),
			"timestamp":  b.clock.Now().UTC().Format(time.RFC3339Nano),
		},
	})
}

func (b *Bootstrapper) bootstrapAuditEventExists(ctx context.Context, orgID uuid.UUID) (bool, error) {
	var found int
	err := b.pool.QueryRow(ctx, `
		SELECT 1
		FROM audit_event
		WHERE event_type = 'system.bootstrap' AND organization_id = $1
		LIMIT 1
	`, orgID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (b *Bootstrapper) ensureModelProvider(ctx context.Context, seed modelProviderSeed) (repo.ModelProvider, error) {
	provider, err := b.modelProviderRepo.GetBySlug(ctx, seed.Slug)
	if errors.Is(err, repo.ErrNotFound) {
		return b.modelProviderRepo.Create(ctx, repo.ModelProvider{
			Slug:              seed.Slug,
			DisplayName:       seed.DisplayName,
			APIBaseURL:        seed.APIBaseURL,
			SupportedFeatures: append([]string{}, seed.SupportedFeatures...),
			IsEnabled:         true,
		})
	}
	if err != nil {
		return repo.ModelProvider{}, err
	}
	if modelProviderNeedsEnable(provider) {
		return b.modelProviderRepo.SetEnabled(ctx, provider.ID, true)
	}
	return provider, nil
}

func (b *Bootstrapper) ensureModelProfile(ctx context.Context, orgID uuid.UUID, seed modelProfileSeed) (repo.ModelProfile, error) {
	current, exists, err := b.currentOrgProfile(ctx, orgID, seed.LogicalProfileID)
	if err != nil {
		return repo.ModelProfile{}, err
	}
	if !exists {
		return b.modelProfileRepo.Create(ctx, seed.toRepo(orgID))
	}
	if !modelProfileNeedsDeprecation(current, seed) {
		return current, nil
	}
	return b.modelProfileRepo.Deprecate(ctx, current.ID, seed.toRepo(orgID))
}

func (b *Bootstrapper) currentOrgProfile(ctx context.Context, orgID uuid.UUID, logicalProfileID string) (repo.ModelProfile, bool, error) {
	row := b.pool.QueryRow(ctx, `
		SELECT
			id,
			logical_profile_id,
			organization_id,
			version,
			is_current,
			provider_id,
			model_name,
			context_window_tokens,
			max_output_tokens,
			supports_streaming,
			supports_vision,
			temperature,
			invocation_purpose,
			fallback_profile_id,
			created_at,
			updated_at
		FROM model_profile
		WHERE organization_id = $1
		  AND logical_profile_id = $2
		  AND is_current = true
		LIMIT 1
	`, orgID, strings.TrimSpace(logicalProfileID))

	var profile repo.ModelProfile
	err := row.Scan(
		&profile.ID,
		&profile.LogicalProfileID,
		&profile.OrganizationID,
		&profile.Version,
		&profile.IsCurrent,
		&profile.ProviderID,
		&profile.ModelName,
		&profile.ContextWindowTokens,
		&profile.MaxOutputTokens,
		&profile.SupportsStreaming,
		&profile.SupportsVision,
		&profile.Temperature,
		&profile.InvocationPurpose,
		&profile.FallbackProfileID,
		&profile.CreatedAt,
		&profile.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return repo.ModelProfile{}, false, nil
	}
	if err != nil {
		return repo.ModelProfile{}, false, err
	}
	return profile, true, nil
}

type modelProviderSeed struct {
	Slug              string
	DisplayName       string
	APIBaseURL        string
	SupportedFeatures []string
}

type modelProfileSeed struct {
	LogicalProfileID    string
	ProviderID          uuid.UUID
	ModelName           string
	ContextWindowTokens int
	MaxOutputTokens     int
	SupportsStreaming   bool
	SupportsVision      bool
	Temperature         *float64
	InvocationPurpose   string
	FallbackProfileID   *string
}

func (s modelProfileSeed) toRepo(orgID uuid.UUID) repo.ModelProfile {
	return repo.ModelProfile{
		LogicalProfileID:    s.LogicalProfileID,
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          s.ProviderID,
		ModelName:           s.ModelName,
		ContextWindowTokens: s.ContextWindowTokens,
		MaxOutputTokens:     s.MaxOutputTokens,
		SupportsStreaming:   s.SupportsStreaming,
		SupportsVision:      s.SupportsVision,
		Temperature:         s.Temperature,
		InvocationPurpose:   s.InvocationPurpose,
		FallbackProfileID:   s.FallbackProfileID,
	}
}

type defaultSkill struct {
	Slug        string
	DisplayName string
	Description string
	FileName    string
	Content     []byte
}

var defaultSkillCatalog = []defaultSkill{
	{Slug: "summarize", DisplayName: "Summarize", Description: "Summarize material and decisions.", FileName: "summarize.md"},
	{Slug: "code-review", DisplayName: "Code Review", Description: "Review code for correctness and risk.", FileName: "code-review.md"},
	{Slug: "plan-task", DisplayName: "Plan Task", Description: "Break work into concrete implementation steps.", FileName: "plan-task.md"},
}

func loadDefaultSkills() ([]defaultSkill, error) {
	skills := make([]defaultSkill, 0, len(defaultSkillCatalog))
	for _, metadata := range defaultSkillCatalog {
		content, err := defaultSkillsFS.ReadFile(filepath.ToSlash(filepath.Join("defaults", "skills", metadata.FileName)))
		if err != nil {
			return nil, fmt.Errorf("read embedded skill %q: %w", metadata.FileName, err)
		}

		skill := metadata
		skill.Content = content
		skills = append(skills, skill)
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Slug < skills[j].Slug
	})
	return skills, nil
}

func writeFileIfDifferent(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure skill directory: %w", err)
	}

	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, content) {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read existing skill file: %w", err)
	}

	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write skill file %q: %w", path, err)
	}
	return nil
}

func canonicalStepName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if alias, ok := stepAliases[normalized]; ok {
		return alias
	}
	return normalized
}

func waitingForTableMessage(tableName string) string {
	return fmt.Sprintf("waiting for table %s to be registered", strings.TrimSpace(tableName))
}

func bootstrapOrgSlug() string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("OTTERCAMP_ORG_SLUG")))
	if raw == "" {
		return defaultOrgSlug
	}
	return raw
}

func bootstrapOrgName() string {
	raw := strings.TrimSpace(os.Getenv("OTTERCAMP_ORG_NAME"))
	if raw == "" {
		return defaultOrgName
	}
	return raw
}

func readAdminCredentials() (email string, password string, configured bool, err error) {
	email = strings.ToLower(strings.TrimSpace(os.Getenv("OTTERCAMP_ADMIN_EMAIL")))
	password = os.Getenv("OTTERCAMP_ADMIN_PASSWORD")

	if email == "" && strings.TrimSpace(password) == "" {
		return "", "", false, nil
	}
	if email == "" || strings.TrimSpace(password) == "" {
		return "", "", false, fmt.Errorf("OTTERCAMP_ADMIN_EMAIL and OTTERCAMP_ADMIN_PASSWORD must both be set")
	}
	return email, password, true, nil
}

func adminUserExists(users []repo.HumanUser) bool {
	for _, user := range users {
		if strings.EqualFold(strings.TrimSpace(user.Role), "admin") {
			return true
		}
	}
	return false
}

func skillNeedsUpsert(existing repo.Skill, desired repo.Skill) bool {
	return strings.TrimSpace(existing.DisplayName) != strings.TrimSpace(desired.DisplayName) ||
		strings.TrimSpace(existing.Description) != strings.TrimSpace(desired.Description) ||
		filepath.ToSlash(strings.TrimSpace(existing.FilePath)) != filepath.ToSlash(strings.TrimSpace(desired.FilePath)) ||
		existing.IsActive != desired.IsActive
}

func modelProviderNeedsEnable(provider repo.ModelProvider) bool {
	return !provider.IsEnabled
}

func modelProfileNeedsDeprecation(current repo.ModelProfile, desired modelProfileSeed) bool {
	if current.ProviderID != desired.ProviderID {
		return true
	}
	if strings.TrimSpace(current.ModelName) != strings.TrimSpace(desired.ModelName) {
		return true
	}
	if current.ContextWindowTokens != desired.ContextWindowTokens {
		return true
	}
	if current.MaxOutputTokens != desired.MaxOutputTokens {
		return true
	}
	if current.SupportsStreaming != desired.SupportsStreaming {
		return true
	}
	if current.SupportsVision != desired.SupportsVision {
		return true
	}
	if !equalOptionalFloat64(current.Temperature, desired.Temperature) {
		return true
	}
	if strings.TrimSpace(current.InvocationPurpose) != strings.TrimSpace(desired.InvocationPurpose) {
		return true
	}
	if !equalOptionalString(current.FallbackProfileID, desired.FallbackProfileID) {
		return true
	}
	return false
}

func equalOptionalFloat64(a, b *float64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return math.Abs(*a-*b) < 0.000001
}

func equalOptionalString(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return strings.TrimSpace(*a) == strings.TrimSpace(*b)
}

func ptrFloat64(v float64) *float64 {
	return &v
}

func ptrString(v string) *string {
	return &v
}

type stepSkippedError struct {
	reason string
}

func (e *stepSkippedError) Error() string {
	return e.reason
}

func skipStep(reason string) error {
	return &stepSkippedError{reason: strings.TrimSpace(reason)}
}
