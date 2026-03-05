package bootstrap

import (
	"bytes"
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
	"golang.org/x/crypto/bcrypt"

	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

const (
	defaultOrgSlug  = "default"
	defaultOrgName  = "OtterCamp"
	defaultSkillsFS = "./skills"
	adminRole       = "admin"
	bcryptCost      = 12
	systemActorType = "system"
	systemEventType = "system.bootstrap"
)

var (
	systemActorID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

	stepNameAliases = map[string]string{
		"create-agents": "create-starter-trio",
	}
)

//go:embed defaults/skills/*.md
var defaultSkillAssets embed.FS

type StepFunc func(ctx context.Context, state *State) error

type MigrateRunner interface {
	Run(ctx context.Context) error
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Progress struct {
	Step    int
	Name    string
	Status  string
	Details string
}

type ProgressFn func(progress Progress)

type State struct {
	OrganizationID uuid.UUID
}

type Options struct {
	Pool       *pgxpool.Pool
	Logger     *slog.Logger
	Store      storage.Store
	SkillsDir  string
	OrgSlug    string
	OrgName    string
	AdminEmail string

	AdminPassword      string
	Version            string
	MigrateRunner      MigrateRunner
	Now                func() time.Time
	ProgressFn         ProgressFn
	DisableDefaultStep bool
}

type step struct {
	number int
	name   string
	fn     StepFunc
}

type skipStep struct {
	reason string
}

func (e skipStep) Error() string {
	return e.reason
}

func (e skipStep) Reason() string {
	return e.reason
}

func skipped(reason string) error {
	return skipStep{reason: strings.TrimSpace(reason)}
}

type Bootstrapper struct {
	mu sync.RWMutex

	logger    *slog.Logger
	store     storage.Store
	skillsDir string
	version   string
	now       func() time.Time
	progress  ProgressFn

	orgSlug       string
	orgName       string
	adminEmail    string
	adminPassword string

	runner MigrateRunner

	pool                *pgxpool.Pool
	rowQuerier          rowQuerier
	orgRepo             *repo.OrgRepo
	userRepo            *repo.HumanUserRepo
	skillRepo           *repo.SkillRepo
	providerRepo        *repo.ModelProviderRepo
	profileRepo         *repo.ModelProfileRepo
	profileAssignRepo   *repo.ModelProfileAssignmentRepo
	flowTemplateRepo    *repo.FlowTemplateRepo
	flowNodeRepo        *repo.FlowNodeRepo
	auditRepo           *repo.AuditEventRepo
	auditService        *audit.Service
	defaultSkillSeeds   []defaultSkillSeed
	modelAssignmentPlan []modelAssignment

	steps []step
}

type defaultSkillSeed struct {
	Slug        string
	DisplayName string
	Description string
	FilePath    string
	Content     []byte
}

type modelProfileSeed struct {
	LogicalProfileID    string
	ProviderSlug        string
	ModelName           string
	ContextWindowTokens int
	MaxOutputTokens     int
	SupportsStreaming   bool
	SupportsVision      bool
	InvocationPurpose   string
}

type modelAssignment struct {
	LogicalProfileID  string
	InvocationPurpose string
}

func NewBootstrapper(opts ...Options) *Bootstrapper {
	merged := defaultOptions()
	if len(opts) > 0 {
		merged = mergeOptions(merged, opts[0])
	}
	if merged.Logger == nil {
		merged.Logger = slog.Default()
	}
	if merged.Now == nil {
		merged.Now = time.Now
	}
	if strings.TrimSpace(merged.OrgSlug) == "" {
		merged.OrgSlug = defaultOrgSlug
	}
	if strings.TrimSpace(merged.OrgName) == "" {
		merged.OrgName = defaultOrgName
	}
	if strings.TrimSpace(merged.SkillsDir) == "" {
		merged.SkillsDir = defaultSkillsFS
	}
	if merged.MigrateRunner == nil && merged.Pool != nil {
		merged.MigrateRunner = migrate.NewRunner(merged.Pool, merged.Logger)
	}

	b := &Bootstrapper{
		logger:            merged.Logger,
		store:             merged.Store,
		skillsDir:         merged.SkillsDir,
		version:           strings.TrimSpace(merged.Version),
		now:               merged.Now,
		progress:          merged.ProgressFn,
		orgSlug:           strings.TrimSpace(merged.OrgSlug),
		orgName:           strings.TrimSpace(merged.OrgName),
		adminEmail:        strings.TrimSpace(merged.AdminEmail),
		adminPassword:     merged.AdminPassword,
		runner:            merged.MigrateRunner,
		pool:              merged.Pool,
		rowQuerier:        merged.Pool,
		defaultSkillSeeds: loadDefaultSkillSeeds(),
		modelAssignmentPlan: []modelAssignment{
			{LogicalProfileID: "high-capability", InvocationPurpose: "agent_turn"},
			{LogicalProfileID: "haiku", InvocationPurpose: "listening_eval"},
			{LogicalProfileID: "standard", InvocationPurpose: "summarization"},
			{LogicalProfileID: "standard", InvocationPurpose: "skill_summarization"},
			{LogicalProfileID: "haiku", InvocationPurpose: "memory_extraction"},
			{LogicalProfileID: "haiku", InvocationPurpose: "memory_distillation"},
			{LogicalProfileID: "haiku", InvocationPurpose: "memory_entity_synthesis"},
			{LogicalProfileID: "high-capability", InvocationPurpose: "replay"},
		},
	}

	if merged.Pool != nil {
		b.orgRepo = repo.NewOrgRepo(merged.Pool)
		b.userRepo = repo.NewHumanUserRepo(merged.Pool)
		b.skillRepo = repo.NewSkillRepo(merged.Pool)
		b.providerRepo = repo.NewModelProviderRepo(merged.Pool)
		b.profileRepo = repo.NewModelProfileRepo(merged.Pool)
		b.profileAssignRepo = repo.NewModelProfileAssignmentRepo(merged.Pool)
		b.flowTemplateRepo = repo.NewFlowTemplateRepo(merged.Pool)
		b.flowNodeRepo = repo.NewFlowNodeRepo(merged.Pool)
		b.auditRepo = repo.NewAuditEventRepo(merged.Pool)
		b.auditService = audit.NewService(b.auditRepo, merged.Logger)
	}

	if !merged.DisableDefaultStep {
		b.steps = b.defaultSteps()
	}
	return b
}

func defaultOptions() Options {
	return Options{
		OrgSlug:       envOrDefault("OTTERCAMP_ORG_SLUG", defaultOrgSlug),
		OrgName:       envOrDefault("OTTERCAMP_ORG_NAME", defaultOrgName),
		AdminEmail:    strings.TrimSpace(os.Getenv("OTTERCAMP_ADMIN_EMAIL")),
		AdminPassword: os.Getenv("OTTERCAMP_ADMIN_PASSWORD"),
		SkillsDir:     envOrDefault("OTTERCAMP_SKILLS_DIR", defaultSkillsFS),
	}
}

func mergeOptions(base, override Options) Options {
	merged := base
	if override.Pool != nil {
		merged.Pool = override.Pool
	}
	if override.Logger != nil {
		merged.Logger = override.Logger
	}
	if override.Store != nil {
		merged.Store = override.Store
	}
	if strings.TrimSpace(override.SkillsDir) != "" {
		merged.SkillsDir = override.SkillsDir
	}
	if strings.TrimSpace(override.OrgSlug) != "" {
		merged.OrgSlug = override.OrgSlug
	}
	if strings.TrimSpace(override.OrgName) != "" {
		merged.OrgName = override.OrgName
	}
	if strings.TrimSpace(override.AdminEmail) != "" {
		merged.AdminEmail = override.AdminEmail
	}
	if override.AdminPassword != "" {
		merged.AdminPassword = override.AdminPassword
	}
	if strings.TrimSpace(override.Version) != "" {
		merged.Version = override.Version
	}
	if override.MigrateRunner != nil {
		merged.MigrateRunner = override.MigrateRunner
	}
	if override.Now != nil {
		merged.Now = override.Now
	}
	if override.ProgressFn != nil {
		merged.ProgressFn = override.ProgressFn
	}
	merged.DisableDefaultStep = override.DisableDefaultStep
	return merged
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func (b *Bootstrapper) RegisterStep(name string, fn StepFunc) {
	if b == nil || fn == nil {
		return
	}

	normalized := normalizeStepName(name)
	if normalized == "" {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for i := range b.steps {
		if b.steps[i].name == normalized {
			b.steps[i].fn = fn
			return
		}
	}

	b.steps = append(b.steps, step{
		number: len(b.steps) + 1,
		name:   normalized,
		fn:     fn,
	})
}

func (b *Bootstrapper) StepNames() []string {
	if b == nil {
		return nil
	}

	steps := b.snapshot()
	names := make([]string, 0, len(steps))
	for _, current := range steps {
		names = append(names, current.name)
	}
	return names
}

func (b *Bootstrapper) Run(ctx context.Context) error {
	return b.RunWithState(ctx, &State{})
}

func (b *Bootstrapper) RunWithState(ctx context.Context, state *State) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}
	if state == nil {
		state = &State{}
	}

	for _, current := range b.snapshot() {
		err := runStep(ctx, current.fn, state)
		if err == nil {
			b.emitProgress(Progress{Step: current.number, Name: current.name, Status: "done"})
			continue
		}

		var skippedErr skipStep
		if errors.As(err, &skippedErr) {
			b.emitProgress(Progress{
				Step:    current.number,
				Name:    current.name,
				Status:  "skipped",
				Details: skippedErr.Reason(),
			})
			continue
		}

		b.emitProgress(Progress{
			Step:    current.number,
			Name:    current.name,
			Status:  "failed",
			Details: err.Error(),
		})
		return fmt.Errorf("bootstrap step %d (%s): %w", current.number, current.name, err)
	}

	return nil
}

func (b *Bootstrapper) emitProgress(progress Progress) {
	if b == nil {
		return
	}

	if b.progress != nil {
		b.progress(progress)
	}

	message := fmt.Sprintf("bootstrap step %d (%s): %s", progress.Step, progress.Name, progress.Status)
	if strings.TrimSpace(progress.Details) != "" {
		message = fmt.Sprintf("%s - %s", message, strings.TrimSpace(progress.Details))
	}
	b.logger.Info(message)
}

func (b *Bootstrapper) snapshot() []step {
	b.mu.RLock()
	defer b.mu.RUnlock()

	steps := make([]step, len(b.steps))
	copy(steps, b.steps)
	return steps
}

func (b *Bootstrapper) defaultSteps() []step {
	return []step{
		{number: 1, name: "run-migrations", fn: b.stepRunMigrations},
		{number: 2, name: "create-organization", fn: b.stepCreateOrganization},
		{number: 3, name: "create-admin-user", fn: b.stepCreateAdminUser},
		{number: 4, name: "seed-default-skills", fn: b.stepSeedDefaultSkills},
		{number: 5, name: "seed-model-profiles", fn: b.stepSeedModelProfiles},
		{number: 6, name: "seed-flow-templates", fn: b.stepSeedFlowTemplates},
		{number: 7, name: "create-starter-trio", fn: b.stubForTable(7, "create-starter-trio", "agent")},
		{number: 8, name: "create-general-session", fn: b.stubForTable(8, "create-general-session", "chat_session")},
		{number: 9, name: "seed-capability-policy", fn: b.stubForTable(9, "seed-capability-policy", "capability_policy")},
		{number: 10, name: "record-bootstrap-audit-event", fn: b.stepRecordBootstrapAuditEvent},
	}
}

func (b *Bootstrapper) stepRunMigrations(ctx context.Context, _ *State) error {
	if b.runner == nil {
		return fmt.Errorf("migration runner is not configured")
	}
	return b.runner.Run(ctx)
}

func (b *Bootstrapper) stepCreateOrganization(ctx context.Context, state *State) error {
	if b.orgRepo == nil {
		return fmt.Errorf("organization repository is not configured")
	}

	existing, err := b.orgRepo.GetBySlug(ctx, b.orgSlug)
	switch {
	case err == nil:
		state.OrganizationID = existing.ID
		return nil
	case errors.Is(err, repo.ErrNotFound):
	default:
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

func (b *Bootstrapper) stepCreateAdminUser(ctx context.Context, state *State) error {
	if b.userRepo == nil {
		return fmt.Errorf("human_user repository is not configured")
	}
	if state == nil || state.OrganizationID == uuid.Nil {
		return fmt.Errorf("organization id is required for admin bootstrap")
	}

	alreadyExists, err := b.adminUserExists(ctx, state.OrganizationID)
	if err != nil {
		return err
	}
	if alreadyExists {
		return nil
	}

	email := strings.TrimSpace(b.adminEmail)
	password := b.adminPassword
	if email == "" && password == "" {
		return skipped("admin credentials not provided")
	}
	if email == "" || password == "" {
		return fmt.Errorf("both OTTERCAMP_ADMIN_EMAIL and OTTERCAMP_ADMIN_PASSWORD must be set")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	hash := string(passwordHash)

	_, err = b.userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: state.OrganizationID,
		Email:          email,
		DisplayName:    "Admin",
		PasswordHash:   &hash,
		Role:           adminRole,
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		return err
	}
	return nil
}

func (b *Bootstrapper) stepSeedDefaultSkills(ctx context.Context, state *State) error {
	if b.skillRepo == nil {
		return fmt.Errorf("skill repository is not configured")
	}
	if state == nil || state.OrganizationID == uuid.Nil {
		return fmt.Errorf("organization id is required for skill bootstrap")
	}
	if len(b.defaultSkillSeeds) == 0 {
		return fmt.Errorf("default skill asset set is empty")
	}

	skills := make([]repo.Skill, 0, len(b.defaultSkillSeeds))
	for _, seed := range b.defaultSkillSeeds {
		if err := b.writeSkillAsset(ctx, seed); err != nil {
			return err
		}
		skills = append(skills, repo.Skill{
			OrganizationID: state.OrganizationID,
			ProjectID:      nil,
			Slug:           seed.Slug,
			DisplayName:    seed.DisplayName,
			Description:    seed.Description,
			FilePath:       seed.FilePath,
			Version:        1,
			IsActive:       true,
			CreatedByType:  systemActorType,
			CreatedByID:    systemActorID,
		})
	}

	_, err := b.skillRepo.BulkUpsertBySlug(ctx, skills)
	return err
}

func (b *Bootstrapper) writeSkillAsset(ctx context.Context, seed defaultSkillSeed) error {
	if b.store != nil {
		return b.store.Put(ctx, seed.FilePath, bytes.NewReader(seed.Content), storage.PutOptions{
			ContentType:   "text/markdown; charset=utf-8",
			ContentLength: int64(len(seed.Content)),
		})
	}

	targetPath := filepath.Join(b.skillsDir, filepath.Base(seed.FilePath))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create skills directory: %w", err)
	}
	if err := os.WriteFile(targetPath, seed.Content, 0o644); err != nil {
		return fmt.Errorf("write default skill asset %s: %w", targetPath, err)
	}
	return nil
}

func (b *Bootstrapper) stepSeedModelProfiles(ctx context.Context, state *State) error {
	if b.providerRepo == nil || b.profileRepo == nil || b.profileAssignRepo == nil {
		return fmt.Errorf("model bootstrap repositories are not configured")
	}
	if state == nil || state.OrganizationID == uuid.Nil {
		return fmt.Errorf("organization id is required for model bootstrap")
	}

	providerBySlug := make(map[string]repo.ModelProvider, 2)
	for _, seed := range defaultProviderSeeds() {
		provider, err := b.upsertProvider(ctx, seed)
		if err != nil {
			return err
		}
		providerBySlug[provider.Slug] = provider
	}

	for _, seed := range defaultProfileSeeds() {
		provider, ok := providerBySlug[seed.ProviderSlug]
		if !ok {
			return fmt.Errorf("missing provider %q for profile %q", seed.ProviderSlug, seed.LogicalProfileID)
		}
		if _, err := b.upsertOrgProfile(ctx, state.OrganizationID, provider.ID, seed); err != nil {
			return err
		}
	}

	for _, assignment := range b.modelAssignmentPlan {
		if _, err := b.profileAssignRepo.Upsert(ctx, repo.ModelProfileAssignment{
			OrganizationID:    state.OrganizationID,
			ScopeType:         "organization",
			ScopeID:           state.OrganizationID,
			LogicalProfileID:  assignment.LogicalProfileID,
			InvocationPurpose: assignment.InvocationPurpose,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (b *Bootstrapper) stepSeedFlowTemplates(ctx context.Context, _ *State) error {
	if b.flowTemplateRepo == nil || b.flowNodeRepo == nil {
		return fmt.Errorf("flow template repositories are not configured")
	}

	templates, err := b.flowTemplateRepo.BulkUpsertBySlug(ctx, defaultFlowTemplateSeeds())
	if err != nil {
		return err
	}
	return b.seedSystemTemplateNodes(ctx, templates)
}

type flowNodeSeedSpec struct {
	DisplayName         string
	NodeType            string
	Position            int
	ActorType           *string
	RequiresHumanReview bool
	NextSeedIndex       *int
}

type systemTemplateNodeSeedPlan struct {
	StartNodeID *uuid.UUID
	Seeds       []flowNodeSeedSpec
}

func buildSystemTemplateNodeSeedPlan(template repo.FlowTemplate, existing []repo.FlowNode) (systemTemplateNodeSeedPlan, error) {
	if len(existing) > 0 {
		if template.StartNodeID != nil {
			return systemTemplateNodeSeedPlan{}, nil
		}
		start := existing[0].ID
		return systemTemplateNodeSeedPlan{StartNodeID: &start}, nil
	}

	roleActor := "role"
	switch template.Slug {
	case "default-single-agent":
		nextIndex := 1
		return systemTemplateNodeSeedPlan{
			Seeds: []flowNodeSeedSpec{
				{
					DisplayName:   "Work",
					NodeType:      "work",
					Position:      0,
					ActorType:     &roleActor,
					NextSeedIndex: &nextIndex,
				},
				{
					DisplayName: "Review",
					NodeType:    "review",
					Position:    1,
				},
			},
		}, nil
	case "default-review":
		nextIndex := 1
		return systemTemplateNodeSeedPlan{
			Seeds: []flowNodeSeedSpec{
				{
					DisplayName:   "Work",
					NodeType:      "work",
					Position:      0,
					ActorType:     &roleActor,
					NextSeedIndex: &nextIndex,
				},
				{
					DisplayName: "Internal Review",
					NodeType:    "review",
					Position:    1,
				},
			},
		}, nil
	case "default-review-refinement":
		nextIndex := 1
		finalIndex := 2
		return systemTemplateNodeSeedPlan{
			Seeds: []flowNodeSeedSpec{
				{
					DisplayName:   "Generation",
					NodeType:      "work",
					Position:      0,
					ActorType:     &roleActor,
					NextSeedIndex: &nextIndex,
				},
				{
					DisplayName:   "Internal Review",
					NodeType:      "review",
					Position:      1,
					NextSeedIndex: &finalIndex,
				},
				{
					DisplayName:         "Human Review",
					NodeType:            "review",
					Position:            2,
					RequiresHumanReview: true,
				},
			},
		}, nil
	default:
		return systemTemplateNodeSeedPlan{}, fmt.Errorf("unsupported system flow template slug %q", template.Slug)
	}
}

func (b *Bootstrapper) seedSystemTemplateNodes(ctx context.Context, templates []repo.FlowTemplate) error {
	for _, template := range templates {
		plan, err := b.planSystemTemplateNodes(ctx, template)
		if err != nil {
			return err
		}
		if err := b.applySystemTemplateNodeSeedPlan(ctx, template, plan); err != nil {
			return err
		}
	}
	return nil
}

func (b *Bootstrapper) planSystemTemplateNodes(ctx context.Context, template repo.FlowTemplate) (systemTemplateNodeSeedPlan, error) {
	if !template.IsSystem {
		return systemTemplateNodeSeedPlan{}, nil
	}

	existing, err := b.flowNodeRepo.GetByTemplateOrdered(ctx, template.ID)
	if err != nil {
		return systemTemplateNodeSeedPlan{}, err
	}
	return buildSystemTemplateNodeSeedPlan(template, existing)
}

func (b *Bootstrapper) applySystemTemplateNodeSeedPlan(ctx context.Context, template repo.FlowTemplate, plan systemTemplateNodeSeedPlan) error {
	if len(plan.Seeds) == 0 {
		if plan.StartNodeID != nil {
			return b.updateTemplateStartNode(ctx, template, *plan.StartNodeID)
		}
		return nil
	}

	created := make([]repo.FlowNode, 0, len(plan.Seeds))
	for _, seed := range plan.Seeds {
		node, err := b.flowNodeRepo.Create(ctx, repo.FlowNode{
			FlowTemplateID:      template.ID,
			DisplayName:         seed.DisplayName,
			NodeType:            seed.NodeType,
			Position:            seed.Position,
			ActorType:           seed.ActorType,
			RequiresHumanReview: seed.RequiresHumanReview,
		})
		if err != nil {
			return err
		}
		created = append(created, node)
	}

	for i := range created {
		nextIndex := plan.Seeds[i].NextSeedIndex
		if nextIndex == nil {
			continue
		}
		if *nextIndex < 0 || *nextIndex >= len(created) {
			return fmt.Errorf("seed plan next node index %d out of range for template %s", *nextIndex, template.Slug)
		}
		node := created[i]
		node.NextNodeID = &created[*nextIndex].ID
		if _, err := b.flowNodeRepo.Update(ctx, node); err != nil {
			return err
		}
	}

	return b.updateTemplateStartNode(ctx, template, created[0].ID)
}

func (b *Bootstrapper) updateTemplateStartNode(ctx context.Context, template repo.FlowTemplate, nodeID uuid.UUID) error {
	next := template
	next.StartNodeID = &nodeID
	_, err := b.flowTemplateRepo.Update(ctx, next)
	return err
}

func (b *Bootstrapper) stepRecordBootstrapAuditEvent(ctx context.Context, state *State) error {
	if b.pool == nil || b.auditService == nil {
		return fmt.Errorf("audit dependencies are not configured")
	}
	if state == nil || state.OrganizationID == uuid.Nil {
		return fmt.Errorf("organization id is required for bootstrap audit event")
	}

	exists, err := b.bootstrapAuditEventExists(ctx, state.OrganizationID)
	if err != nil {
		return err
	}
	if exists {
		return skipped("already bootstrapped")
	}

	event := audit.Event{
		OrgID:         state.OrganizationID,
		EventType:     systemEventType,
		PrincipalType: systemActorType,
		PrincipalID:   systemActorID,
		Metadata: map[string]any{
			"version":    b.version,
			"step_count": 10,
			"timestamp":  b.now().UTC(),
		},
	}
	return b.auditService.Record(ctx, event)
}

func (b *Bootstrapper) adminUserExists(ctx context.Context, organizationID uuid.UUID) (bool, error) {
	if b.rowQuerier == nil {
		return false, fmt.Errorf("row querier is not configured")
	}

	var exists bool
	err := b.rowQuerier.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM human_user
			WHERE organization_id = $1
			  AND role = $2
			LIMIT 1
		)
	`, organizationID, adminRole).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (b *Bootstrapper) currentOrgProfileExists(ctx context.Context, organizationID uuid.UUID, logicalID string) (bool, error) {
	if b.rowQuerier == nil {
		return false, fmt.Errorf("row querier is not configured")
	}

	var exists bool
	err := b.rowQuerier.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM model_profile
			WHERE organization_id = $1
			  AND logical_profile_id = $2
			  AND is_current = true
			LIMIT 1
		)
	`, organizationID, logicalID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (b *Bootstrapper) bootstrapAuditEventExists(ctx context.Context, organizationID uuid.UUID) (bool, error) {
	if b.rowQuerier == nil {
		return false, fmt.Errorf("row querier is not configured")
	}

	var exists bool
	err := b.rowQuerier.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM audit_event
			WHERE organization_id = $1
			  AND event_type = $2
			LIMIT 1
		)
	`, organizationID, systemEventType).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func defaultProviderSeeds() []repo.ModelProvider {
	return []repo.ModelProvider{
		{
			Slug:              "anthropic",
			DisplayName:       "Anthropic",
			APIBaseURL:        "https://api.anthropic.com",
			SupportedFeatures: []string{"chat", "streaming"},
			IsEnabled:         true,
			Metadata:          json.RawMessage(`{}`),
		},
		{
			Slug:              "openai",
			DisplayName:       "OpenAI",
			APIBaseURL:        "https://api.openai.com/v1",
			SupportedFeatures: []string{"chat", "streaming"},
			IsEnabled:         true,
			Metadata:          json.RawMessage(`{}`),
		},
	}
}

func defaultProfileSeeds() []modelProfileSeed {
	return []modelProfileSeed{
		{
			LogicalProfileID:    "high-capability",
			ProviderSlug:        "anthropic",
			ModelName:           "claude-opus-4-6",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     4096,
			SupportsStreaming:   true,
			SupportsVision:      false,
			InvocationPurpose:   "agent_turn",
		},
		{
			LogicalProfileID:    "standard",
			ProviderSlug:        "anthropic",
			ModelName:           "claude-sonnet-4-6",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     4096,
			SupportsStreaming:   true,
			SupportsVision:      false,
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
}

func defaultFlowTemplateSeeds() []repo.FlowTemplate {
	return []repo.FlowTemplate{
		{
			OrganizationID: nil,
			ProjectID:      nil,
			Slug:           "default-single-agent",
			DisplayName:    "Default Single Agent",
			Description:    "Single-agent default task flow template.",
			IsCurrent:      true,
			Version:        1,
			IsSystem:       true,
			CreatedByType:  systemActorType,
			CreatedByID:    systemActorID,
		},
		{
			OrganizationID: nil,
			ProjectID:      nil,
			Slug:           "default-review",
			DisplayName:    "Default Review",
			Description:    "Review-oriented default task flow template.",
			IsCurrent:      true,
			Version:        1,
			IsSystem:       true,
			CreatedByType:  systemActorType,
			CreatedByID:    systemActorID,
		},
		{
			OrganizationID: nil,
			ProjectID:      nil,
			Slug:           "default-review-refinement",
			DisplayName:    "Default Review Refinement",
			Description:    "Subjective multi-option flow with internal review and human review handoff.",
			IsCurrent:      true,
			Version:        1,
			IsSystem:       true,
			CreatedByType:  systemActorType,
			CreatedByID:    systemActorID,
		},
	}
}

func (b *Bootstrapper) upsertProvider(ctx context.Context, desired repo.ModelProvider) (repo.ModelProvider, error) {
	existing, err := b.providerRepo.GetBySlug(ctx, desired.Slug)
	switch {
	case err == nil:
		if !existing.IsEnabled {
			return b.providerRepo.SetEnabled(ctx, existing.ID, true)
		}
		return existing, nil
	case errors.Is(err, repo.ErrNotFound):
		return b.providerRepo.Create(ctx, desired)
	default:
		return repo.ModelProvider{}, err
	}
}

func (b *Bootstrapper) upsertOrgProfile(ctx context.Context, organizationID uuid.UUID, providerID uuid.UUID, seed modelProfileSeed) (repo.ModelProfile, error) {
	exists, err := b.currentOrgProfileExists(ctx, organizationID, seed.LogicalProfileID)
	if err != nil {
		return repo.ModelProfile{}, err
	}

	if !exists {
		orgID := organizationID
		return b.profileRepo.Create(ctx, repo.ModelProfile{
			LogicalProfileID:    seed.LogicalProfileID,
			OrganizationID:      &orgID,
			Version:             1,
			IsCurrent:           true,
			ProviderID:          providerID,
			ModelName:           seed.ModelName,
			ContextWindowTokens: seed.ContextWindowTokens,
			MaxOutputTokens:     seed.MaxOutputTokens,
			SupportsStreaming:   seed.SupportsStreaming,
			SupportsVision:      seed.SupportsVision,
			InvocationPurpose:   seed.InvocationPurpose,
		})
	}

	current, err := b.getCurrentOrgProfile(ctx, organizationID, seed.LogicalProfileID)
	switch {
	case err == nil:
		desired := repo.ModelProfile{
			ProviderID:          providerID,
			ModelName:           seed.ModelName,
			ContextWindowTokens: seed.ContextWindowTokens,
			MaxOutputTokens:     seed.MaxOutputTokens,
			SupportsStreaming:   seed.SupportsStreaming,
			SupportsVision:      seed.SupportsVision,
			InvocationPurpose:   seed.InvocationPurpose,
		}
		if !profileNeedsRotation(current, desired) {
			return current, nil
		}
		return b.profileRepo.Deprecate(ctx, current.ID, desired)
	case errors.Is(err, repo.ErrNotFound):
		orgID := organizationID
		return b.profileRepo.Create(ctx, repo.ModelProfile{
			LogicalProfileID:    seed.LogicalProfileID,
			OrganizationID:      &orgID,
			Version:             1,
			IsCurrent:           true,
			ProviderID:          providerID,
			ModelName:           seed.ModelName,
			ContextWindowTokens: seed.ContextWindowTokens,
			MaxOutputTokens:     seed.MaxOutputTokens,
			SupportsStreaming:   seed.SupportsStreaming,
			SupportsVision:      seed.SupportsVision,
			InvocationPurpose:   seed.InvocationPurpose,
		})
	default:
		return repo.ModelProfile{}, err
	}
}

func profileNeedsRotation(current repo.ModelProfile, desired repo.ModelProfile) bool {
	return current.ProviderID != desired.ProviderID ||
		current.ModelName != desired.ModelName ||
		current.ContextWindowTokens != desired.ContextWindowTokens ||
		current.MaxOutputTokens != desired.MaxOutputTokens ||
		current.SupportsStreaming != desired.SupportsStreaming ||
		current.SupportsVision != desired.SupportsVision ||
		current.InvocationPurpose != desired.InvocationPurpose
}

func (b *Bootstrapper) getCurrentOrgProfile(ctx context.Context, organizationID uuid.UUID, logicalID string) (repo.ModelProfile, error) {
	if b.rowQuerier == nil {
		return repo.ModelProfile{}, fmt.Errorf("row querier is not configured")
	}

	row := b.rowQuerier.QueryRow(ctx, `
		SELECT id, logical_profile_id, organization_id, version, is_current, provider_id, model_name, context_window_tokens, max_output_tokens, supports_streaming, supports_vision, temperature, invocation_purpose, fallback_profile_id, created_at, updated_at
		FROM model_profile
		WHERE organization_id = $1
		  AND logical_profile_id = $2
		  AND is_current = true
		LIMIT 1
	`, organizationID, logicalID)

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
		return repo.ModelProfile{}, repo.ErrNotFound
	}
	if err != nil {
		return repo.ModelProfile{}, err
	}
	return profile, nil
}

func (b *Bootstrapper) stubForTable(stepNumber int, stepName, tableName string) StepFunc {
	return func(_ context.Context, _ *State) error {
		return skipped(fmt.Sprintf("step %d (%s) skipped - waiting for table %s to be registered", stepNumber, stepName, tableName))
	}
}

func normalizeStepName(name string) string {
	normalized := strings.TrimSpace(name)
	if normalized == "" {
		return ""
	}
	if alias, ok := stepNameAliases[normalized]; ok {
		return alias
	}
	return normalized
}

func loadDefaultSkillSeeds() []defaultSkillSeed {
	matches, err := fs.Glob(defaultSkillAssets, "defaults/skills/*.md")
	if err != nil || len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)

	seeds := make([]defaultSkillSeed, 0, len(matches))
	for _, match := range matches {
		content, readErr := fs.ReadFile(defaultSkillAssets, match)
		if readErr != nil {
			continue
		}
		fileName := filepath.Base(match)
		slug := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		display, description := inferSkillMetadata(slug, content)
		seeds = append(seeds, defaultSkillSeed{
			Slug:        slug,
			DisplayName: display,
			Description: description,
			FilePath:    filepath.ToSlash(filepath.Join("skills", fileName)),
			Content:     content,
		})
	}
	return seeds
}

func inferSkillMetadata(slug string, content []byte) (string, string) {
	display := slugToDisplay(slug)
	description := fmt.Sprintf("Default skill: %s", slug)

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			candidate := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if candidate != "" {
				display = candidate
			}
			continue
		}
		description = trimmed
		break
	}
	return display, description
}

func slugToDisplay(slug string) string {
	parts := strings.Split(slug, "-")
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, " ")
}

func runStep(ctx context.Context, fn StepFunc, state *State) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	if fn == nil {
		return fmt.Errorf("step is not configured")
	}
	return fn(ctx, state)
}

func progressWriter(w io.Writer) ProgressFn {
	if w == nil {
		return nil
	}
	return func(progress Progress) {
		details := strings.TrimSpace(progress.Details)
		if details != "" {
			_, _ = fmt.Fprintf(w, "step %d (%s): %s - %s\n", progress.Step, progress.Name, progress.Status, details)
			return
		}
		_, _ = fmt.Fprintf(w, "step %d (%s): %s\n", progress.Step, progress.Name, progress.Status)
	}
}
