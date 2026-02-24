//go:build integration

package budget

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

type integrationClock struct {
	now time.Time
}

func (c integrationClock) Now() time.Time {
	return c.now
}

type inboxNotificationService struct {
	pool *pgxpool.Pool
}

func (s inboxNotificationService) CreateSystemAlert(ctx context.Context, orgID uuid.UUID, _ *string, description string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO inbox_item (organization_id, item_type, created_by_type, created_by_id, title, body)
		VALUES ($1, 'system_alert', 'system', $2, $3, $4)
	`, orgID, uuid.Nil, "Budget alert", description)
	return err
}

func TestCheckBudgetSumsModelInvocationTokens(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	budgetRepo := repo.NewTokenBudgetRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "budget-check-org",
		DisplayName: "Budget Check Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "budget-provider",
		DisplayName: "Budget Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	projectA, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "project-a",
		DisplayName:    "Project A",
		Description:    "budget usage scope",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projectB, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "project-b",
		DisplayName:    "Project B",
		Description:    "budget usage scope",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}

	hard := int64(300)
	if _, err := budgetRepo.Create(ctx, repo.TokenBudget{
		OrganizationID:  org.ID,
		ProjectID:       &projectA.ID,
		Period:          "daily",
		HardLimitTokens: &hard,
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	}); err != nil {
		t.Fatalf("create budget: %v", err)
	}

	createdAt := time.Now().UTC()
	insertRow := func(projectID uuid.UUID, input, output int64) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO model_invocation (organization_id, model_provider_id, project_id, invocation_purpose, status, model_name, input_tokens, output_tokens, created_at)
			VALUES ($1, $2, $3, 'agent_turn', 'completed', $4, $5, $6, $7)
		`, org.ID, provider.ID, projectID, "budget-model", input, output, createdAt); err != nil {
			t.Fatalf("insert model_invocation row: %v", err)
		}
	}
	insertRow(projectA.ID, 50, 50)  // 100
	insertRow(projectA.ID, 20, 30)  // 50
	insertRow(projectB.ID, 90, 110) // 200 (must not count for projectA)

	svc, err := NewService(Options{
		Pool:    pool,
		Budgets: budgetRepo,
		Usage:   NewPostgresUsageQuerier(pool),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	okResult, err := svc.CheckBudget(ctx, org.ID, &projectA.ID, nil, 100) // 150 + 100 = 250
	if err != nil {
		t.Fatalf("CheckBudget allowed call: %v", err)
	}
	if !okResult.Allowed {
		t.Fatalf("Allowed = %v, want true", okResult.Allowed)
	}
	if okResult.CurrentTokens != 250 {
		t.Fatalf("CurrentTokens = %d, want 250", okResult.CurrentTokens)
	}

	blockedResult, err := svc.CheckBudget(ctx, org.ID, &projectA.ID, nil, 200) // 150 + 200 = 350
	if err != nil {
		t.Fatalf("CheckBudget blocked call: %v", err)
	}
	if blockedResult.Allowed {
		t.Fatalf("Allowed = %v, want false", blockedResult.Allowed)
	}
	if !blockedResult.HardLimitHit {
		t.Fatalf("HardLimitHit = %v, want true", blockedResult.HardLimitHit)
	}
}

func TestCheckBudgetWarnsOncePerPeriodWithInboxInsert(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	budgetRepo := repo.NewTokenBudgetRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "budget-soft-alert-org",
		DisplayName: "Budget Soft Alert Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "budget-soft-provider",
		DisplayName: "Budget Soft Provider",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	soft := int64(100)
	hard := int64(1000)
	budget, err := budgetRepo.Create(ctx, repo.TokenBudget{
		OrganizationID:  org.ID,
		Period:          "daily",
		SoftLimitTokens: &soft,
		HardLimitTokens: &hard,
		IsEnabled:       true,
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create budget: %v", err)
	}

	now := time.Date(2026, time.February, 24, 10, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO model_invocation (organization_id, model_provider_id, invocation_purpose, status, model_name, input_tokens, output_tokens, created_at)
		VALUES ($1, $2, 'agent_turn', 'completed', $3, $4, $5, $6)
	`, org.ID, provider.ID, "budget-model", int64(70), int64(50), now.Add(2*time.Hour)); err != nil {
		t.Fatalf("insert model_invocation: %v", err)
	}

	svc, err := NewService(Options{
		Pool:          pool,
		Budgets:       budgetRepo,
		Usage:         NewPostgresUsageQuerier(pool),
		Notifications: inboxNotificationService{pool: pool},
		Clock:         integrationClock{now: now},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	first, err := svc.CheckBudget(ctx, org.ID, nil, nil, 0)
	if err != nil {
		t.Fatalf("first CheckBudget: %v", err)
	}
	if !first.Allowed || !first.SoftLimitHit {
		t.Fatalf("first result = %+v, want allowed soft hit", first)
	}

	second, err := svc.CheckBudget(ctx, org.ID, nil, nil, 0)
	if err != nil {
		t.Fatalf("second CheckBudget: %v", err)
	}
	if !second.Allowed || !second.SoftLimitHit {
		t.Fatalf("second result = %+v, want allowed soft hit", second)
	}

	var alertCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM inbox_item WHERE organization_id = $1`, org.ID).Scan(&alertCount); err != nil {
		t.Fatalf("count inbox_item: %v", err)
	}
	if alertCount != 1 {
		t.Fatalf("inbox_item count = %d, want 1", alertCount)
	}

	stored, err := budgetRepo.GetByID(ctx, budget.ID)
	if err != nil {
		t.Fatalf("GetByID budget: %v", err)
	}
	if stored.LastWarnedPeriod == nil || *stored.LastWarnedPeriod == "" {
		t.Fatalf("last_warned_period = %v, want non-empty", stored.LastWarnedPeriod)
	}
}
