package repo

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFlowTemplateRepoDeprecateIdempotency(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	createdAt := time.Now().UTC().Add(-time.Hour)

	current := FlowTemplate{
		ID:             uuid.New(),
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "default-flow",
		DisplayName:    "Default Flow",
		Description:    "v1",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "human_user",
		CreatedByID:    uuid.New(),
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	}

	repo := &inMemoryFlowTemplateRepo{
		templates: []FlowTemplate{current},
	}

	first, err := repo.Deprecate(ctx, current.ID, FlowTemplate{
		DisplayName:   "Default Flow v2",
		Description:   "v2",
		CreatedByType: "human_user",
		CreatedByID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("first Deprecate: %v", err)
	}
	if first.Version != 2 {
		t.Fatalf("first deprecate version = %d, want 2", first.Version)
	}

	second, err := repo.Deprecate(ctx, current.ID, FlowTemplate{
		DisplayName:   "Default Flow v2",
		Description:   "v2",
		CreatedByType: "human_user",
		CreatedByID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("second Deprecate: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second Deprecate id = %s, want %s", second.ID, first.ID)
	}

	matchingRows := repo.templatesByScopeAndSlug(current.OrganizationID, current.ProjectID, current.Slug)
	if len(matchingRows) != 2 {
		t.Fatalf("flow_template row count = %d, want 2", len(matchingRows))
	}

	currentRows := 0
	for _, row := range matchingRows {
		if row.IsCurrent {
			currentRows++
		}
	}
	if currentRows != 1 {
		t.Fatalf("is_current row count = %d, want 1", currentRows)
	}
}

func TestTaskScheduleRepoListDueFiltersCorrectly(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-2 * time.Minute)
	future := now.Add(2 * time.Minute)

	dueEnabledID := uuid.New()
	repo := &inMemoryTaskScheduleRepo{
		schedules: []TaskSchedule{
			{
				ID:         dueEnabledID,
				IsEnabled:  true,
				NextFireAt: &past,
			},
			{
				ID:         uuid.New(),
				IsEnabled:  true,
				NextFireAt: &future,
			},
			{
				ID:         uuid.New(),
				IsEnabled:  false,
				NextFireAt: &past,
			},
			{
				ID:        uuid.New(),
				IsEnabled: true,
			},
		},
	}

	due, err := repo.ListDue(ctx, now, 50)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("ListDue len = %d, want 1", len(due))
	}
	if due[0].ID != dueEnabledID {
		t.Fatalf("ListDue[0].ID = %s, want %s", due[0].ID, dueEnabledID)
	}
}

type inMemoryFlowTemplateRepo struct {
	templates []FlowTemplate
}

func (r *inMemoryFlowTemplateRepo) Deprecate(_ context.Context, currentID uuid.UUID, next FlowTemplate) (FlowTemplate, error) {
	index := -1
	for i := range r.templates {
		if r.templates[i].ID == currentID {
			index = i
			break
		}
	}
	if index == -1 {
		return FlowTemplate{}, ErrNotFound
	}

	current := r.templates[index]
	if !current.IsCurrent {
		latest, ok := r.latestCurrentByScopeAndSlug(current.OrganizationID, current.ProjectID, current.Slug)
		if ok {
			return latest, nil
		}
		return current, nil
	}

	r.templates[index].IsCurrent = false
	created := mergeFlowTemplateForDeprecate(current, next)
	created.ID = uuid.New()
	now := time.Now().UTC()
	created.CreatedAt = now
	created.UpdatedAt = now
	r.templates = append(r.templates, created)
	return created, nil
}

func (r *inMemoryFlowTemplateRepo) latestCurrentByScopeAndSlug(organizationID, projectID *uuid.UUID, slug string) (FlowTemplate, bool) {
	var (
		found  bool
		latest FlowTemplate
	)
	for _, candidate := range r.templates {
		if !candidate.IsCurrent {
			continue
		}
		if candidate.Slug != slug {
			continue
		}
		if !sameOptionalUUID(candidate.OrganizationID, organizationID) {
			continue
		}
		if !sameOptionalUUID(candidate.ProjectID, projectID) {
			continue
		}
		if !found || candidate.Version > latest.Version {
			latest = candidate
			found = true
		}
	}
	return latest, found
}

func (r *inMemoryFlowTemplateRepo) templatesByScopeAndSlug(organizationID, projectID *uuid.UUID, slug string) []FlowTemplate {
	rows := make([]FlowTemplate, 0)
	for _, candidate := range r.templates {
		if candidate.Slug != slug {
			continue
		}
		if !sameOptionalUUID(candidate.OrganizationID, organizationID) {
			continue
		}
		if !sameOptionalUUID(candidate.ProjectID, projectID) {
			continue
		}
		rows = append(rows, candidate)
	}
	return rows
}

type inMemoryTaskScheduleRepo struct {
	schedules []TaskSchedule
}

func (r *inMemoryTaskScheduleRepo) ListDue(_ context.Context, now time.Time, limit int) ([]TaskSchedule, error) {
	if limit <= 0 {
		limit = 100
	}

	due := make([]TaskSchedule, 0)
	for _, schedule := range r.schedules {
		if !schedule.IsEnabled || schedule.NextFireAt == nil {
			continue
		}
		if schedule.NextFireAt.After(now) {
			continue
		}
		due = append(due, schedule)
	}

	sort.Slice(due, func(i, j int) bool {
		left := due[i].NextFireAt.UTC()
		right := due[j].NextFireAt.UTC()
		if left.Equal(right) {
			return due[i].ID.String() < due[j].ID.String()
		}
		return left.Before(right)
	})
	if len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return *left == *right
	}
}
