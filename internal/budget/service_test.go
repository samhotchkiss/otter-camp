package budget

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type fakeClock struct {
	now time.Time
}

func (c fakeClock) Now() time.Time {
	return c.now
}

type fakeBudgetRepo struct {
	getByID map[uuid.UUID]repo.TokenBudget
	byOrg   map[uuid.UUID][]repo.TokenBudget
	all     []repo.TokenBudget

	updateCalls int
}

func (f *fakeBudgetRepo) Create(context.Context, repo.TokenBudget) (repo.TokenBudget, error) {
	return repo.TokenBudget{}, nil
}

func (f *fakeBudgetRepo) GetByID(_ context.Context, id uuid.UUID) (repo.TokenBudget, error) {
	if budget, ok := f.getByID[id]; ok {
		return budget, nil
	}
	return repo.TokenBudget{}, repo.ErrNotFound
}

func (f *fakeBudgetRepo) GetByScope(context.Context, uuid.UUID, *uuid.UUID, string) (repo.TokenBudget, error) {
	return repo.TokenBudget{}, repo.ErrNotFound
}

func (f *fakeBudgetRepo) List(_ context.Context, orgID uuid.UUID) ([]repo.TokenBudget, error) {
	return cloneBudgets(f.byOrg[orgID]), nil
}

func (f *fakeBudgetRepo) ListAll(context.Context) ([]repo.TokenBudget, error) {
	return cloneBudgets(f.all), nil
}

func (f *fakeBudgetRepo) Update(_ context.Context, budget repo.TokenBudget) (repo.TokenBudget, error) {
	f.updateCalls++
	f.getByID[budget.ID] = budget
	f.replaceInOrg(budget)
	f.replaceInAll(budget)
	return budget, nil
}

func (f *fakeBudgetRepo) Delete(context.Context, uuid.UUID) error {
	return nil
}

func (f *fakeBudgetRepo) replaceInOrg(updated repo.TokenBudget) {
	items := f.byOrg[updated.OrganizationID]
	for i := range items {
		if items[i].ID == updated.ID {
			items[i] = updated
			f.byOrg[updated.OrganizationID] = items
			return
		}
	}
	f.byOrg[updated.OrganizationID] = append(items, updated)
}

func (f *fakeBudgetRepo) replaceInAll(updated repo.TokenBudget) {
	for i := range f.all {
		if f.all[i].ID == updated.ID {
			f.all[i] = updated
			return
		}
	}
	f.all = append(f.all, updated)
}

func cloneBudgets(in []repo.TokenBudget) []repo.TokenBudget {
	out := make([]repo.TokenBudget, len(in))
	copy(out, in)
	return out
}

type fakeUsageQuerier struct {
	sumFn         func(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, since time.Time) (int64, error)
	dailyRollups  []DailyRollup
	dailyRollupFn func(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, days int) ([]DailyRollup, error)
}

func (f fakeUsageQuerier) SumTokens(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, since time.Time) (int64, error) {
	if f.sumFn == nil {
		return 0, nil
	}
	return f.sumFn(ctx, orgID, projectID, agentID, since)
}

func (f fakeUsageQuerier) DailyRollups(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, days int) ([]DailyRollup, error) {
	if f.dailyRollupFn != nil {
		return f.dailyRollupFn(ctx, orgID, projectID, days)
	}
	return f.dailyRollups, nil
}

type fakeNotificationService struct {
	messages []string
}

func (f *fakeNotificationService) CreateSystemAlert(_ context.Context, _ uuid.UUID, _ *string, description string) error {
	f.messages = append(f.messages, description)
	return nil
}

type fakeEventPublisher struct {
	events []eventbus.DomainEvent
}

func (f *fakeEventPublisher) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	f.events = append(f.events, event)
	return nil
}

func TestCheckBudgetOutcomes(t *testing.T) {
	orgID := uuid.New()
	now := time.Date(2026, time.March, 10, 9, 0, 0, 0, time.UTC)
	soft := int64(100)
	hard := int64(200)

	cases := []struct {
		name         string
		currentUsage int64
		estimate     int64
		wantAllowed  bool
		wantSoft     bool
		wantHard     bool
	}{
		{name: "below soft", currentUsage: 80, estimate: 10, wantAllowed: true, wantSoft: false, wantHard: false},
		{name: "between soft and hard", currentUsage: 100, estimate: 10, wantAllowed: true, wantSoft: true, wantHard: false},
		{name: "at hard", currentUsage: 190, estimate: 10, wantAllowed: false, wantSoft: false, wantHard: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := repo.TokenBudget{
				ID:              uuid.New(),
				OrganizationID:  orgID,
				Period:          "daily",
				SoftLimitTokens: &soft,
				HardLimitTokens: &hard,
				IsEnabled:       true,
			}
			repository := &fakeBudgetRepo{
				getByID: map[uuid.UUID]repo.TokenBudget{budget.ID: budget},
				byOrg:   map[uuid.UUID][]repo.TokenBudget{orgID: {budget}},
				all:     []repo.TokenBudget{budget},
			}
			notifier := &fakeNotificationService{}
			svc := &service{
				budgets: repository,
				usage: fakeUsageQuerier{sumFn: func(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID, time.Time) (int64, error) {
					return tc.currentUsage, nil
				}},
				notifications: notifier,
				clock:         fakeClock{now: now},
			}

			got, err := svc.CheckBudget(context.Background(), orgID, nil, nil, tc.estimate)
			if err != nil {
				t.Fatalf("CheckBudget error: %v", err)
			}
			if got.Allowed != tc.wantAllowed {
				t.Fatalf("Allowed = %v, want %v", got.Allowed, tc.wantAllowed)
			}
			if got.SoftLimitHit != tc.wantSoft {
				t.Fatalf("SoftLimitHit = %v, want %v", got.SoftLimitHit, tc.wantSoft)
			}
			if got.HardLimitHit != tc.wantHard {
				t.Fatalf("HardLimitHit = %v, want %v", got.HardLimitHit, tc.wantHard)
			}
		})
	}
}

func TestCheckBudgetWarnsOnlyOncePerPeriod(t *testing.T) {
	orgID := uuid.New()
	now := time.Date(2026, time.January, 14, 11, 0, 0, 0, time.UTC)
	soft := int64(100)
	hard := int64(1000)
	budget := repo.TokenBudget{
		ID:              uuid.New(),
		OrganizationID:  orgID,
		Period:          "daily",
		SoftLimitTokens: &soft,
		HardLimitTokens: &hard,
		IsEnabled:       true,
	}

	repository := &fakeBudgetRepo{
		getByID: map[uuid.UUID]repo.TokenBudget{budget.ID: budget},
		byOrg:   map[uuid.UUID][]repo.TokenBudget{orgID: {budget}},
		all:     []repo.TokenBudget{budget},
	}
	notifier := &fakeNotificationService{}
	svc := &service{
		budgets:       repository,
		usage:         fakeUsageQuerier{sumFn: func(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID, time.Time) (int64, error) { return 120, nil }},
		notifications: notifier,
		clock:         fakeClock{now: now},
	}

	if _, err := svc.CheckBudget(context.Background(), orgID, nil, nil, 0); err != nil {
		t.Fatalf("first CheckBudget error: %v", err)
	}
	if _, err := svc.CheckBudget(context.Background(), orgID, nil, nil, 0); err != nil {
		t.Fatalf("second CheckBudget error: %v", err)
	}

	if len(notifier.messages) != 1 {
		t.Fatalf("alert count = %d, want 1", len(notifier.messages))
	}
	if repository.updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", repository.updateCalls)
	}
}

func TestPeriodWindow(t *testing.T) {
	now := time.Date(2026, time.January, 7, 15, 4, 0, 0, time.UTC) // Wednesday

	dailyStart, dailyKey, err := periodWindow("daily", now)
	if err != nil {
		t.Fatalf("daily periodWindow error: %v", err)
	}
	if want := "2026-01-07"; dailyKey != want {
		t.Fatalf("daily key = %q, want %q", dailyKey, want)
	}
	if want := time.Date(2026, time.January, 7, 0, 0, 0, 0, time.UTC); !dailyStart.Equal(want) {
		t.Fatalf("daily start = %v, want %v", dailyStart, want)
	}

	weeklyStart, weeklyKey, err := periodWindow("weekly", now)
	if err != nil {
		t.Fatalf("weekly periodWindow error: %v", err)
	}
	if want := "2026-W02"; weeklyKey != want {
		t.Fatalf("weekly key = %q, want %q", weeklyKey, want)
	}
	if want := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC); !weeklyStart.Equal(want) {
		t.Fatalf("weekly start = %v, want %v", weeklyStart, want)
	}

	monthlyStart, monthlyKey, err := periodWindow("monthly", now)
	if err != nil {
		t.Fatalf("monthly periodWindow error: %v", err)
	}
	if want := "2026-01"; monthlyKey != want {
		t.Fatalf("monthly key = %q, want %q", monthlyKey, want)
	}
	if want := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC); !monthlyStart.Equal(want) {
		t.Fatalf("monthly start = %v, want %v", monthlyStart, want)
	}
}

func TestScanForAnomaliesThresholds(t *testing.T) {
	now := time.Date(2026, time.May, 18, 8, 0, 0, 0, time.UTC)
	orgID := uuid.New()
	budget := repo.TokenBudget{
		ID:             uuid.New(),
		OrganizationID: orgID,
		Period:         "daily",
		IsEnabled:      true,
	}

	tests := []struct {
		name             string
		currentUsage     int64
		wantAlerts       int
		wantDomainEvents int
	}{
		{name: "anomaly at 3.1x", currentUsage: 310, wantAlerts: 1, wantDomainEvents: 1},
		{name: "no anomaly at 2.9x", currentUsage: 290, wantAlerts: 0, wantDomainEvents: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repository := &fakeBudgetRepo{
				getByID: map[uuid.UUID]repo.TokenBudget{budget.ID: budget},
				byOrg:   map[uuid.UUID][]repo.TokenBudget{orgID: {budget}},
				all:     []repo.TokenBudget{budget},
			}
			notifier := &fakeNotificationService{}
			publisher := &fakeEventPublisher{}
			svc := &service{
				budgets: repository,
				usage: fakeUsageQuerier{
					sumFn: func(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID, time.Time) (int64, error) {
						return tc.currentUsage, nil
					},
					dailyRollups: []DailyRollup{
						{Tokens: 100}, {Tokens: 100}, {Tokens: 100}, {Tokens: 100}, {Tokens: 100}, {Tokens: 100}, {Tokens: 100},
					},
				},
				notifications: notifier,
				events:        publisher,
				clock:         fakeClock{now: now},
			}

			if err := svc.ScanForAnomalies(context.Background()); err != nil {
				t.Fatalf("ScanForAnomalies error: %v", err)
			}
			if len(notifier.messages) != tc.wantAlerts {
				t.Fatalf("alert count = %d, want %d", len(notifier.messages), tc.wantAlerts)
			}
			if len(publisher.events) != tc.wantDomainEvents {
				t.Fatalf("event count = %d, want %d", len(publisher.events), tc.wantDomainEvents)
			}
			if len(publisher.events) > 0 {
				payload := map[string]any{}
				if err := json.Unmarshal(publisher.events[0].Payload, &payload); err != nil {
					t.Fatalf("unmarshal anomaly payload: %v", err)
				}
			}
		})
	}
}
