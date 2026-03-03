package project

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
)

func TestValidateSlug(t *testing.T) {
	cases := []struct {
		name    string
		slug    string
		wantErr bool
	}{
		{name: "valid simple", slug: "alpha", wantErr: false},
		{name: "valid dashed", slug: "project-123", wantErr: false},
		{name: "invalid uppercase", slug: "Project", wantErr: true},
		{name: "invalid space", slug: "my project", wantErr: true},
		{name: "invalid leading hyphen", slug: "-alpha", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateSlug(tc.slug)
			if tc.wantErr && !errors.Is(err, ErrInvalidSlug) {
				t.Fatalf("validateSlug(%q) err = %v, want ErrInvalidSlug", tc.slug, err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateSlug(%q) err = %v, want nil", tc.slug, err)
			}
		})
	}
}

func TestComputeNextFireAtValidation(t *testing.T) {
	now := time.Date(2026, time.February, 24, 8, 15, 0, 0, time.UTC)
	svc := &service{
		clock:      clock.NewFake(now),
		cronParser: scheduling.NewCronParser(),
	}

	next, err := svc.computeNextFireAt("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("computeNextFireAt valid expression: %v", err)
	}
	want := time.Date(2026, time.February, 24, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next_fire_at = %s, want %s", next, want)
	}

	_, err = svc.computeNextFireAt("not-a-cron")
	if !errors.Is(err, ErrInvalidCronExpression) {
		t.Fatalf("invalid expression err = %v, want ErrInvalidCronExpression", err)
	}
}

func TestProjectCreatePublishesStaffingNeededEvent(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	projects := &fakeProjectCreateRepo{
		createdID: projectID,
	}
	events := &fakeProjectEventBus{}
	svc := &service{
		projects: projects,
		events:   events,
	}

	created, err := svc.Create(context.Background(), CreateProjectRequest{
		OrganizationID: orgID,
		Slug:           "staffing-project",
		DisplayName:    "Staffing Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		Settings:       json.RawMessage(`{"custom":"value"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != projectID {
		t.Fatalf("created id = %s, want %s", created.ID, projectID)
	}
	if len(events.events) != 2 {
		t.Fatalf("published events = %d, want 2", len(events.events))
	}
	if events.events[0].EventType != "project.created" {
		t.Fatalf("event[0].type = %q, want project.created", events.events[0].EventType)
	}
	if events.events[1].EventType != "project.staffing_needed" {
		t.Fatalf("event[1].type = %q, want project.staffing_needed", events.events[1].EventType)
	}
	if !projectRequiresPMQueueSetting(projects.lastCreated.Settings) {
		t.Fatalf("project settings = %s, want requires_pm_assignment_before_queue=true", string(projects.lastCreated.Settings))
	}
}

func TestWithProjectStaffingDefaultsHandlesJSONNull(t *testing.T) {
	settings := withProjectStaffingDefaults(json.RawMessage("null"))
	if !projectRequiresPMQueueSetting(settings) {
		t.Fatalf("settings = %s, want requires_pm_assignment_before_queue=true", string(settings))
	}
}

type fakeProjectCreateRepo struct {
	createdID   uuid.UUID
	lastCreated repo.Project
}

func (f *fakeProjectCreateRepo) Create(_ context.Context, project repo.Project) (repo.Project, error) {
	project.ID = f.createdID
	f.lastCreated = project
	return project, nil
}

func (f *fakeProjectCreateRepo) GetByID(context.Context, uuid.UUID) (repo.Project, error) {
	return repo.Project{}, repo.ErrNotFound
}

func (f *fakeProjectCreateRepo) GetBySlug(context.Context, uuid.UUID, string) (repo.Project, error) {
	return repo.Project{}, repo.ErrNotFound
}

func (f *fakeProjectCreateRepo) List(context.Context, uuid.UUID) ([]repo.Project, error) {
	return nil, nil
}

func (f *fakeProjectCreateRepo) Update(context.Context, repo.Project) (repo.Project, error) {
	return repo.Project{}, nil
}

func (f *fakeProjectCreateRepo) Archive(context.Context, uuid.UUID) error { return nil }

func (f *fakeProjectCreateRepo) Delete(context.Context, uuid.UUID) error { return nil }

type fakeProjectEventBus struct {
	events []eventbus.DomainEvent
}

func (f *fakeProjectEventBus) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	f.events = append(f.events, event)
	return nil
}

func (f *fakeProjectEventBus) Subscribe(string, *uuid.UUID, eventbus.EventHandler) eventbus.Subscription {
	return eventbus.Subscription{}
}

func (f *fakeProjectEventBus) Unsubscribe(eventbus.Subscription) {}

func projectRequiresPMQueueSetting(settings json.RawMessage) bool {
	var payload map[string]any
	if err := json.Unmarshal(settings, &payload); err != nil {
		return false
	}
	raw, ok := payload["requires_pm_assignment_before_queue"]
	if !ok {
		return false
	}
	flag, ok := raw.(bool)
	return ok && flag
}
