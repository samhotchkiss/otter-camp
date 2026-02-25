package cpfix

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type MakeRunOptions struct {
	ProjectID      *uuid.UUID
	TaskID         *uuid.UUID
	FlowNodeID     *uuid.UUID
	SessionID      *uuid.UUID
	TurnID         *uuid.UUID
	PrincipalType  string
	PrincipalID    uuid.UUID
	Status         string
	IdempotencyKey *string
	TriggerType    string
	Metadata       json.RawMessage
}

func MakeRun(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, opts MakeRunOptions) *controlplane.Run {
	t.Helper()

	principalType := strings.TrimSpace(opts.PrincipalType)
	if principalType == "" {
		principalType = "system"
	}
	triggerType := strings.TrimSpace(opts.TriggerType)
	if triggerType == "" {
		triggerType = "api"
	}
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "created"
	}
	metadata := opts.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{"run_mode":"sync"}`)
	}

	row, err := controlplane.NewRunRepository(db).Create(context.Background(), controlplane.Run{
		OrganizationID: orgID,
		ProjectID:      opts.ProjectID,
		TaskID:         opts.TaskID,
		FlowNodeID:     opts.FlowNodeID,
		SessionID:      opts.SessionID,
		TurnID:         opts.TurnID,
		PrincipalType:  principalType,
		PrincipalID:    opts.PrincipalID,
		Status:         status,
		IdempotencyKey: opts.IdempotencyKey,
		TriggerType:    triggerType,
		Metadata:       metadata,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return &row
}

type MakeCapabilityPolicyOptions struct {
	PolicyLayer    string
	OrganizationID *uuid.UUID
	ProjectID      *uuid.UUID
	AgentID        *uuid.UUID
	Capability     string
	Effect         string
	Priority       int
	Conditions     json.RawMessage
	CreatedByType  string
	CreatedByID    *uuid.UUID
}

func MakeCapabilityPolicy(t testing.TB, db *pgxpool.Pool, opts MakeCapabilityPolicyOptions) repo.CapabilityPolicy {
	t.Helper()

	layer := strings.TrimSpace(opts.PolicyLayer)
	if layer == "" {
		layer = "org"
	}
	capability := strings.TrimSpace(opts.Capability)
	if capability == "" {
		capability = "system.file.read"
	}
	effect := strings.TrimSpace(opts.Effect)
	if effect == "" {
		effect = "allow"
	}
	createdByType := strings.TrimSpace(opts.CreatedByType)
	if createdByType == "" {
		createdByType = "system"
	}
	priority := opts.Priority
	if priority <= 0 {
		priority = 100
	}
	conditions := opts.Conditions
	if len(conditions) == 0 {
		conditions = json.RawMessage(`{}`)
	}

	row, err := repo.NewCapabilityPolicyRepo(db).Create(context.Background(), repo.CapabilityPolicy{
		PolicyLayer:    layer,
		OrganizationID: opts.OrganizationID,
		ProjectID:      opts.ProjectID,
		AgentID:        opts.AgentID,
		Capability:     capability,
		Effect:         effect,
		Conditions:     conditions,
		Priority:       priority,
		CreatedByType:  createdByType,
		CreatedByID:    opts.CreatedByID,
	})
	if err != nil {
		t.Fatalf("create capability policy: %v", err)
	}
	return row
}

type StubWorkerOutcome string

const (
	StubWorkerSuccess          StubWorkerOutcome = "success"
	StubWorkerTransientFailure StubWorkerOutcome = "transient_failure"
	StubWorkerPermanentFailure StubWorkerOutcome = "permanent_failure"
	StubWorkerTimeout          StubWorkerOutcome = "timeout"
)

var (
	ErrStubWorkerTransient = errors.New("stub worker transient failure")
	ErrStubWorkerPermanent = errors.New("stub worker permanent failure")
	ErrStubWorkerTimeout   = context.DeadlineExceeded
)

type StubControlPlaneWorker struct {
	outcome StubWorkerOutcome
}

func StubWorker(t testing.TB, outcome StubWorkerOutcome) *StubControlPlaneWorker {
	t.Helper()
	switch outcome {
	case StubWorkerSuccess, StubWorkerTransientFailure, StubWorkerPermanentFailure, StubWorkerTimeout:
		return &StubControlPlaneWorker{outcome: outcome}
	default:
		t.Fatalf("unsupported stub worker outcome: %q", outcome)
		return nil
	}
}

func (w *StubControlPlaneWorker) Execute(context.Context) error {
	if w == nil {
		return nil
	}
	switch w.outcome {
	case StubWorkerSuccess:
		return nil
	case StubWorkerTransientFailure:
		return ErrStubWorkerTransient
	case StubWorkerPermanentFailure:
		return ErrStubWorkerPermanent
	case StubWorkerTimeout:
		return ErrStubWorkerTimeout
	default:
		return nil
	}
}
