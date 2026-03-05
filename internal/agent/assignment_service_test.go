package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type fakeTxStarter struct {
	tx       *fakeTx
	beginErr error
}

func (f *fakeTxStarter) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	if f.tx == nil {
		f.tx = &fakeTx{}
	}
	return f.tx, nil
}

type fakeTx struct {
	commitCount   int
	rollbackCount int
}

func (f *fakeTx) Begin(context.Context) (pgx.Tx, error) { return f, nil }
func (f *fakeTx) Commit(context.Context) error {
	f.commitCount++
	return nil
}
func (f *fakeTx) Rollback(context.Context) error {
	f.rollbackCount++
	return nil
}
func (f *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (f *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (f *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }
func (f *fakeTx) Conn() *pgx.Conn                                         { return nil }

type fakeProjectAssignments struct {
	currentPM repo.AgentProjectAssignment
	getPMErr  error
	assignErr error

	assignResult     repo.AgentProjectAssignment
	deactivateResult repo.AgentProjectAssignment

	callOrder []string
	callTxs   []pgx.Tx
}

func (f *fakeProjectAssignments) AssignTx(_ context.Context, tx pgx.Tx, assignment repo.AgentProjectAssignment) (repo.AgentProjectAssignment, error) {
	f.callOrder = append(f.callOrder, "assign")
	f.callTxs = append(f.callTxs, tx)
	if f.assignErr != nil {
		return repo.AgentProjectAssignment{}, f.assignErr
	}
	if f.assignResult.ID == uuid.Nil {
		f.assignResult = assignment
		f.assignResult.ID = uuid.New()
		f.assignResult.IsActive = true
	}
	return f.assignResult, nil
}

func (f *fakeProjectAssignments) DeactivateTx(_ context.Context, tx pgx.Tx, agentID, projectID uuid.UUID) (repo.AgentProjectAssignment, error) {
	f.callOrder = append(f.callOrder, "deactivate")
	f.callTxs = append(f.callTxs, tx)
	if f.deactivateResult.ID == uuid.Nil {
		f.deactivateResult = repo.AgentProjectAssignment{
			ID:        uuid.New(),
			AgentID:   agentID,
			ProjectID: projectID,
			Role:      "project_manager",
			IsActive:  false,
		}
	}
	return f.deactivateResult, nil
}

func (f *fakeProjectAssignments) GetByAgentAndProjectTx(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) (repo.AgentProjectAssignment, error) {
	return repo.AgentProjectAssignment{}, repo.ErrNotFound
}

func (f *fakeProjectAssignments) GetPMTx(_ context.Context, tx pgx.Tx, _ uuid.UUID) (repo.AgentProjectAssignment, error) {
	f.callOrder = append(f.callOrder, "get_pm")
	f.callTxs = append(f.callTxs, tx)
	if f.getPMErr != nil {
		return repo.AgentProjectAssignment{}, f.getPMErr
	}
	return f.currentPM, nil
}

type fakeAssignmentAgents struct {
	getByIDFn func(context.Context, uuid.UUID) (repo.Agent, error)
}

func (f *fakeAssignmentAgents) GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return repo.Agent{ID: id, AgentClass: "staff"}, nil
}

type fakeSkillAttachments struct {
	records   map[string]repo.AgentSkillAttachment
	setCalls  map[uuid.UUID]int
	callTxs   []pgx.Tx
	attachErr error
	detachErr error
	setErr    error
}

func newFakeSkillAttachments() *fakeSkillAttachments {
	return &fakeSkillAttachments{
		records:  make(map[string]repo.AgentSkillAttachment),
		setCalls: make(map[uuid.UUID]int),
	}
}

func skillKey(agentID, skillID uuid.UUID) string {
	return fmt.Sprintf("%s:%s", agentID.String(), skillID.String())
}

func (f *fakeSkillAttachments) Attach(_ context.Context, attachment repo.AgentSkillAttachment) (repo.AgentSkillAttachment, error) {
	if f.attachErr != nil {
		return repo.AgentSkillAttachment{}, f.attachErr
	}

	key := skillKey(attachment.AgentID, attachment.SkillID)
	existing, ok := f.records[key]
	if !ok {
		existing = attachment
		existing.ID = uuid.New()
	}
	existing.Priority = attachment.Priority
	existing.AttachedByType = attachment.AttachedByType
	existing.AttachedByID = attachment.AttachedByID
	existing.IsActive = true
	existing.DeactivatedAt = nil
	f.records[key] = existing
	return existing, nil
}

func (f *fakeSkillAttachments) Detach(_ context.Context, agentID, skillID uuid.UUID) (repo.AgentSkillAttachment, error) {
	if f.detachErr != nil {
		return repo.AgentSkillAttachment{}, f.detachErr
	}
	key := skillKey(agentID, skillID)
	existing, ok := f.records[key]
	if !ok || !existing.IsActive {
		return repo.AgentSkillAttachment{}, repo.ErrNotFound
	}
	existing.IsActive = false
	f.records[key] = existing
	return existing, nil
}

func (f *fakeSkillAttachments) SetPriorityTx(_ context.Context, tx pgx.Tx, agentID, skillID uuid.UUID, priority int) (repo.AgentSkillAttachment, error) {
	f.callTxs = append(f.callTxs, tx)
	if f.setErr != nil {
		return repo.AgentSkillAttachment{}, f.setErr
	}
	key := skillKey(agentID, skillID)
	existing, ok := f.records[key]
	if !ok {
		return repo.AgentSkillAttachment{}, repo.ErrNotFound
	}
	existing.Priority = priority
	f.records[key] = existing
	f.setCalls[skillID] = priority
	return existing, nil
}

func TestAssignToProjectPMSwapUsesSingleTransaction(t *testing.T) {
	t.Parallel()

	tx := &fakeTx{}
	txStarter := &fakeTxStarter{tx: tx}
	projectID := uuid.New()
	previousPM := uuid.New()
	newPM := uuid.New()

	assignments := &fakeProjectAssignments{
		currentPM: repo.AgentProjectAssignment{
			ID:        uuid.New(),
			AgentID:   previousPM,
			ProjectID: projectID,
			Role:      "project_manager",
			IsActive:  true,
		},
		assignResult: repo.AgentProjectAssignment{
			ID:        uuid.New(),
			AgentID:   newPM,
			ProjectID: projectID,
			Role:      "project_manager",
			IsActive:  true,
		},
	}

	svc := &assignmentService{
		txStarter:          txStarter,
		projectAssignments: assignments,
		agents: &fakeAssignmentAgents{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
				return repo.Agent{ID: id, AgentClass: "staff", IsStarterTrio: false}, nil
			},
		},
	}

	result, err := svc.AssignToProject(context.Background(), newPM, projectID, "pm", AssignmentActor{Type: "system"})
	if err != nil {
		t.Fatalf("AssignToProject returned error: %v", err)
	}
	if result.AgentID != newPM {
		t.Fatalf("assigned agent_id = %s, want %s", result.AgentID, newPM)
	}

	if len(assignments.callOrder) != 3 {
		t.Fatalf("call order len = %d, want 3", len(assignments.callOrder))
	}
	wantOrder := []string{"get_pm", "deactivate", "assign"}
	for i := range wantOrder {
		if assignments.callOrder[i] != wantOrder[i] {
			t.Fatalf("call order[%d] = %q, want %q", i, assignments.callOrder[i], wantOrder[i])
		}
	}

	if len(assignments.callTxs) != 3 {
		t.Fatalf("call tx count = %d, want 3", len(assignments.callTxs))
	}
	for i, txCall := range assignments.callTxs {
		if txCall != tx {
			t.Fatalf("call tx[%d] mismatch: got %p want %p", i, txCall, tx)
		}
	}

	if tx.commitCount != 1 {
		t.Fatalf("commit count = %d, want 1", tx.commitCount)
	}
}

func TestAttachSkillReattachDetachedReusesExistingRecord(t *testing.T) {
	t.Parallel()

	agentID := uuid.New()
	skillID := uuid.New()
	skills := newFakeSkillAttachments()

	svc := &assignmentService{
		skillAttachments: skills,
	}

	first, err := svc.AttachSkill(context.Background(), agentID, skillID, 120, AssignmentActor{Type: "system"})
	if err != nil {
		t.Fatalf("AttachSkill first call: %v", err)
	}

	if _, err := svc.DetachSkill(context.Background(), agentID, skillID); err != nil {
		t.Fatalf("DetachSkill: %v", err)
	}

	second, err := svc.AttachSkill(context.Background(), agentID, skillID, 40, AssignmentActor{Type: "system"})
	if err != nil {
		t.Fatalf("AttachSkill second call: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("reattach id = %s, want original %s", second.ID, first.ID)
	}
	if !second.IsActive {
		t.Fatalf("reattached record is_active = false, want true")
	}
	if second.Priority != 40 {
		t.Fatalf("reattached priority = %d, want 40", second.Priority)
	}
}

func TestReorderSkillsPartialMapUpdatesOnlyProvidedSkills(t *testing.T) {
	t.Parallel()

	tx := &fakeTx{}
	txStarter := &fakeTxStarter{tx: tx}
	agentID := uuid.New()
	skillA := uuid.New()
	skillB := uuid.New()
	skillC := uuid.New()
	skills := newFakeSkillAttachments()
	skills.records[skillKey(agentID, skillA)] = repo.AgentSkillAttachment{ID: uuid.New(), AgentID: agentID, SkillID: skillA, Priority: 200, IsActive: true}
	skills.records[skillKey(agentID, skillB)] = repo.AgentSkillAttachment{ID: uuid.New(), AgentID: agentID, SkillID: skillB, Priority: 50, IsActive: true}
	skills.records[skillKey(agentID, skillC)] = repo.AgentSkillAttachment{ID: uuid.New(), AgentID: agentID, SkillID: skillC, Priority: 100, IsActive: true}

	svc := &assignmentService{
		txStarter:        txStarter,
		skillAttachments: skills,
	}

	err := svc.ReorderSkills(context.Background(), agentID, map[uuid.UUID]int{
		skillA: 10,
		skillC: 90,
	})
	if err != nil {
		t.Fatalf("ReorderSkills returned error: %v", err)
	}

	if got := skills.records[skillKey(agentID, skillA)].Priority; got != 10 {
		t.Fatalf("skillA priority = %d, want 10", got)
	}
	if got := skills.records[skillKey(agentID, skillC)].Priority; got != 90 {
		t.Fatalf("skillC priority = %d, want 90", got)
	}
	if got := skills.records[skillKey(agentID, skillB)].Priority; got != 50 {
		t.Fatalf("skillB priority = %d, want unchanged 50", got)
	}

	if len(skills.setCalls) != 2 {
		t.Fatalf("set priority call count = %d, want 2", len(skills.setCalls))
	}

	for i, txCall := range skills.callTxs {
		if txCall != tx {
			t.Fatalf("set priority tx[%d] mismatch: got %p want %p", i, txCall, tx)
		}
	}
	if tx.commitCount != 1 {
		t.Fatalf("commit count = %d, want 1", tx.commitCount)
	}
}

func TestAssignToProjectReturnsPMConflict(t *testing.T) {
	t.Parallel()

	txStarter := &fakeTxStarter{tx: &fakeTx{}}
	assignments := &fakeProjectAssignments{
		getPMErr:  repo.ErrNotFound,
		assignErr: repo.ErrPMConflict,
	}
	svc := &assignmentService{
		txStarter:          txStarter,
		projectAssignments: assignments,
		agents: &fakeAssignmentAgents{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
				return repo.Agent{ID: id, AgentClass: "staff", IsStarterTrio: false}, nil
			},
		},
	}

	_, err := svc.AssignToProject(context.Background(), uuid.New(), uuid.New(), "pm", AssignmentActor{Type: "system"})
	if !errors.Is(err, ErrPMConflict) {
		t.Fatalf("AssignToProject error = %v, want ErrPMConflict", err)
	}
}

func TestAssignToProjectRejectsStarterTrioForPMWorkerReviewerRoles(t *testing.T) {
	t.Parallel()

	for _, role := range []string{"pm", "worker", "reviewer"} {
		role := role
		t.Run(role, func(t *testing.T) {
			t.Parallel()

			tx := &fakeTx{}
			txStarter := &fakeTxStarter{tx: tx}
			assignments := &fakeProjectAssignments{}
			svc := &assignmentService{
				txStarter:          txStarter,
				projectAssignments: assignments,
				agents: &fakeAssignmentAgents{
					getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
						return repo.Agent{ID: id, AgentClass: "staff", IsStarterTrio: true}, nil
					},
				},
			}

			_, err := svc.AssignToProject(context.Background(), uuid.New(), uuid.New(), role, AssignmentActor{Type: "system"})
			if !errors.Is(err, ErrAssignmentStarterTrioRole) {
				t.Fatalf("AssignToProject error = %v, want ErrAssignmentStarterTrioRole", err)
			}
			if len(assignments.callOrder) != 0 {
				t.Fatalf("assignment calls = %d, want 0", len(assignments.callOrder))
			}
			if tx.commitCount != 0 || tx.rollbackCount != 0 {
				t.Fatalf("tx counts commit=%d rollback=%d, want 0/0", tx.commitCount, tx.rollbackCount)
			}
		})
	}
}

func TestAssignToProjectRejectsTempAgentForProjectManagerRole(t *testing.T) {
	t.Parallel()

	tx := &fakeTx{}
	txStarter := &fakeTxStarter{tx: tx}
	assignments := &fakeProjectAssignments{}
	svc := &assignmentService{
		txStarter:          txStarter,
		projectAssignments: assignments,
		agents: &fakeAssignmentAgents{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
				return repo.Agent{ID: id, AgentClass: "temp", IsStarterTrio: false}, nil
			},
		},
	}

	_, err := svc.AssignToProject(context.Background(), uuid.New(), uuid.New(), "project_manager", AssignmentActor{Type: "system"})
	if !errors.Is(err, ErrAssignmentPMRequiresStaff) {
		t.Fatalf("AssignToProject error = %v, want ErrAssignmentPMRequiresStaff", err)
	}
	if len(assignments.callOrder) != 0 {
		t.Fatalf("assignment calls = %d, want 0", len(assignments.callOrder))
	}
	if tx.commitCount != 0 || tx.rollbackCount != 0 {
		t.Fatalf("tx counts commit=%d rollback=%d, want 0/0", tx.commitCount, tx.rollbackCount)
	}
}

func TestAssignToProjectAllowsStarterTrioObserverRole(t *testing.T) {
	t.Parallel()

	tx := &fakeTx{}
	txStarter := &fakeTxStarter{tx: tx}
	starterTrioID := uuid.New()
	projectID := uuid.New()
	assignments := &fakeProjectAssignments{
		assignResult: repo.AgentProjectAssignment{
			ID:        uuid.New(),
			AgentID:   starterTrioID,
			ProjectID: projectID,
			Role:      "observer",
			IsActive:  true,
		},
	}
	svc := &assignmentService{
		txStarter:          txStarter,
		projectAssignments: assignments,
		agents: &fakeAssignmentAgents{
			getByIDFn: func(_ context.Context, id uuid.UUID) (repo.Agent, error) {
				return repo.Agent{ID: id, AgentClass: "staff", IsStarterTrio: true}, nil
			},
		},
	}

	result, err := svc.AssignToProject(context.Background(), starterTrioID, projectID, "observer", AssignmentActor{Type: "system"})
	if err != nil {
		t.Fatalf("AssignToProject observer returned error: %v", err)
	}
	if result.Role != "observer" {
		t.Fatalf("assigned role = %q, want observer", result.Role)
	}
	if len(assignments.callOrder) != 1 || assignments.callOrder[0] != "assign" {
		t.Fatalf("assignment calls = %v, want [assign]", assignments.callOrder)
	}
	if tx.commitCount != 1 {
		t.Fatalf("commit count = %d, want 1", tx.commitCount)
	}
}
