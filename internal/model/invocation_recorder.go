package model

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type modelInvocationCreator interface {
	Create(ctx context.Context, invocation repo.ModelInvocation) (repo.ModelInvocation, error)
}

type InvocationRecorderOptions struct {
	Invocations  modelInvocationCreator
	Attribution  *AttributionMiddleware
	Rollup       *RollupUpdater
	Logger       *slog.Logger
	AsyncSpawner func(func())
}

type InvocationRecorder struct {
	invocations modelInvocationCreator
	attribution *AttributionMiddleware
	rollup      *RollupUpdater
	logger      *slog.Logger
	spawn       func(func())
}

type ModelInvocationInput struct {
	OrganizationID       uuid.UUID
	ModelProviderID      uuid.UUID
	ProviderConnectionID *uuid.UUID
	ModelProfileID       *string
	InvocationPurpose    string
	Status               string
	ModelName            string
	IsStreaming          bool
	Metadata             json.RawMessage
	InputTokens          *int
	OutputTokens         *int
	CacheReadTokens      *int
	AgentID              *uuid.UUID
	ProjectID            *uuid.UUID
	ProjectTaskID        *uuid.UUID
	SessionID            *uuid.UUID
	TurnID               *uuid.UUID
	RunID                *uuid.UUID
	RunStepID            *uuid.UUID
	RunAttemptID         *uuid.UUID
}

func NewInvocationRecorder(opts InvocationRecorderOptions) (*InvocationRecorder, error) {
	if opts.Invocations == nil {
		return nil, fmt.Errorf("invocation repository is required")
	}
	if opts.Attribution == nil {
		opts.Attribution = NewAttributionMiddleware()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.AsyncSpawner == nil {
		opts.AsyncSpawner = func(fn func()) { go fn() }
	}

	return &InvocationRecorder{
		invocations: opts.Invocations,
		attribution: opts.Attribution,
		rollup:      opts.Rollup,
		logger:      opts.Logger,
		spawn:       opts.AsyncSpawner,
	}, nil
}

func (r *InvocationRecorder) Create(ctx context.Context, input ModelInvocationInput) (repo.ModelInvocation, error) {
	if r == nil || r.invocations == nil || r.attribution == nil {
		return repo.ModelInvocation{}, fmt.Errorf("invocation recorder is not configured")
	}
	if input.ModelProviderID == uuid.Nil {
		return repo.ModelInvocation{}, fmt.Errorf("model_provider_id is required")
	}
	if input.ModelName == "" {
		return repo.ModelInvocation{}, fmt.Errorf("model_name is required")
	}

	attribution := r.attribution.Populate(ctx)
	organizationID := input.OrganizationID
	if organizationID == uuid.Nil {
		organizationID = attribution.OrganizationID
	}
	if organizationID == uuid.Nil {
		return repo.ModelInvocation{}, fmt.Errorf("organization_id is required")
	}

	invocation := repo.ModelInvocation{
		OrganizationID:       organizationID,
		ModelProviderID:      input.ModelProviderID,
		ProviderConnectionID: firstUUIDPointer(input.ProviderConnectionID, nil),
		ModelProfileID:       input.ModelProfileID,
		InvocationPurpose:    normalizeInvocationPurpose(firstString(input.InvocationPurpose, attribution.InvocationPurpose)),
		Status:               input.Status,
		ModelName:            input.ModelName,
		IsStreaming:          input.IsStreaming,
		Metadata:             normalizeInvocationMetadata(input.Metadata),
		InputTokens:          cloneIntPointer(input.InputTokens),
		OutputTokens:         cloneIntPointer(input.OutputTokens),
		CacheReadTokens:      cloneIntPointer(input.CacheReadTokens),
		AgentID:              firstUUIDPointer(input.AgentID, attribution.AgentID),
		ProjectID:            firstUUIDPointer(input.ProjectID, attribution.ProjectID),
		ProjectTaskID:        firstUUIDPointer(input.ProjectTaskID, attribution.ProjectTaskID),
		SessionID:            firstUUIDPointer(input.SessionID, attribution.SessionID),
		TurnID:               firstUUIDPointer(input.TurnID, attribution.TurnID),
		RunID:                firstUUIDPointer(input.RunID, attribution.RunID),
		RunStepID:            firstUUIDPointer(input.RunStepID, attribution.RunStepID),
		RunAttemptID:         firstUUIDPointer(input.RunAttemptID, attribution.RunAttemptID),
	}

	created, err := r.invocations.Create(ctx, invocation)
	if err != nil {
		return repo.ModelInvocation{}, err
	}

	if r.rollup != nil {
		r.spawn(func() {
			if updateErr := r.rollup.UpdateRunTokenCounts(context.Background(), created); updateErr != nil {
				r.logger.Warn("run token rollup update failed", "invocation_id", created.ID, "run_id", created.RunID, "error", updateErr)
			}
		})
	}

	return created, nil
}

func normalizeInvocationMetadata(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstUUIDPointer(values ...*uuid.UUID) *uuid.UUID {
	for _, value := range values {
		if value != nil {
			copied := *value
			return &copied
		}
	}
	return nil
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
