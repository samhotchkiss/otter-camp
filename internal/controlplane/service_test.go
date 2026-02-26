package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestCreateRunIdempotencyReturnsExistingRun(t *testing.T) {
	repos := newFakeRunDeps()
	svc := repos.newService(t)

	key := "same-key"
	first, err := svc.CreateRun(context.Background(), CreateRunInput{
		OrganizationID: uuid.New(),
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		IdempotencyKey: &key,
	})
	if err != nil {
		t.Fatalf("CreateRun first call: %v", err)
	}

	second, err := svc.CreateRun(context.Background(), CreateRunInput{
		OrganizationID: first.OrganizationID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		IdempotencyKey: &key,
	})
	if err != nil {
		t.Fatalf("CreateRun second call: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("deduped run id = %s, want %s", second.ID, first.ID)
	}
	if repos.runs.createCalls != 1 {
		t.Fatalf("run create calls = %d, want 1", repos.runs.createCalls)
	}
}

func TestCreateRunPolicyDeniedCreatesFailedRun(t *testing.T) {
	repos := newFakeRunDeps()
	repos.policy.decision = RunCreationPolicyDecision{Allowed: false, Reason: "deny"}
	svc := repos.newService(t)

	created, err := svc.CreateRun(context.Background(), CreateRunInput{
		OrganizationID: uuid.New(),
		PrincipalType:  "human_user",
		PrincipalID:    uuid.New(),
		TriggerType:    "api",
	})
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("CreateRun error = %v, want ErrPolicyDenied", err)
	}
	if created.Status != "failed" {
		t.Fatalf("status = %s, want failed", created.Status)
	}
	if created.FailureClass == nil || *created.FailureClass != "policy_denied" {
		t.Fatalf("failure_class = %v, want policy_denied", created.FailureClass)
	}
}

func TestCreateRunBudgetHardExceededCreatesFailedRun(t *testing.T) {
	repos := newFakeRunDeps()
	repos.budget.result = &budget.BudgetCheckResult{
		Allowed:      false,
		HardLimitHit: true,
	}
	svc := repos.newService(t)

	created, err := svc.CreateRun(context.Background(), CreateRunInput{
		OrganizationID: uuid.New(),
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
	})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("CreateRun error = %v, want ErrBudgetExceeded", err)
	}
	if created.Status != "failed" {
		t.Fatalf("status = %s, want failed", created.Status)
	}
	if created.FailureClass == nil || *created.FailureClass != "budget_exceeded" {
		t.Fatalf("failure_class = %v, want budget_exceeded", created.FailureClass)
	}
}

func TestCreateRunBudgetSoftExceededAppendsRunEvent(t *testing.T) {
	repos := newFakeRunDeps()
	repos.budget.result = &budget.BudgetCheckResult{
		Allowed:      true,
		SoftLimitHit: true,
	}
	svc := repos.newService(t)

	created, err := svc.CreateRun(context.Background(), CreateRunInput{
		OrganizationID: uuid.New(),
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if created.Status != "created" {
		t.Fatalf("status = %s, want created", created.Status)
	}

	found := false
	for _, event := range repos.events.appended {
		if event.RunID == created.ID && event.EventType == "budget_exceeded" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected run_event budget_exceeded")
	}
}

func TestCreateRunRoutesFlowNodeRunToSession(t *testing.T) {
	repos := newFakeRunDeps()
	bridge := &fakeRunSessionBridge{}
	repos.sessionBridge = bridge
	svc := repos.newService(t)

	taskID := uuid.New()
	flowNodeID := uuid.New()
	created, err := svc.CreateRun(context.Background(), CreateRunInput{
		OrganizationID: uuid.New(),
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "agent_tool",
		TaskID:         &taskID,
		FlowNodeID:     &flowNodeID,
	})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if bridge.calls != 1 {
		t.Fatalf("RouteRunToSession calls = %d, want 1", bridge.calls)
	}
	if len(bridge.runs) != 1 {
		t.Fatalf("routed runs = %d, want 1", len(bridge.runs))
	}
	routed := bridge.runs[0]
	if routed.ID != created.ID {
		t.Fatalf("routed id = %s, want %s", routed.ID, created.ID)
	}
	if routed.TaskID == nil || *routed.TaskID != taskID {
		t.Fatalf("routed task_id = %v, want %s", routed.TaskID, taskID)
	}
	if routed.FlowNodeID == nil || *routed.FlowNodeID != flowNodeID {
		t.Fatalf("routed flow_node_id = %v, want %s", routed.FlowNodeID, flowNodeID)
	}
	if routed.Summary != "agent_tool" {
		t.Fatalf("routed summary = %q, want %q", routed.Summary, "agent_tool")
	}
}

func TestRequestCancelCreatedRunCancelsImmediately(t *testing.T) {
	repos := newFakeRunDeps()
	svc := repos.newService(t)

	runRecord, err := repos.runs.Create(context.Background(), Run{
		OrganizationID: uuid.New(),
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Status:         "created",
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}

	if err := svc.RequestCancel(context.Background(), runRecord.ID, CancelRequestActor{Type: "system"}); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}

	updated, err := repos.runs.Get(context.Background(), runRecord.ID)
	if err != nil {
		t.Fatalf("Get updated run: %v", err)
	}
	if updated.Status != "cancelled" {
		t.Fatalf("status = %s, want cancelled", updated.Status)
	}
}

func TestCreateRetryAttemptInternalMaxOne(t *testing.T) {
	repos := newFakeRunDeps()
	svc := repos.newService(t)

	runRecord := repos.seedRun("in_progress")
	step := repos.seedStep(runRecord.ID, 1)

	if _, err := svc.CreateRetryAttempt(context.Background(), step.ID, "retry_transient"); err != nil {
		t.Fatalf("first CreateRetryAttempt: %v", err)
	}

	_, err := svc.CreateRetryAttempt(context.Background(), step.ID, "retry_transient")
	if !errors.Is(err, ErrMaxAttemptsExceeded) {
		t.Fatalf("second CreateRetryAttempt error = %v, want ErrMaxAttemptsExceeded", err)
	}

	updated, err := repos.runs.Get(context.Background(), runRecord.ID)
	if err != nil {
		t.Fatalf("Get updated run: %v", err)
	}
	if updated.Status != "dead_letter" {
		t.Fatalf("run status = %s, want dead_letter", updated.Status)
	}
}

func TestCreateRetryAttemptMCPMaxThree(t *testing.T) {
	repos := newFakeRunDeps()
	svc := repos.newService(t)

	runRecord := repos.seedRun("in_progress")
	step := repos.seedStep(runRecord.ID, 1)

	worker := "mcp"
	repos.attempts.seed(step.ID, RunAttempt{RunStepID: step.ID, AttemptNumber: 1, Trigger: "initial", Status: "failed", WorkerType: &worker})
	repos.attempts.seed(step.ID, RunAttempt{RunStepID: step.ID, AttemptNumber: 2, Trigger: "retry_transient", Status: "failed", WorkerType: &worker})
	repos.attempts.seed(step.ID, RunAttempt{RunStepID: step.ID, AttemptNumber: 3, Trigger: "retry_transient", Status: "failed", WorkerType: &worker})

	_, err := svc.CreateRetryAttempt(context.Background(), step.ID, "retry_transient")
	if !errors.Is(err, ErrMaxAttemptsExceeded) {
		t.Fatalf("CreateRetryAttempt attempt 4 error = %v, want ErrMaxAttemptsExceeded", err)
	}
}

func TestStartRunCompletedReturnsErrInvalidTransition(t *testing.T) {
	repos := newFakeRunDeps()
	svc := repos.newService(t)

	runRecord := repos.seedRun("completed")
	err := svc.StartRun(context.Background(), runRecord.ID)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("StartRun error = %v, want ErrInvalidTransition", err)
	}
}

func TestSupervisorDetectStuckRunsInitiatesRecovery(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	stuckRun := Run{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		TaskID:         testPointerUUID(uuid.New()),
		FlowNodeID:     testPointerUUID(uuid.New()),
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "in_progress",
		TriggerType:    "api",
		Metadata:       []byte(`{"run_mode":"sync"}`),
		UpdatedAt:      now.Add(-2 * time.Minute),
	}

	runs := &fakeSupervisorRuns{
		inProgress: []Run{stuckRun},
	}
	events := &fakeSupervisorEvents{
		latestHeartbeatErr: ErrNotFound,
	}
	runSvc := &fakeSupervisorRunService{}
	supervisor, err := NewSupervisor(SupervisorOptions{
		RunService:   runSvc,
		Runs:         runs,
		RunEvents:    events,
		Clock:        clock.NewFake(now),
		PollInterval: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}

	if err := supervisor.detectStuckRuns(context.Background()); err != nil {
		t.Fatalf("detectStuckRuns: %v", err)
	}
	if runSvc.createRunCalls == 0 {
		t.Fatal("expected supervisor recovery CreateRun call")
	}
	if len(events.appended) == 0 || events.appended[0].ActorType != "supervisor" {
		t.Fatal("expected supervisor run_event with actor_type=supervisor")
	}
}

func TestSupervisorSkipsRecoveryAfterThreeDeadLettersFilesBlocker(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	stuckRun := Run{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		ProjectID:      testPointerUUID(uuid.New()),
		TaskID:         testPointerUUID(uuid.New()),
		FlowNodeID:     testPointerUUID(uuid.New()),
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "in_progress",
		TriggerType:    "api",
		Metadata:       []byte(`{"run_mode":"sync"}`),
		UpdatedAt:      now.Add(-2 * time.Minute),
	}

	runs := &fakeSupervisorRuns{
		inProgress:      []Run{stuckRun},
		deadLetterCount: 3,
	}
	events := &fakeSupervisorEvents{latestHeartbeatErr: ErrNotFound}
	runSvc := &fakeSupervisorRunService{}
	notifier := &fakeSupervisorNotifier{}
	supervisor := &Supervisor{
		runService: runSvc,
		runs:       runs,
		events:     events,
		notifier:   notifier,
		clock:      clock.NewFake(now),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := supervisor.detectStuckRuns(context.Background()); err != nil {
		t.Fatalf("detectStuckRuns: %v", err)
	}
	if runSvc.createRunCalls != 0 {
		t.Fatalf("supervisor recovery createRun calls = %d, want 0", runSvc.createRunCalls)
	}
	if notifier.blockerCalls == 0 {
		t.Fatal("expected blocker inbox creation when dead-letter threshold reached")
	}
}

type fakeRunDeps struct {
	runs          *fakeRunRepo
	steps         *fakeRunStepRepo
	attempts      *fakeRunAttemptRepo
	events        *fakeRunEventRepo
	bus           *fakeDomainBus
	policy        *fakeRunPolicyService
	budget        *fakeBudgetChecker
	sessionBridge runSessionRouter
}

func newFakeRunDeps() *fakeRunDeps {
	return &fakeRunDeps{
		runs: &fakeRunRepo{
			byID:          make(map[uuid.UUID]Run),
			idempotency:   make(map[string]uuid.UUID),
			statusHistory: make([]string, 0),
		},
		steps: &fakeRunStepRepo{
			byID:    make(map[uuid.UUID]RunStep),
			byRunID: make(map[uuid.UUID][]uuid.UUID),
		},
		attempts: &fakeRunAttemptRepo{
			byStep: make(map[uuid.UUID][]RunAttempt),
		},
		events: &fakeRunEventRepo{
			seqByRun: make(map[uuid.UUID]int),
		},
		bus:    &fakeDomainBus{},
		policy: &fakeRunPolicyService{decision: RunCreationPolicyDecision{Allowed: true}},
		budget: &fakeBudgetChecker{result: &budget.BudgetCheckResult{Allowed: true}},
	}
}

func (d *fakeRunDeps) newService(t *testing.T) RunService {
	t.Helper()
	svc, err := NewRunService(RunServiceOptions{
		Runs:          d.runs,
		RunSteps:      d.steps,
		Attempts:      d.attempts,
		RunEvent:      d.events,
		EventBus:      d.bus,
		Policy:        d.policy,
		Budget:        d.budget,
		SessionBridge: d.sessionBridge,
		TaskEvents:    &noopTaskEvents{},
		Inbox:         &noopInboxCreator{},
		Assignments:   &noopAssignments{},
		Agents:        &noopAgentReader{},
		Users:         &noopUserReader{},
		Clock:         clock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}
	return svc
}

func (d *fakeRunDeps) seedRun(status string) Run {
	runRecord, _ := d.runs.Create(context.Background(), Run{
		OrganizationID: uuid.New(),
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         status,
		TriggerType:    "api",
		Metadata:       []byte(`{}`),
	})
	return runRecord
}

func (d *fakeRunDeps) seedStep(runID uuid.UUID, stepNumber int) RunStep {
	step, _ := d.steps.Create(context.Background(), RunStep{
		RunID:      runID,
		StepNumber: stepNumber,
		Status:     "pending",
		Metadata:   []byte(`{}`),
	})
	return step
}

type fakeRunRepo struct {
	mu            sync.Mutex
	byID          map[uuid.UUID]Run
	idempotency   map[string]uuid.UUID
	createCalls   int
	statusHistory []string
}

func (r *fakeRunRepo) Create(_ context.Context, runRecord Run) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCalls++
	if runRecord.ID == uuid.Nil {
		runRecord.ID = uuid.New()
	}
	if runRecord.Version == 0 {
		runRecord.Version = 1
	}
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	if runRecord.CreatedAt.IsZero() {
		runRecord.CreatedAt = now
	}
	if runRecord.UpdatedAt.IsZero() {
		runRecord.UpdatedAt = now
	}
	runRecord.Status = strings.TrimSpace(runRecord.Status)
	if runRecord.Status == "" {
		runRecord.Status = "created"
	}
	if runRecord.IdempotencyKey != nil {
		key := strings.TrimSpace(*runRecord.IdempotencyKey)
		if key != "" {
			if existing, ok := r.idempotency[key]; ok && existing != runRecord.ID {
				return Run{}, ErrConflict
			}
			r.idempotency[key] = runRecord.ID
			runRecord.IdempotencyKey = &key
		}
	}
	r.byID[runRecord.ID] = runRecord
	r.statusHistory = append(r.statusHistory, runRecord.Status)
	return runRecord, nil
}

func (r *fakeRunRepo) Get(_ context.Context, id uuid.UUID) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	runRecord, ok := r.byID[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return runRecord, nil
}

func (r *fakeRunRepo) GetByIdempotencyKey(_ context.Context, organizationID uuid.UUID, key string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.idempotency[strings.TrimSpace(key)]
	if !ok {
		return Run{}, ErrNotFound
	}
	runRecord := r.byID[id]
	if runRecord.OrganizationID != organizationID {
		return Run{}, ErrNotFound
	}
	return runRecord, nil
}

func (r *fakeRunRepo) UpdateStatus(_ context.Context, id uuid.UUID, expectedVersion int, status string, failureReason, failureClass *string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	runRecord, ok := r.byID[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	if runRecord.Version != expectedVersion {
		return Run{}, ErrConflict
	}
	runRecord.Version++
	runRecord.Status = status
	runRecord.UpdatedAt = runRecord.UpdatedAt.Add(time.Second)
	runRecord.FailureReason = failureReason
	runRecord.FailureClass = failureClass
	if status == "in_progress" && runRecord.StartedAt == nil {
		now := runRecord.UpdatedAt
		runRecord.StartedAt = &now
	}
	if status == "completed" || status == "failed" || status == "timed_out" || status == "cancelled" || status == "dead_letter" {
		if runRecord.CompletedAt == nil {
			now := runRecord.UpdatedAt
			runRecord.CompletedAt = &now
		}
	}
	r.byID[id] = runRecord
	r.statusHistory = append(r.statusHistory, status)
	return runRecord, nil
}

func (r *fakeRunRepo) List(_ context.Context, filter RunListFilter) ([]Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Run
	for _, run := range r.byID {
		if filter.Status != "" && run.Status != filter.Status {
			continue
		}
		if filter.TaskID != nil && (run.TaskID == nil || *run.TaskID != *filter.TaskID) {
			continue
		}
		out = append(out, run)
	}
	return out, nil
}

type fakeRunStepRepo struct {
	mu      sync.Mutex
	byID    map[uuid.UUID]RunStep
	byRunID map[uuid.UUID][]uuid.UUID
}

func (r *fakeRunStepRepo) Create(_ context.Context, step RunStep) (RunStep, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if step.ID == uuid.Nil {
		step.ID = uuid.New()
	}
	if step.Status == "" {
		step.Status = "pending"
	}
	if step.CreatedAt.IsZero() {
		step.CreatedAt = time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	}
	for _, existingID := range r.byRunID[step.RunID] {
		if r.byID[existingID].StepNumber == step.StepNumber {
			return RunStep{}, ErrConflict
		}
	}
	r.byID[step.ID] = step
	r.byRunID[step.RunID] = append(r.byRunID[step.RunID], step.ID)
	return step, nil
}

func (r *fakeRunStepRepo) Get(_ context.Context, id uuid.UUID) (RunStep, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return RunStep{}, ErrNotFound
	}
	return item, nil
}

func (r *fakeRunStepRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string) (RunStep, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.byID[id]
	if !ok {
		return RunStep{}, ErrNotFound
	}
	item.Status = status
	r.byID[id] = item
	return item, nil
}

func (r *fakeRunStepRepo) ListByRun(_ context.Context, runID uuid.UUID) ([]RunStep, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]RunStep, 0, len(r.byRunID[runID]))
	for _, id := range r.byRunID[runID] {
		items = append(items, r.byID[id])
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].StepNumber < items[j].StepNumber
	})
	return items, nil
}

type fakeRunAttemptRepo struct {
	mu     sync.Mutex
	byStep map[uuid.UUID][]RunAttempt
}

func (r *fakeRunAttemptRepo) seed(stepID uuid.UUID, attempt RunAttempt) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if attempt.ID == uuid.Nil {
		attempt.ID = uuid.New()
	}
	r.byStep[stepID] = append(r.byStep[stepID], attempt)
}

func (r *fakeRunAttemptRepo) Create(_ context.Context, attempt RunAttempt) (RunAttempt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if attempt.ID == uuid.Nil {
		attempt.ID = uuid.New()
	}
	if attempt.Status == "" {
		attempt.Status = "pending"
	}
	r.byStep[attempt.RunStepID] = append(r.byStep[attempt.RunStepID], attempt)
	return attempt, nil
}

func (r *fakeRunAttemptRepo) GetLatestByStep(_ context.Context, runStepID uuid.UUID) (RunAttempt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := r.byStep[runStepID]
	if len(items) == 0 {
		return RunAttempt{}, ErrNotFound
	}
	latest := items[0]
	for _, item := range items[1:] {
		if item.AttemptNumber > latest.AttemptNumber {
			latest = item
		}
	}
	return latest, nil
}

type fakeRunEventRepo struct {
	mu       sync.Mutex
	seqByRun map[uuid.UUID]int
	appended []RunEvent
}

func (r *fakeRunEventRepo) Append(_ context.Context, event RunEvent) (RunEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seqByRun[event.RunID]++
	event.ID = uuid.New()
	event.Sequence = r.seqByRun[event.RunID]
	event.CreatedAt = time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC).Add(time.Duration(event.Sequence) * time.Second)
	if len(event.Payload) == 0 {
		event.Payload = []byte(`{}`)
	}
	r.appended = append(r.appended, event)
	return event, nil
}

func (r *fakeRunEventRepo) GetLatestHeartbeat(_ context.Context, runID uuid.UUID) (RunEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.appended) - 1; i >= 0; i-- {
		event := r.appended[i]
		if event.RunID == runID && event.EventType == "heartbeat" {
			return event, nil
		}
	}
	return RunEvent{}, ErrNotFound
}

type fakeDomainBus struct {
	mu     sync.Mutex
	events []eventbus.DomainEvent
}

func (b *fakeDomainBus) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
	return nil
}

type fakeRunPolicyService struct {
	decision RunCreationPolicyDecision
	err      error
}

func (s *fakeRunPolicyService) EvaluateRunCreation(context.Context, uuid.UUID, Principal) (RunCreationPolicyDecision, error) {
	if s.err != nil {
		return RunCreationPolicyDecision{}, s.err
	}
	return s.decision, nil
}

type fakeBudgetChecker struct {
	result *budget.BudgetCheckResult
	err    error
}

func (b *fakeBudgetChecker) CheckBudget(context.Context, uuid.UUID, *uuid.UUID, *uuid.UUID, int64) (*budget.BudgetCheckResult, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.result == nil {
		return &budget.BudgetCheckResult{Allowed: true}, nil
	}
	return b.result, nil
}

type fakeRunSessionBridge struct {
	calls int
	runs  []projectsvc.FlowRun
	err   error
}

func (f *fakeRunSessionBridge) RouteRunToSession(_ context.Context, run projectsvc.FlowRun) error {
	f.calls++
	f.runs = append(f.runs, run)
	return f.err
}

type noopTaskEvents struct{}

func (noopTaskEvents) Record(context.Context, repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error) {
	return repo.ProjectTaskEvent{}, nil
}

type noopInboxCreator struct{}

func (noopInboxCreator) Create(context.Context, repo.InboxItem) (repo.InboxItem, error) {
	return repo.InboxItem{}, nil
}

type noopAssignments struct{}

func (noopAssignments) GetPM(context.Context, uuid.UUID) (repo.AgentProjectAssignment, error) {
	return repo.AgentProjectAssignment{}, repo.ErrNotFound
}

type noopAgentReader struct{}

func (noopAgentReader) GetByID(context.Context, uuid.UUID) (repo.Agent, error) {
	return repo.Agent{}, repo.ErrNotFound
}

type noopUserReader struct{}

func (noopUserReader) GetByID(context.Context, uuid.UUID) (repo.HumanUser, error) {
	return repo.HumanUser{}, repo.ErrNotFound
}

func (noopUserReader) List(context.Context, uuid.UUID) ([]repo.HumanUser, error) {
	return nil, nil
}

type fakeSupervisorRuns struct {
	inProgress      []Run
	deadLetterCount int
}

func (f *fakeSupervisorRuns) ListInProgressUpdatedBefore(context.Context, time.Time) ([]Run, error) {
	return f.inProgress, nil
}

func (f *fakeSupervisorRuns) ListPausedUpdatedBefore(context.Context, time.Time) ([]Run, error) {
	return nil, nil
}

func (f *fakeSupervisorRuns) ListOrphanedInProgress(context.Context, time.Time) ([]Run, error) {
	return nil, nil
}

func (f *fakeSupervisorRuns) CountDeadLetterByTaskFlowNode(context.Context, uuid.UUID, uuid.UUID) (int, error) {
	return f.deadLetterCount, nil
}

type fakeSupervisorEvents struct {
	appended           []RunEvent
	latestHeartbeatErr error
}

func (f *fakeSupervisorEvents) Append(_ context.Context, event RunEvent) (RunEvent, error) {
	f.appended = append(f.appended, event)
	return event, nil
}

func (f *fakeSupervisorEvents) GetLatestHeartbeat(context.Context, uuid.UUID) (RunEvent, error) {
	if f.latestHeartbeatErr != nil {
		return RunEvent{}, f.latestHeartbeatErr
	}
	return RunEvent{}, ErrNotFound
}

type fakeSupervisorRunService struct {
	createRunCalls int
}

func (f *fakeSupervisorRunService) CreateRun(_ context.Context, input CreateRunInput) (Run, error) {
	f.createRunCalls++
	return Run{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		Status:         "created",
		TriggerType:    input.TriggerType,
	}, nil
}

func (*fakeSupervisorRunService) StartRun(context.Context, uuid.UUID) error { return nil }
func (*fakeSupervisorRunService) CompleteRun(context.Context, uuid.UUID, json.RawMessage) error {
	return nil
}
func (*fakeSupervisorRunService) FailRun(context.Context, uuid.UUID, string, string) error {
	return nil
}
func (*fakeSupervisorRunService) RequestCancel(context.Context, uuid.UUID, CancelRequestActor) error {
	return nil
}
func (*fakeSupervisorRunService) ConfirmCancelled(context.Context, uuid.UUID) error { return nil }
func (*fakeSupervisorRunService) PauseRun(context.Context, uuid.UUID) error         { return nil }
func (*fakeSupervisorRunService) ResumeRun(context.Context, uuid.UUID) error        { return nil }
func (*fakeSupervisorRunService) CreateRetryAttempt(context.Context, uuid.UUID, string) (RunAttempt, error) {
	return RunAttempt{}, nil
}
func (*fakeSupervisorRunService) DeadLetter(context.Context, uuid.UUID) error { return nil }
func (*fakeSupervisorRunService) EmitHeartbeat(context.Context, uuid.UUID, *uuid.UUID) error {
	return nil
}
func (*fakeSupervisorRunService) CreateStep(context.Context, uuid.UUID, string, string) (RunStep, error) {
	return RunStep{}, nil
}
func (*fakeSupervisorRunService) StartStep(context.Context, uuid.UUID) error { return nil }
func (*fakeSupervisorRunService) CompleteStep(context.Context, uuid.UUID) error {
	return nil
}
func (*fakeSupervisorRunService) FailStep(context.Context, uuid.UUID, string) error { return nil }
func (*fakeSupervisorRunService) GetRun(context.Context, uuid.UUID) (Run, error) {
	return Run{}, ErrNotFound
}
func (*fakeSupervisorRunService) ListRunsByTask(context.Context, uuid.UUID, uuid.UUID, string, string) ([]Run, error) {
	return nil, nil
}

type fakeSupervisorNotifier struct {
	blockerCalls    int
	escalationCalls int
}

func (*fakeSupervisorNotifier) recordRunDeadLetteredTaskEvent(context.Context, Run, string) error {
	return nil
}

func (f *fakeSupervisorNotifier) createEscalationInboxItem(context.Context, Run, string, bool) error {
	f.escalationCalls++
	return nil
}

func (f *fakeSupervisorNotifier) createBlockerInboxItem(context.Context, Run, string, string, string, bool) error {
	f.blockerCalls++
	return nil
}

func testPointerUUID(id uuid.UUID) *uuid.UUID {
	return &id
}
