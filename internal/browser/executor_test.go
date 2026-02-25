package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

type fakeSessionRepo struct {
	mu         sync.Mutex
	byID       map[uuid.UUID]BrowserSession
	activeTask map[uuid.UUID]uuid.UUID
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{byID: map[uuid.UUID]BrowserSession{}, activeTask: map[uuid.UUID]uuid.UUID{}}
}

func (r *fakeSessionRepo) Create(_ context.Context, session BrowserSession) (BrowserSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existingID, ok := r.activeTask[session.TaskID]; ok {
		existing := r.byID[existingID]
		if existing.Status == "active" {
			return BrowserSession{}, ErrConflict
		}
	}
	session.ID = uuid.New()
	if session.Status == "" {
		session.Status = "active"
	}
	now := time.Now().UTC()
	session.CreatedAt = now
	session.UpdatedAt = now
	r.byID[session.ID] = session
	if session.Status == "active" {
		r.activeTask[session.TaskID] = session.ID
	}
	return session, nil
}

func (r *fakeSessionRepo) Get(_ context.Context, id uuid.UUID) (BrowserSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return BrowserSession{}, ErrNotFound
	}
	return item, nil
}

func (r *fakeSessionRepo) GetActiveByTask(_ context.Context, taskID uuid.UUID) (BrowserSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.activeTask[taskID]
	if !ok {
		return BrowserSession{}, ErrNotFound
	}
	item := r.byID[id]
	if item.Status != "active" {
		return BrowserSession{}, ErrNotFound
	}
	return item, nil
}

func (r *fakeSessionRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string, revokedAt *time.Time) (BrowserSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return BrowserSession{}, ErrNotFound
	}
	item.Status = status
	if status == "revoked" {
		if revokedAt == nil {
			now := time.Now().UTC()
			item.RevokedAt = &now
		} else {
			t := revokedAt.UTC()
			item.RevokedAt = &t
		}
	}
	item.UpdatedAt = time.Now().UTC()
	r.byID[id] = item
	if status == "active" {
		r.activeTask[item.TaskID] = id
	} else if existingID, ok := r.activeTask[item.TaskID]; ok && existingID == id {
		delete(r.activeTask, item.TaskID)
	}
	return item, nil
}

func (r *fakeSessionRepo) UpdateCurrentURL(_ context.Context, id uuid.UUID, currentURL *string) (BrowserSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return BrowserSession{}, ErrNotFound
	}
	item.CurrentURL = currentURL
	item.UpdatedAt = time.Now().UTC()
	r.byID[id] = item
	return item, nil
}

func (r *fakeSessionRepo) UpdateIdleSince(_ context.Context, id uuid.UUID, idleSince *time.Time) (BrowserSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return BrowserSession{}, ErrNotFound
	}
	item.IdleSince = idleSince
	item.UpdatedAt = time.Now().UTC()
	r.byID[id] = item
	return item, nil
}

type fakeActionRepo struct {
	mu      sync.Mutex
	byID    map[uuid.UUID]BrowserAction
	created []BrowserAction
}

func newFakeActionRepo() *fakeActionRepo {
	return &fakeActionRepo{byID: map[uuid.UUID]BrowserAction{}}
}

func (r *fakeActionRepo) Create(_ context.Context, action BrowserAction) (BrowserAction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	action.ID = uuid.New()
	if action.Status == "" {
		action.Status = StatusPending
	}
	action.CreatedAt = time.Now().UTC()
	r.byID[action.ID] = action
	r.created = append(r.created, action)
	return action, nil
}

func (r *fakeActionRepo) UpdateCompletion(_ context.Context, id uuid.UUID, update ActionCompletion) (BrowserAction, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return BrowserAction{}, ErrNotFound
	}
	if update.Status != "" {
		item.Status = update.Status
	}
	if len(update.Output) > 0 {
		item.Output = update.Output
	}
	item.ErrorMessage = update.ErrorMessage
	item.ScreenshotArtifactID = update.ScreenshotArtifactID
	item.URLAtExecution = update.URLAtExecution
	item.StartedAt = update.StartedAt
	item.CompletedAt = update.CompletedAt
	item.DurationMS = update.DurationMS
	r.byID[id] = item
	return item, nil
}

type fakeHandoffRepo struct {
	mu      sync.Mutex
	byID    map[uuid.UUID]BrowserHandoff
	byInbox map[uuid.UUID]uuid.UUID
}

func newFakeHandoffRepo() *fakeHandoffRepo {
	return &fakeHandoffRepo{byID: map[uuid.UUID]BrowserHandoff{}, byInbox: map[uuid.UUID]uuid.UUID{}}
}

func (r *fakeHandoffRepo) Create(_ context.Context, handoff BrowserHandoff) (BrowserHandoff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if handoff.ID == uuid.Nil {
		handoff.ID = uuid.New()
	}
	if handoff.Status == "" {
		handoff.Status = "pending"
	}
	if handoff.ExpiresAt.IsZero() {
		handoff.ExpiresAt = time.Now().UTC().Add(24 * time.Hour)
	}
	handoff.CreatedAt = time.Now().UTC()
	r.byID[handoff.ID] = handoff
	r.byInbox[handoff.InboxItemID] = handoff.ID
	return handoff, nil
}

func (r *fakeHandoffRepo) Get(_ context.Context, id uuid.UUID) (BrowserHandoff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return BrowserHandoff{}, ErrNotFound
	}
	return item, nil
}

func (r *fakeHandoffRepo) GetByInboxItem(_ context.Context, inboxItemID uuid.UUID) (BrowserHandoff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byInbox[inboxItemID]
	if !ok {
		return BrowserHandoff{}, ErrNotFound
	}
	return r.byID[id], nil
}

func (r *fakeHandoffRepo) UpdateStatus(_ context.Context, id uuid.UUID, update HandoffStatusUpdate) (BrowserHandoff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return BrowserHandoff{}, ErrNotFound
	}
	item.Status = update.Status
	item.PostHandoffScreenshotArtifactID = update.PostHandoffScreenshotArtifactID
	item.CompletedAt = update.CompletedAt
	r.byID[id] = item
	return item, nil
}

type fakeProjectRepo struct {
	project repo.Project
}

func (r fakeProjectRepo) GetByID(context.Context, uuid.UUID) (repo.Project, error) {
	return r.project, nil
}

type fakeRunService struct {
	pauseCalls  []uuid.UUID
	resumeCalls []uuid.UUID
	failCalls   []uuid.UUID
}

func (s *fakeRunService) PauseRun(_ context.Context, runID uuid.UUID) error {
	s.pauseCalls = append(s.pauseCalls, runID)
	return nil
}

func (s *fakeRunService) ResumeRun(_ context.Context, runID uuid.UUID) error {
	s.resumeCalls = append(s.resumeCalls, runID)
	return nil
}

func (s *fakeRunService) FailRun(_ context.Context, runID uuid.UUID, _ string, _ string) error {
	s.failCalls = append(s.failCalls, runID)
	return nil
}

type fakeInboxRepo struct {
	created []repo.InboxItem
	acted   []uuid.UUID
}

func (r *fakeInboxRepo) Create(_ context.Context, item repo.InboxItem) (repo.InboxItem, error) {
	item.ID = uuid.New()
	r.created = append(r.created, item)
	return item, nil
}

func (r *fakeInboxRepo) MarkActed(_ context.Context, id, _ uuid.UUID) (repo.InboxItem, error) {
	r.acted = append(r.acted, id)
	return repo.InboxItem{ID: id, IsActed: true}, nil
}

type fakeArtifactRepo struct {
	created []controlplane.RunArtifact
}

func (r *fakeArtifactRepo) Create(_ context.Context, artifact controlplane.RunArtifact) (controlplane.RunArtifact, error) {
	artifact.ID = uuid.New()
	r.created = append(r.created, artifact)
	return artifact, nil
}

type fakeStore struct {
	keys []string
}

func (s *fakeStore) Put(_ context.Context, key string, r io.Reader, _ storage.PutOptions) error {
	_, _ = io.Copy(io.Discard, r)
	s.keys = append(s.keys, key)
	return nil
}

func (*fakeStore) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (*fakeStore) Delete(context.Context, string) error {
	return nil
}

func (*fakeStore) Exists(context.Context, string) (bool, error) {
	return true, nil
}

func (*fakeStore) List(context.Context, string) ([]storage.ObjectMeta, error) {
	return nil, nil
}

type fakePolicyChecker struct {
	allow bool
}

func (c fakePolicyChecker) IsAllowed(context.Context, uuid.UUID, string) bool {
	return c.allow
}

type fakeDriver struct {
	executeCalls    int
	screenshotCalls int
	lastAction      DriverAction
	executeErr      error
	output          map[string]any
}

func (d *fakeDriver) Execute(_ context.Context, action DriverAction) (map[string]any, error) {
	d.executeCalls++
	d.lastAction = action
	if d.executeErr != nil {
		return nil, d.executeErr
	}
	if d.output == nil {
		return map[string]any{"current_url": "https://example.com"}, nil
	}
	return d.output, nil
}

func (d *fakeDriver) TakeScreenshot(context.Context) ([]byte, error) {
	d.screenshotCalls++
	return []byte("png"), nil
}

func (*fakeDriver) Close() error { return nil }

func TestExecutorExecuteActionRevokedSession(t *testing.T) {
	sessions := newFakeSessionRepo()
	actions := newFakeActionRepo()
	handoffs := newFakeHandoffRepo()
	driver := &fakeDriver{}

	session := BrowserSession{
		ID:             uuid.New(),
		TaskID:         uuid.New(),
		ProjectID:      uuid.New(),
		AgentID:        uuid.New(),
		OrganizationID: uuid.New(),
		Status:         "revoked",
	}
	sessions.byID[session.ID] = session

	exec, err := NewExecutor(ExecutorOptions{
		Sessions:      sessions,
		Actions:       actions,
		Handoffs:      handoffs,
		Projects:      fakeProjectRepo{project: repo.Project{ID: session.ProjectID, OrganizationID: session.OrganizationID}},
		Runs:          &fakeRunService{},
		Inbox:         &fakeInboxRepo{},
		Artifacts:     &fakeArtifactRepo{},
		Store:         &fakeStore{},
		PolicyChecker: fakePolicyChecker{allow: true},
		DriverFactory: func(context.Context, BrowserSession) (BrowserDriver, error) { return driver, nil },
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	_, execErr := exec.ExecuteAction(context.Background(), session.ID, uuid.New(), uuid.New(), ExecuteActionInput{ActionType: "navigate", Input: map[string]any{"url": "https://example.com"}})
	if !errors.Is(execErr, ErrSessionRevoked) {
		t.Fatalf("ExecuteAction error = %v, want ErrSessionRevoked", execErr)
	}
	if driver.executeCalls != 0 {
		t.Fatalf("driver execute calls = %d, want 0", driver.executeCalls)
	}
}

func TestExecutorExecuteActionNavigateCapturesScreenshot(t *testing.T) {
	sessions := newFakeSessionRepo()
	actions := newFakeActionRepo()
	handoffs := newFakeHandoffRepo()
	runs := &fakeRunService{}
	inbox := &fakeInboxRepo{}
	artifacts := &fakeArtifactRepo{}
	store := &fakeStore{}
	driver := &fakeDriver{output: map[string]any{"current_url": "https://en.wikipedia.org/wiki/Go"}}

	taskID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()
	session, _ := sessions.Create(context.Background(), BrowserSession{
		TaskID:         taskID,
		ProjectID:      projectID,
		AgentID:        agentID,
		OrganizationID: orgID,
		Status:         "active",
	})

	exec, err := NewExecutor(ExecutorOptions{
		Sessions:      sessions,
		Actions:       actions,
		Handoffs:      handoffs,
		Projects:      fakeProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		Runs:          runs,
		Inbox:         inbox,
		Artifacts:     artifacts,
		Store:         store,
		PolicyChecker: fakePolicyChecker{allow: true},
		DriverFactory: func(context.Context, BrowserSession) (BrowserDriver, error) { return driver, nil },
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	_, err = exec.ExecuteAction(context.Background(), session.ID, uuid.New(), uuid.New(), ExecuteActionInput{
		ActionType: "navigate",
		Input:      map[string]any{"url": "https://en.wikipedia.org/wiki/Go"},
	})
	if err != nil {
		t.Fatalf("ExecuteAction: %v", err)
	}
	if driver.screenshotCalls != 1 {
		t.Fatalf("screenshot calls = %d, want 1", driver.screenshotCalls)
	}
	if len(artifacts.created) != 1 {
		t.Fatalf("artifact rows = %d, want 1", len(artifacts.created))
	}
	if artifacts.created[0].InlineContent != nil {
		t.Fatal("screenshot inline_content should be nil")
	}
}

func TestExecutorGetOrCreateSessionReusesExistingActiveSession(t *testing.T) {
	sessions := newFakeSessionRepo()
	actions := newFakeActionRepo()
	handoffs := newFakeHandoffRepo()
	driver := &fakeDriver{}

	taskID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()
	existing, _ := sessions.Create(context.Background(), BrowserSession{
		TaskID:         taskID,
		ProjectID:      projectID,
		AgentID:        agentID,
		OrganizationID: orgID,
		Status:         "active",
	})

	factoryCalls := 0
	exec, err := NewExecutor(ExecutorOptions{
		Sessions:      sessions,
		Actions:       actions,
		Handoffs:      handoffs,
		Projects:      fakeProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		Runs:          &fakeRunService{},
		Inbox:         &fakeInboxRepo{},
		Artifacts:     &fakeArtifactRepo{},
		Store:         &fakeStore{},
		PolicyChecker: fakePolicyChecker{allow: true},
		DriverFactory: func(context.Context, BrowserSession) (BrowserDriver, error) {
			factoryCalls++
			return driver, nil
		},
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	first, err := exec.GetOrCreateSession(context.Background(), taskID, agentID, projectID)
	if err != nil {
		t.Fatalf("first GetOrCreateSession: %v", err)
	}
	second, err := exec.GetOrCreateSession(context.Background(), taskID, agentID, projectID)
	if err != nil {
		t.Fatalf("second GetOrCreateSession: %v", err)
	}
	if first.ID != existing.ID || second.ID != existing.ID {
		t.Fatalf("expected existing session id %s, got first=%s second=%s", existing.ID, first.ID, second.ID)
	}
	if factoryCalls != 1 {
		t.Fatalf("driver factory calls = %d, want 1", factoryCalls)
	}
}

func TestExecutorRequestAndCompleteHandoffTransitionsRun(t *testing.T) {
	sessions := newFakeSessionRepo()
	actions := newFakeActionRepo()
	handoffs := newFakeHandoffRepo()
	runs := &fakeRunService{}
	inbox := &fakeInboxRepo{}
	driver := &fakeDriver{}

	taskID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()
	targetUserID := uuid.New()
	runID := uuid.New()

	session, _ := sessions.Create(context.Background(), BrowserSession{
		TaskID:         taskID,
		ProjectID:      projectID,
		AgentID:        agentID,
		OrganizationID: orgID,
		Status:         "active",
	})

	exec, err := NewExecutor(ExecutorOptions{
		Sessions:      sessions,
		Actions:       actions,
		Handoffs:      handoffs,
		Projects:      fakeProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		Runs:          runs,
		Inbox:         inbox,
		Artifacts:     &fakeArtifactRepo{},
		Store:         &fakeStore{},
		PolicyChecker: fakePolicyChecker{allow: true},
		DriverFactory: func(context.Context, BrowserSession) (BrowserDriver, error) { return driver, nil },
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	handoff, err := exec.RequestHandoff(context.Background(), session.ID, runID, agentID, targetUserID, "need human help")
	if err != nil {
		t.Fatalf("RequestHandoff: %v", err)
	}
	if len(runs.pauseCalls) != 1 || runs.pauseCalls[0] != runID {
		t.Fatalf("pause calls = %+v, want %s", runs.pauseCalls, runID)
	}
	if len(inbox.created) != 1 || inbox.created[0].ItemType != "browser_handoff" {
		t.Fatalf("inbox created = %+v, want browser_handoff", inbox.created)
	}

	if err := exec.CompleteHandoff(context.Background(), handoff.ID, "completed"); err != nil {
		t.Fatalf("CompleteHandoff completed: %v", err)
	}
	if len(runs.resumeCalls) != 1 || runs.resumeCalls[0] != runID {
		t.Fatalf("resume calls = %+v, want %s", runs.resumeCalls, runID)
	}

	stored, err := handoffs.Get(context.Background(), handoff.ID)
	if err != nil {
		t.Fatalf("handoff Get: %v", err)
	}
	if stored.Status != "completed" {
		t.Fatalf("handoff status = %q, want completed", stored.Status)
	}
}

func TestExecutorCompleteHandoffCancelledFailsRun(t *testing.T) {
	sessions := newFakeSessionRepo()
	actions := newFakeActionRepo()
	handoffs := newFakeHandoffRepo()
	runs := &fakeRunService{}
	inbox := &fakeInboxRepo{}
	driver := &fakeDriver{}

	taskID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()
	targetUserID := uuid.New()
	runID := uuid.New()

	session, _ := sessions.Create(context.Background(), BrowserSession{TaskID: taskID, ProjectID: projectID, AgentID: agentID, OrganizationID: orgID, Status: "active"})

	exec, err := NewExecutor(ExecutorOptions{
		Sessions:      sessions,
		Actions:       actions,
		Handoffs:      handoffs,
		Projects:      fakeProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		Runs:          runs,
		Inbox:         inbox,
		Artifacts:     &fakeArtifactRepo{},
		Store:         &fakeStore{},
		PolicyChecker: fakePolicyChecker{allow: true},
		DriverFactory: func(context.Context, BrowserSession) (BrowserDriver, error) { return driver, nil },
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	handoff, err := exec.RequestHandoff(context.Background(), session.ID, runID, agentID, targetUserID, "cancel path")
	if err != nil {
		t.Fatalf("RequestHandoff: %v", err)
	}
	if err := exec.CompleteHandoff(context.Background(), handoff.ID, "cancelled"); err != nil {
		t.Fatalf("CompleteHandoff cancelled: %v", err)
	}
	if len(runs.failCalls) != 1 || runs.failCalls[0] != runID {
		t.Fatalf("fail calls = %+v, want %s", runs.failCalls, runID)
	}
}

func TestExecutorIdleMonitorClosesStaleSession(t *testing.T) {
	sessions := newFakeSessionRepo()
	actions := newFakeActionRepo()
	handoffs := newFakeHandoffRepo()
	driver := &fakeDriver{}

	taskID := uuid.New()
	projectID := uuid.New()
	agentID := uuid.New()
	orgID := uuid.New()
	idle := time.Now().UTC().Add(-90 * time.Minute)
	existing, _ := sessions.Create(context.Background(), BrowserSession{
		TaskID:         taskID,
		ProjectID:      projectID,
		AgentID:        agentID,
		OrganizationID: orgID,
		Status:         "active",
		IdleSince:      &idle,
	})

	exec, err := NewExecutor(ExecutorOptions{
		Sessions:          sessions,
		Actions:           actions,
		Handoffs:          handoffs,
		Projects:          fakeProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		Runs:              &fakeRunService{},
		Inbox:             &fakeInboxRepo{},
		Artifacts:         &fakeArtifactRepo{},
		Store:             &fakeStore{},
		PolicyChecker:     fakePolicyChecker{allow: true},
		DriverFactory:     func(context.Context, BrowserSession) (BrowserDriver, error) { return driver, nil },
		IdleCheckInterval: 10 * time.Millisecond,
		IdleTimeout:       20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	defer exec.Shutdown()

	if _, err := exec.GetOrCreateSession(context.Background(), taskID, agentID, projectID); err != nil {
		t.Fatalf("GetOrCreateSession: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		current, getErr := sessions.Get(context.Background(), existing.ID)
		if getErr == nil && current.Status == "closed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := sessions.Get(context.Background(), existing.ID)
	encoded, _ := json.Marshal(current)
	t.Fatalf("session was not closed by idle monitor: %s", string(encoded))
}
