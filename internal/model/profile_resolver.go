package model

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const maxFallbackHops = 3

var (
	ErrNoProfileAssigned = errors.New("model: no profile assigned")
	ErrFallbackCycle     = errors.New("model: fallback cycle detected")
)

const (
	ScopeTypeOrganization = "organization"
	ScopeTypeProject      = "project"
	ScopeTypeAgent        = "agent"
	ScopeTypeFlowNode     = "flow_node"
)

type ModelProfile = repo.ModelProfile

type Scope struct {
	Type string
	ID   uuid.UUID
}

type ProfileResolver interface {
	Resolve(ctx context.Context, orgID uuid.UUID, purpose string, scopes ...Scope) (*ModelProfile, error)
}

type assignmentRepo interface {
	GetByScope(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, purpose string) (repo.ModelProfileAssignment, error)
}

type profileRepo interface {
	GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error)
}

type providerConnectionRepo interface {
	ListByProvider(ctx context.Context, organizationID, providerID uuid.UUID) ([]repo.ProviderConnection, error)
}

type profileResolver struct {
	assignments assignmentRepo
	profiles    profileRepo
	connections providerConnectionRepo
}

func NewProfileResolver(assignments assignmentRepo, profiles profileRepo, connections providerConnectionRepo) ProfileResolver {
	return &profileResolver{
		assignments: assignments,
		profiles:    profiles,
		connections: connections,
	}
}

func (r *profileResolver) Resolve(ctx context.Context, orgID uuid.UUID, purpose string, scopes ...Scope) (*ModelProfile, error) {
	scopeIDs := make(map[string]uuid.UUID, len(scopes))
	for _, scope := range scopes {
		scopeIDs[scope.Type] = scope.ID
	}

	for _, scopeType := range []string{ScopeTypeFlowNode, ScopeTypeAgent, ScopeTypeProject, ScopeTypeOrganization} {
		scopeID, ok := scopeIDs[scopeType]
		if !ok {
			continue
		}

		assignment, err := r.assignments.GetByScope(ctx, orgID, scopeType, scopeID, purpose)
		if errors.Is(err, repo.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get assignment for scope %s: %w", scopeType, err)
		}

		profile, err := r.profiles.GetCurrentByLogicalID(ctx, orgID, assignment.LogicalProfileID)
		if err != nil {
			return nil, fmt.Errorf("load profile %q: %w", assignment.LogicalProfileID, err)
		}

		resolved, err := r.resolveWithFallbacks(ctx, orgID, &profile)
		if err != nil {
			return nil, err
		}
		return resolved, nil
	}

	return nil, ErrNoProfileAssigned
}

func (r *profileResolver) resolveWithFallbacks(ctx context.Context, orgID uuid.UUID, profile *repo.ModelProfile) (*repo.ModelProfile, error) {
	current := profile
	seen := make(map[string]struct{}, maxFallbackHops)

	for hop := 0; hop < maxFallbackHops; hop++ {
		if _, exists := seen[current.LogicalProfileID]; exists {
			return nil, ErrFallbackCycle
		}
		seen[current.LogicalProfileID] = struct{}{}

		available, err := r.providerAvailable(ctx, orgID, current.ProviderID)
		if err != nil {
			return nil, err
		}
		if available {
			return current, nil
		}

		fallbackID := ""
		if current.FallbackProfileID != nil {
			fallbackID = strings.TrimSpace(*current.FallbackProfileID)
		}
		if fallbackID == "" {
			return current, nil
		}

		next, err := r.profiles.GetCurrentByLogicalID(ctx, orgID, fallbackID)
		if err != nil {
			return nil, fmt.Errorf("load fallback profile %q: %w", fallbackID, err)
		}
		current = &next
	}

	return nil, ErrFallbackCycle
}

func (r *profileResolver) providerAvailable(ctx context.Context, orgID, providerID uuid.UUID) (bool, error) {
	connections, err := r.connections.ListByProvider(ctx, orgID, providerID)
	if err != nil {
		return false, fmt.Errorf("list provider connections: %w", err)
	}

	for _, conn := range connections {
		if conn.IsEnabled && conn.HealthStatus != "unavailable" {
			return true, nil
		}
	}

	return false, nil
}
