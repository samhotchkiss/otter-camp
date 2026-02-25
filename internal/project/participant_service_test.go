package project

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

func TestTaskParticipantServiceAddParticipantEmitsEventAndIsIdempotent(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()
	participantID := uuid.New()

	participants := newFakeTaskParticipantRepo()
	events := &fakeTaskParticipantEventPublisher{}
	svc, err := NewTaskParticipantService(TaskParticipantServiceOptions{
		Participants: participants,
		Tasks: &fakeTaskParticipantTaskRepo{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
			},
		},
		Events: events,
	})
	if err != nil {
		t.Fatalf("NewTaskParticipantService: %v", err)
	}

	if err := svc.AddParticipant(context.Background(), taskID, "agent", participantID, "worker"); err != nil {
		t.Fatalf("AddParticipant first: %v", err)
	}
	if err := svc.AddParticipant(context.Background(), taskID, "agent", participantID, "worker"); err != nil {
		t.Fatalf("AddParticipant duplicate: %v", err)
	}

	if participants.addCalls != 1 {
		t.Fatalf("participant add calls = %d, want 1", participants.addCalls)
	}
	active, err := svc.ListParticipants(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("active participant count = %d, want 1", len(active))
	}
	if len(events.published) != 1 {
		t.Fatalf("event count = %d, want 1", len(events.published))
	}
	if events.published[0].EventType != "task.participant_added" {
		t.Fatalf("event_type = %q, want task.participant_added", events.published[0].EventType)
	}
	payload := decodeParticipantPayload(t, events.published[0].Payload)
	if payload["task_id"] != taskID.String() {
		t.Fatalf("payload.task_id = %q, want %q", payload["task_id"], taskID.String())
	}
	if payload["participant_type"] != "agent" {
		t.Fatalf("payload.participant_type = %q, want agent", payload["participant_type"])
	}
	if payload["participant_id"] != participantID.String() {
		t.Fatalf("payload.participant_id = %q, want %q", payload["participant_id"], participantID.String())
	}
	if payload["role"] != "worker" {
		t.Fatalf("payload.role = %q, want worker", payload["role"])
	}
}

func TestTaskParticipantServiceRemoveParticipantEmitsEvent(t *testing.T) {
	taskID := uuid.New()
	orgID := uuid.New()
	participantID := uuid.New()

	participants := newFakeTaskParticipantRepo()
	now := time.Now().UTC()
	participants.items[participantKey(taskID, "human_user", participantID)] = repo.ProjectTaskParticipant{
		ID:              uuid.New(),
		TaskID:          taskID,
		ParticipantType: "human_user",
		ParticipantID:   participantID,
		Role:            "reviewer",
		JoinedAt:        now,
	}

	events := &fakeTaskParticipantEventPublisher{}
	svc, err := NewTaskParticipantService(TaskParticipantServiceOptions{
		Participants: participants,
		Tasks: &fakeTaskParticipantTaskRepo{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
			},
		},
		Events: events,
	})
	if err != nil {
		t.Fatalf("NewTaskParticipantService: %v", err)
	}

	if err := svc.RemoveParticipant(context.Background(), taskID, "human_user", participantID); err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}

	if len(events.published) != 1 {
		t.Fatalf("event count = %d, want 1", len(events.published))
	}
	if events.published[0].EventType != "task.participant_removed" {
		t.Fatalf("event_type = %q, want task.participant_removed", events.published[0].EventType)
	}

	payload := decodeParticipantPayload(t, events.published[0].Payload)
	if payload["task_id"] != taskID.String() {
		t.Fatalf("payload.task_id = %q, want %q", payload["task_id"], taskID.String())
	}
	if payload["participant_type"] != "human_user" {
		t.Fatalf("payload.participant_type = %q, want human_user", payload["participant_type"])
	}
	if payload["participant_id"] != participantID.String() {
		t.Fatalf("payload.participant_id = %q, want %q", payload["participant_id"], participantID.String())
	}
	if payload["role"] != "reviewer" {
		t.Fatalf("payload.role = %q, want reviewer", payload["role"])
	}

	active, err := svc.ListParticipants(context.Background(), taskID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active participant count = %d, want 0", len(active))
	}
}

func TestTaskParticipantServiceAddParticipantConflictIsNoOp(t *testing.T) {
	taskID := uuid.New()
	participantID := uuid.New()
	participants := newFakeTaskParticipantRepo()
	participants.forceConflictOnAdd = true

	events := &fakeTaskParticipantEventPublisher{}
	svc, err := NewTaskParticipantService(TaskParticipantServiceOptions{
		Participants: participants,
		Tasks: &fakeTaskParticipantTaskRepo{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: uuid.New(),
			},
		},
		Events: events,
	})
	if err != nil {
		t.Fatalf("NewTaskParticipantService: %v", err)
	}

	if err := svc.AddParticipant(context.Background(), taskID, "agent", participantID, "worker"); err != nil {
		t.Fatalf("AddParticipant conflict should be no-op: %v", err)
	}
	if len(events.published) != 0 {
		t.Fatalf("event count = %d, want 0", len(events.published))
	}
}

type fakeTaskParticipantRepo struct {
	items map[string]repo.ProjectTaskParticipant

	addCalls           int
	forceConflictOnAdd bool
}

func newFakeTaskParticipantRepo() *fakeTaskParticipantRepo {
	return &fakeTaskParticipantRepo{
		items: make(map[string]repo.ProjectTaskParticipant),
	}
}

func (f *fakeTaskParticipantRepo) Add(_ context.Context, participant repo.ProjectTaskParticipant) (repo.ProjectTaskParticipant, error) {
	f.addCalls++
	key := participantKey(participant.TaskID, participant.ParticipantType, participant.ParticipantID)
	if f.forceConflictOnAdd {
		return repo.ProjectTaskParticipant{}, repo.ErrConflict
	}
	if existing, ok := f.items[key]; ok && existing.LeftAt == nil {
		return repo.ProjectTaskParticipant{}, repo.ErrConflict
	}

	now := time.Now().UTC()
	participant.ID = uuid.New()
	participant.JoinedAt = now
	participant.LeftAt = nil
	f.items[key] = participant
	return participant, nil
}

func (f *fakeTaskParticipantRepo) Remove(_ context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID) (repo.ProjectTaskParticipant, error) {
	key := participantKey(taskID, participantType, participantID)
	item, ok := f.items[key]
	if !ok || item.LeftAt != nil {
		return repo.ProjectTaskParticipant{}, repo.ErrNotFound
	}
	now := time.Now().UTC()
	item.LeftAt = &now
	f.items[key] = item
	return item, nil
}

func (f *fakeTaskParticipantRepo) ListActive(_ context.Context, taskID uuid.UUID) ([]repo.ProjectTaskParticipant, error) {
	result := make([]repo.ProjectTaskParticipant, 0)
	for _, item := range f.items {
		if item.TaskID == taskID && item.LeftAt == nil {
			result = append(result, item)
		}
	}
	return result, nil
}

func (f *fakeTaskParticipantRepo) GetByParticipant(_ context.Context, taskID uuid.UUID, participantType string, participantID uuid.UUID) (repo.ProjectTaskParticipant, error) {
	key := participantKey(taskID, participantType, participantID)
	item, ok := f.items[key]
	if !ok {
		return repo.ProjectTaskParticipant{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeTaskParticipantTaskRepo struct {
	task repo.ProjectTask
	err  error
}

func (f *fakeTaskParticipantTaskRepo) GetByID(context.Context, uuid.UUID) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	return f.task, nil
}

type fakeTaskParticipantEventPublisher struct {
	published []eventbus.DomainEvent
}

func (f *fakeTaskParticipantEventPublisher) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	f.published = append(f.published, event)
	return nil
}

func participantKey(taskID uuid.UUID, participantType string, participantID uuid.UUID) string {
	return taskID.String() + "|" + participantType + "|" + participantID.String()
}

func decodeParticipantPayload(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	payload := map[string]string{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return payload
}

var _ taskParticipantRepository = (*fakeTaskParticipantRepo)(nil)
var _ taskParticipantTaskRepository = (*fakeTaskParticipantTaskRepo)(nil)
var _ taskParticipantEventPublisher = (*fakeTaskParticipantEventPublisher)(nil)
