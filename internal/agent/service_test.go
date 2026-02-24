package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type fakeAgentRepo struct {
	created         repo.Agent
	createdResult   repo.Agent
	countActiveTemp int
	countErr        error
	createErr       error
}

func (f *fakeAgentRepo) Create(_ context.Context, agent repo.Agent) (repo.Agent, error) {
	f.created = agent
	if f.createErr != nil {
		return repo.Agent{}, f.createErr
	}
	if f.createdResult.ID == uuid.Nil {
		f.createdResult = agent
		f.createdResult.ID = uuid.New()
	}
	return f.createdResult, nil
}

func (f *fakeAgentRepo) GetByID(context.Context, uuid.UUID) (repo.Agent, error) {
	return repo.Agent{}, repo.ErrNotFound
}

func (f *fakeAgentRepo) List(context.Context, uuid.UUID) ([]repo.Agent, error) {
	return nil, nil
}

func (f *fakeAgentRepo) Update(context.Context, repo.Agent) (repo.Agent, error) {
	return repo.Agent{}, nil
}

func (f *fakeAgentRepo) CountActiveTemps(context.Context, uuid.UUID) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.countActiveTemp, nil
}

type fixedClock struct {
	now time.Time
}

func (f fixedClock) Now() time.Time {
	return f.now
}

func TestValidateStaffTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		current   string
		target    string
		eventType string
		wantErr   bool
	}{
		{name: "draft to active", current: statusDraft, target: statusActive, eventType: "agent.activated"},
		{name: "active to paused", current: statusActive, target: statusPaused, eventType: "agent.paused"},
		{name: "paused to active", current: statusPaused, target: statusActive, eventType: "agent.unpaused"},
		{name: "active to retired", current: statusActive, target: statusRetired, eventType: "agent.retired"},
		{name: "active to cancelled", current: statusActive, target: statusCancelled, eventType: "agent.cancelled"},
		{name: "draft to cancelled", current: statusDraft, target: statusCancelled, eventType: "agent.cancelled"},
		{name: "paused to retired invalid", current: statusPaused, target: statusRetired, wantErr: true},
		{name: "draft to paused invalid", current: statusDraft, target: statusPaused, wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eventType, err := validateStaffTransition(tc.current, tc.target)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("validateStaffTransition error = %v, want ErrInvalidTransition", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateStaffTransition returned error: %v", err)
			}
			if eventType != tc.eventType {
				t.Fatalf("eventType = %q, want %q", eventType, tc.eventType)
			}
		})
	}
}

func TestValidateTempTransitionAndStaffGuards(t *testing.T) {
	t.Parallel()

	if err := ensureStaffClass(agentClassStaff); err != nil {
		t.Fatalf("ensureStaffClass(staff) error = %v, want nil", err)
	}
	if !errors.Is(ensureStaffClass(agentClassTemp), ErrInvalidForTempAgent) {
		t.Fatalf("ensureStaffClass(temp) should return ErrInvalidForTempAgent")
	}

	if err := validateTempTransition(statusActive, statusExpired); err != nil {
		t.Fatalf("validateTempTransition(active->expired): %v", err)
	}
	if err := validateTempTransition(statusActive, statusPromoted); err != nil {
		t.Fatalf("validateTempTransition(active->promoted): %v", err)
	}
	if !errors.Is(validateTempTransition(statusPaused, statusExpired), ErrInvalidTransition) {
		t.Fatalf("validateTempTransition(paused->expired) should return ErrInvalidTransition")
	}
}

func TestEnforceMaxConcurrentTemps(t *testing.T) {
	t.Parallel()

	repoAtLimit := &fakeAgentRepo{countActiveTemp: 2}
	svcAtLimit := &service{
		agents: repoAtLimit,
		maxConcurrentTempsLookup: func(context.Context, uuid.UUID) (int, error) {
			return 2, nil
		},
	}

	err := svcAtLimit.EnforceMaxConcurrentTemps(context.Background(), uuid.New())
	if !errors.Is(err, ErrConcurrentTempLimitReached) {
		t.Fatalf("EnforceMaxConcurrentTemps error = %v, want ErrConcurrentTempLimitReached", err)
	}

	repoUnderLimit := &fakeAgentRepo{countActiveTemp: 1}
	svcUnderLimit := &service{
		agents: repoUnderLimit,
		maxConcurrentTempsLookup: func(context.Context, uuid.UUID) (int, error) {
			return 2, nil
		},
	}

	if err := svcUnderLimit.EnforceMaxConcurrentTemps(context.Background(), uuid.New()); err != nil {
		t.Fatalf("EnforceMaxConcurrentTemps under limit returned error: %v", err)
	}
}

func TestCreateTempComputesExpiresAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	ttl := int32(3600)
	projectID := uuid.New()
	repoFake := &fakeAgentRepo{countActiveTemp: 0}
	svc := &service{
		agents: repoFake,
		clock:  fixedClock{now: now},
		maxConcurrentTempsLookup: func(context.Context, uuid.UUID) (int, error) {
			return 5, nil
		},
	}

	_, err := svc.CreateTemp(context.Background(), uuid.New(), CreateTempAgentRequest{
		DisplayName:    "Temp worker",
		AgentType:      "worker",
		TempProjectID:  projectID,
		TempTTLSeconds: &ttl,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTemp returned error: %v", err)
	}

	if repoFake.created.TempExpiresAt == nil {
		t.Fatalf("TempExpiresAt = nil, want non-nil")
	}
	want := now.Add(1 * time.Hour)
	if !repoFake.created.TempExpiresAt.Equal(want) {
		t.Fatalf("TempExpiresAt = %v, want %v", *repoFake.created.TempExpiresAt, want)
	}
}

func TestComputeTempExpiresAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.February, 1, 10, 0, 0, 0, time.UTC)
	ttl := int32(3600)
	expiresAt := computeTempExpiresAt(now, &ttl)
	if expiresAt == nil {
		t.Fatalf("computeTempExpiresAt returned nil")
	}
	if !expiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("expiresAt = %v, want %v", *expiresAt, now.Add(time.Hour))
	}
}
