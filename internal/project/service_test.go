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
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
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

func TestProjectCreateRejectsHTMLDisplayName(t *testing.T) {
	svc := &service{
		projects: &fakeProjectCreateRepo{createdID: uuid.New()},
		events:   &fakeProjectEventBus{},
	}

	_, err := svc.Create(context.Background(), CreateProjectRequest{
		OrganizationID: uuid.New(),
		Slug:           "safe-slug",
		DisplayName:    "<script>alert(1)</script>",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
	})
	if !errors.Is(err, ErrDisplayNameInvalid) {
		t.Fatalf("Create err = %v, want ErrDisplayNameInvalid", err)
	}
}

func TestProjectPauseResumePublishesEvents(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	userID := uuid.New()
	now := time.Date(2026, time.March, 5, 18, 0, 0, 0, time.UTC)

	projects := &fakeProjectMutableRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: orgID,
				Slug:           "paused-project",
				DisplayName:    "Paused Project",
				Settings:       json.RawMessage(`{}`),
			},
		},
	}
	events := &fakeProjectEventBus{}
	svc := &service{
		projects: projects,
		events:   events,
		clock:    clock.NewFake(now),
	}

	paused, err := svc.Pause(context.Background(), orgID, projectID, PauseProjectRequest{
		Reason:       "operator pause",
		Metadata:     json.RawMessage(`{"source":"unit-test"}`),
		PausedByType: "human_user",
		PausedByID:   userID,
	})
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}

	pauseState := projectpause.Parse(paused.Settings)
	if !pauseState.IsPaused {
		t.Fatal("pause state is_paused = false, want true")
	}
	if pauseState.Reason != "operator pause" {
		t.Fatalf("pause reason = %q, want %q", pauseState.Reason, "operator pause")
	}
	if len(events.events) != 1 || events.events[0].EventType != "project.paused" {
		t.Fatalf("pause events = %+v, want project.paused", events.events)
	}

	resumed, err := svc.Resume(context.Background(), orgID, projectID, "human_user", userID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if projectpause.Parse(resumed.Settings).IsPaused {
		t.Fatal("pause state after resume = true, want false")
	}
	if len(events.events) != 2 || events.events[1].EventType != "project.resumed" {
		t.Fatalf("events = %+v, want trailing project.resumed", events.events)
	}
}

func TestWithProjectStaffingDefaultsHandlesJSONNull(t *testing.T) {
	settings := withProjectStaffingDefaults(json.RawMessage("null"))
	if !projectRequiresPMQueueSetting(settings) {
		t.Fatalf("settings = %s, want requires_pm_assignment_before_queue=true", string(settings))
	}
}

func TestValidateFlowTemplateReviewPathRejectsWorkToMergeWithoutReview(t *testing.T) {
	mergeID := uuid.New()
	work := repo.FlowNode{ID: uuid.New(), NodeType: "work", NextNodeID: &mergeID}
	merge := repo.FlowNode{ID: mergeID, NodeType: "merge"}

	err := validateFlowTemplateReviewPath(work.ID, []repo.FlowNode{work, merge})
	if !errors.Is(err, ErrFlowTemplateReviewPath) {
		t.Fatalf("validateFlowTemplateReviewPath err = %v, want ErrFlowTemplateReviewPath", err)
	}
}

func TestValidateFlowTemplateReviewPathAcceptsWorkReviewMerge(t *testing.T) {
	mergeID := uuid.New()
	reviewID := uuid.New()
	work := repo.FlowNode{ID: uuid.New(), NodeType: "work", NextNodeID: &reviewID}
	review := repo.FlowNode{ID: reviewID, NodeType: "review", NextNodeID: &mergeID, RejectNodeID: &mergeID}
	merge := repo.FlowNode{ID: mergeID, NodeType: "merge"}

	if err := validateFlowTemplateReviewPath(work.ID, []repo.FlowNode{work, review, merge}); err != nil {
		t.Fatalf("validateFlowTemplateReviewPath err = %v, want nil", err)
	}
}

func TestValidateFlowTemplateReviewPathRejectsBranchWithoutReview(t *testing.T) {
	mergeID := uuid.New()
	reviewID := uuid.New()
	work := repo.FlowNode{ID: uuid.New(), NodeType: "work", NextNodeID: &mergeID, RejectNodeID: &reviewID}
	review := repo.FlowNode{ID: reviewID, NodeType: "review", NextNodeID: &mergeID, RejectNodeID: &mergeID}
	merge := repo.FlowNode{ID: mergeID, NodeType: "merge"}

	err := validateFlowTemplateReviewPath(work.ID, []repo.FlowNode{work, review, merge})
	if !errors.Is(err, ErrFlowTemplateReviewPath) {
		t.Fatalf("validateFlowTemplateReviewPath err = %v, want ErrFlowTemplateReviewPath", err)
	}
}

func TestValidateFlowTemplateReviewPathAcceptsAllBranchesThroughReview(t *testing.T) {
	mergeID := uuid.New()
	reviewAID := uuid.New()
	reviewBID := uuid.New()
	work := repo.FlowNode{ID: uuid.New(), NodeType: "work", NextNodeID: &reviewAID, RejectNodeID: &reviewBID}
	reviewA := repo.FlowNode{ID: reviewAID, NodeType: "review", NextNodeID: &mergeID, RejectNodeID: &mergeID}
	reviewB := repo.FlowNode{ID: reviewBID, NodeType: "review", NextNodeID: &mergeID, RejectNodeID: &mergeID}
	merge := repo.FlowNode{ID: mergeID, NodeType: "merge"}

	if err := validateFlowTemplateReviewPath(work.ID, []repo.FlowNode{work, reviewA, reviewB, merge}); err != nil {
		t.Fatalf("validateFlowTemplateReviewPath err = %v, want nil", err)
	}
}

func TestValidateFlowTemplateReviewPathRejectsReviewOnlyCompletion(t *testing.T) {
	review := repo.FlowNode{ID: uuid.New(), NodeType: "review"}

	err := validateFlowTemplateReviewPath(review.ID, []repo.FlowNode{review})
	if !errors.Is(err, ErrFlowTemplateReviewPath) {
		t.Fatalf("validateFlowTemplateReviewPath err = %v, want ErrFlowTemplateReviewPath", err)
	}
}

func TestValidateFlowTemplateReviewPathRejectsTerminalWorkAfterReview(t *testing.T) {
	reviewID := uuid.New()
	terminalWorkID := uuid.New()
	work := repo.FlowNode{ID: uuid.New(), NodeType: "work", NextNodeID: &reviewID}
	review := repo.FlowNode{ID: reviewID, NodeType: "review", NextNodeID: &terminalWorkID}
	terminalWork := repo.FlowNode{ID: terminalWorkID, NodeType: "work"}

	err := validateFlowTemplateReviewPath(work.ID, []repo.FlowNode{work, review, terminalWork})
	if !errors.Is(err, ErrFlowTemplateReviewPath) {
		t.Fatalf("validateFlowTemplateReviewPath err = %v, want ErrFlowTemplateReviewPath", err)
	}
}

func TestProjectDeleteCleansHistoricalReferencesBeforeDelete(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	order := make([]string, 0, 3)

	projects := &fakeProjectDeleteRepo{
		project: repo.Project{
			ID:             projectID,
			OrganizationID: orgID,
			Slug:           "cleanup-target",
		},
		order: &order,
	}
	modelInvocations := &fakeProjectDeleteCleanup{order: &order, label: "model_invocations"}
	chatSessions := &fakeProjectDeleteCleanup{order: &order, label: "chat_sessions"}
	events := &fakeProjectEventBus{}

	svc := &service{
		projects:          projects,
		modelInvocations:  modelInvocations,
		chatSessions:      chatSessions,
		activeTaskChecker: fakeProjectTaskActivityChecker{hasActive: false},
		events:            events,
	}

	if err := svc.Delete(ctx, orgID, projectID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got, want := order, []string{"model_invocations", "chat_sessions", "delete"}; !equalStringSlices(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	if len(events.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(events.events))
	}
	if events.events[0].EventType != "project.deleted" {
		t.Fatalf("event type = %q, want project.deleted", events.events[0].EventType)
	}
	if projects.deletedID != projectID {
		t.Fatalf("deleted project id = %s, want %s", projects.deletedID, projectID)
	}
	if modelInvocations.projectID != projectID {
		t.Fatalf("model invocation cleanup project id = %s, want %s", modelInvocations.projectID, projectID)
	}
	if chatSessions.projectID != projectID {
		t.Fatalf("chat cleanup project id = %s, want %s", chatSessions.projectID, projectID)
	}
}

func TestProjectDeleteCleanupFailureStopsDelete(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	order := make([]string, 0, 3)
	cleanupErr := errors.New("cleanup failed")

	projects := &fakeProjectDeleteRepo{
		project: repo.Project{
			ID:             projectID,
			OrganizationID: orgID,
			Slug:           "cleanup-target",
		},
		order: &order,
	}
	modelInvocations := &fakeProjectDeleteCleanup{order: &order, label: "model_invocations"}
	chatSessions := &fakeProjectDeleteCleanup{
		order: &order,
		label: "chat_sessions",
		err:   cleanupErr,
	}
	events := &fakeProjectEventBus{}

	svc := &service{
		projects:          projects,
		modelInvocations:  modelInvocations,
		chatSessions:      chatSessions,
		activeTaskChecker: fakeProjectTaskActivityChecker{hasActive: false},
		events:            events,
	}

	err := svc.Delete(ctx, orgID, projectID)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("Delete err = %v, want %v", err, cleanupErr)
	}
	if got, want := order, []string{"model_invocations", "chat_sessions"}; !equalStringSlices(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	if projects.deletedID != uuid.Nil {
		t.Fatalf("deleted project id = %s, want nil UUID", projects.deletedID)
	}
	if len(events.events) != 0 {
		t.Fatalf("published events = %d, want 0", len(events.events))
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

type fakeProjectMutableRepo struct {
	items map[uuid.UUID]repo.Project
}

func (f *fakeProjectMutableRepo) Create(_ context.Context, project repo.Project) (repo.Project, error) {
	if project.ID == uuid.Nil {
		project.ID = uuid.New()
	}
	if f.items == nil {
		f.items = map[uuid.UUID]repo.Project{}
	}
	f.items[project.ID] = project
	return project, nil
}

func (f *fakeProjectMutableRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.Project{}, repo.ErrNotFound
}

func (f *fakeProjectMutableRepo) GetBySlug(_ context.Context, orgID uuid.UUID, slug string) (repo.Project, error) {
	for _, item := range f.items {
		if item.OrganizationID == orgID && item.Slug == slug {
			return item, nil
		}
	}
	return repo.Project{}, repo.ErrNotFound
}

func (f *fakeProjectMutableRepo) List(context.Context, uuid.UUID) ([]repo.Project, error) {
	items := make([]repo.Project, 0, len(f.items))
	for _, item := range f.items {
		items = append(items, item)
	}
	return items, nil
}

func (f *fakeProjectMutableRepo) Update(_ context.Context, project repo.Project) (repo.Project, error) {
	if f.items == nil {
		f.items = map[uuid.UUID]repo.Project{}
	}
	f.items[project.ID] = project
	return project, nil
}

func (f *fakeProjectMutableRepo) Archive(context.Context, uuid.UUID) error { return nil }

func (f *fakeProjectMutableRepo) Delete(context.Context, uuid.UUID) error { return nil }

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

type fakeProjectDeleteRepo struct {
	project   repo.Project
	order     *[]string
	deletedID uuid.UUID
}

func (f *fakeProjectDeleteRepo) Create(_ context.Context, project repo.Project) (repo.Project, error) {
	return project, nil
}

func (f *fakeProjectDeleteRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	if id != f.project.ID {
		return repo.Project{}, repo.ErrNotFound
	}
	return f.project, nil
}

func (f *fakeProjectDeleteRepo) GetBySlug(context.Context, uuid.UUID, string) (repo.Project, error) {
	return repo.Project{}, repo.ErrNotFound
}

func (f *fakeProjectDeleteRepo) List(context.Context, uuid.UUID) ([]repo.Project, error) {
	return nil, nil
}

func (f *fakeProjectDeleteRepo) Update(context.Context, repo.Project) (repo.Project, error) {
	return repo.Project{}, nil
}

func (f *fakeProjectDeleteRepo) Archive(context.Context, uuid.UUID) error { return nil }

func (f *fakeProjectDeleteRepo) Delete(_ context.Context, id uuid.UUID) error {
	if f.order != nil {
		*f.order = append(*f.order, "delete")
	}
	f.deletedID = id
	return nil
}

type fakeProjectDeleteCleanup struct {
	order     *[]string
	label     string
	projectID uuid.UUID
	err       error
}

func (f *fakeProjectDeleteCleanup) ClearProjectReferences(_ context.Context, projectID uuid.UUID) error {
	if f.order != nil {
		*f.order = append(*f.order, f.label)
	}
	f.projectID = projectID
	return f.err
}

func (f *fakeProjectDeleteCleanup) DeleteProjectScoped(_ context.Context, projectID uuid.UUID) error {
	if f.order != nil {
		*f.order = append(*f.order, f.label)
	}
	f.projectID = projectID
	return f.err
}

func (f *fakeProjectDeleteCleanup) CloseProjectScoped(_ context.Context, projectID uuid.UUID) error {
	if f.order != nil {
		*f.order = append(*f.order, f.label)
	}
	f.projectID = projectID
	return f.err
}

type fakeProjectTaskActivityChecker struct {
	hasActive bool
	err       error
}

func (f fakeProjectTaskActivityChecker) HasActiveProjectTasks(context.Context, uuid.UUID) (bool, error) {
	return f.hasActive, f.err
}

func equalStringSlices(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
