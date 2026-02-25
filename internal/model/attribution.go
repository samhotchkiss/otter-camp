package model

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

const DefaultInvocationPurpose = "agent_turn"

type InvocationContext struct {
	OrganizationID    uuid.UUID
	AgentID           *uuid.UUID
	ProjectID         *uuid.UUID
	ProjectTaskID     *uuid.UUID
	SessionID         *uuid.UUID
	TurnID            *uuid.UUID
	RunID             *uuid.UUID
	RunStepID         *uuid.UUID
	RunAttemptID      *uuid.UUID
	InvocationPurpose string
}

type invocationOrganizationIDContextKey struct{}
type invocationAgentIDContextKey struct{}
type invocationProjectIDContextKey struct{}
type invocationProjectTaskIDContextKey struct{}
type invocationSessionIDContextKey struct{}
type invocationTurnIDContextKey struct{}
type invocationRunIDContextKey struct{}
type invocationRunStepIDContextKey struct{}
type invocationRunAttemptIDContextKey struct{}
type invocationPurposeContextKey struct{}

func WithInvocationContext(ctx context.Context, invocation InvocationContext) context.Context {
	ctx = WithInvocationOrganizationID(ctx, invocation.OrganizationID)
	ctx = WithInvocationAgentID(ctx, invocation.AgentID)
	ctx = WithInvocationProjectID(ctx, invocation.ProjectID)
	ctx = WithInvocationProjectTaskID(ctx, invocation.ProjectTaskID)
	ctx = WithInvocationSessionID(ctx, invocation.SessionID)
	ctx = WithInvocationTurnID(ctx, invocation.TurnID)
	ctx = WithInvocationRunID(ctx, invocation.RunID)
	ctx = WithInvocationRunStepID(ctx, invocation.RunStepID)
	ctx = WithInvocationRunAttemptID(ctx, invocation.RunAttemptID)
	ctx = WithInvocationPurpose(ctx, invocation.InvocationPurpose)
	return ctx
}

func WithInvocationOrganizationID(ctx context.Context, organizationID uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationOrganizationIDContextKey{}, organizationID)
}

func WithInvocationAgentID(ctx context.Context, agentID *uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationAgentIDContextKey{}, cloneUUIDPointer(agentID))
}

func WithInvocationProjectID(ctx context.Context, projectID *uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationProjectIDContextKey{}, cloneUUIDPointer(projectID))
}

func WithInvocationProjectTaskID(ctx context.Context, projectTaskID *uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationProjectTaskIDContextKey{}, cloneUUIDPointer(projectTaskID))
}

func WithInvocationSessionID(ctx context.Context, sessionID *uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationSessionIDContextKey{}, cloneUUIDPointer(sessionID))
}

func WithInvocationTurnID(ctx context.Context, turnID *uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationTurnIDContextKey{}, cloneUUIDPointer(turnID))
}

func WithInvocationRunID(ctx context.Context, runID *uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationRunIDContextKey{}, cloneUUIDPointer(runID))
}

func WithInvocationRunStepID(ctx context.Context, runStepID *uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationRunStepIDContextKey{}, cloneUUIDPointer(runStepID))
}

func WithInvocationRunAttemptID(ctx context.Context, runAttemptID *uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationRunAttemptIDContextKey{}, cloneUUIDPointer(runAttemptID))
}

func WithInvocationPurpose(ctx context.Context, purpose string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationPurposeContextKey{}, strings.TrimSpace(purpose))
}

type AttributionMiddleware struct{}

func NewAttributionMiddleware() *AttributionMiddleware {
	return &AttributionMiddleware{}
}

func (m *AttributionMiddleware) Populate(ctx context.Context) InvocationContext {
	if ctx == nil {
		return InvocationContext{}
	}

	invocation := InvocationContext{}
	if organizationID, ok := ctx.Value(invocationOrganizationIDContextKey{}).(uuid.UUID); ok {
		invocation.OrganizationID = organizationID
	}
	if value, ok := ctx.Value(invocationAgentIDContextKey{}).(*uuid.UUID); ok {
		invocation.AgentID = cloneUUIDPointer(value)
	}
	if value, ok := ctx.Value(invocationProjectIDContextKey{}).(*uuid.UUID); ok {
		invocation.ProjectID = cloneUUIDPointer(value)
	}
	if value, ok := ctx.Value(invocationProjectTaskIDContextKey{}).(*uuid.UUID); ok {
		invocation.ProjectTaskID = cloneUUIDPointer(value)
	}
	if value, ok := ctx.Value(invocationSessionIDContextKey{}).(*uuid.UUID); ok {
		invocation.SessionID = cloneUUIDPointer(value)
	}
	if value, ok := ctx.Value(invocationTurnIDContextKey{}).(*uuid.UUID); ok {
		invocation.TurnID = cloneUUIDPointer(value)
	}
	if value, ok := ctx.Value(invocationRunIDContextKey{}).(*uuid.UUID); ok {
		invocation.RunID = cloneUUIDPointer(value)
	}
	if value, ok := ctx.Value(invocationRunStepIDContextKey{}).(*uuid.UUID); ok {
		invocation.RunStepID = cloneUUIDPointer(value)
	}
	if value, ok := ctx.Value(invocationRunAttemptIDContextKey{}).(*uuid.UUID); ok {
		invocation.RunAttemptID = cloneUUIDPointer(value)
	}
	if purpose, ok := ctx.Value(invocationPurposeContextKey{}).(string); ok {
		invocation.InvocationPurpose = strings.TrimSpace(purpose)
	}

	return invocation
}

func normalizeInvocationPurpose(purpose string) string {
	trimmed := strings.TrimSpace(purpose)
	if trimmed == "" {
		return DefaultInvocationPurpose
	}
	return trimmed
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
