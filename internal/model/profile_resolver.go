package model

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	ErrNoProfileAssigned = errors.New("no model profile assignment for requested scope and purpose")
	ErrFallbackCycle     = errors.New("fallback profile cycle detected")
	ErrInvalidScopeType  = errors.New("invalid scope type")
)

type Scope struct {
	Type string
	ID   uuid.UUID
}

type ModelProfile = repo.ModelProfile

type ProfileResolver interface {
	Resolve(ctx context.Context, orgID uuid.UUID, purpose string, scopes ...Scope) (*ModelProfile, error)
}

type assignmentLookup interface {
	GetByScope(ctx context.Context, organizationID uuid.UUID, scopeType string, scopeID uuid.UUID, invocationPurpose string) (repo.ModelProfileAssignment, error)
}

type profileLookup interface {
	GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error)
}

type resolver struct {
	assignments assignmentLookup
	profiles    profileLookup
}

func NewProfileResolver(assignments assignmentLookup, profiles profileLookup) ProfileResolver {
	return &resolver{
		assignments: assignments,
		profiles:    profiles,
	}
}

func (r *resolver) Resolve(ctx context.Context, orgID uuid.UUID, purpose string, scopes ...Scope) (*ModelProfile, error) {
	if r == nil || r.assignments == nil || r.profiles == nil {
		return nil, fmt.Errorf("profile resolver is not configured")
	}
	if orgID == uuid.Nil {
		return nil, fmt.Errorf("organization id is required")
	}

	invocationPurpose := strings.TrimSpace(purpose)
	if invocationPurpose == "" {
		return nil, fmt.Errorf("invocation purpose is required")
	}

	orderedScopes, err := normalizeScopes(orgID, scopes)
	if err != nil {
		return nil, err
	}

	for _, scope := range orderedScopes {
		assignment, err := r.assignments.GetByScope(ctx, orgID, scope.Type, scope.ID, invocationPurpose)
		if errors.Is(err, repo.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}

		profile, err := r.profiles.GetCurrentByLogicalID(ctx, orgID, assignment.LogicalProfileID)
		if err != nil {
			return nil, err
		}

		resolved, err := r.resolveFallback(ctx, orgID, profile)
		if err != nil {
			return nil, err
		}
		return &resolved, nil
	}

	return nil, ErrNoProfileAssigned
}

func (r *resolver) resolveFallback(ctx context.Context, orgID uuid.UUID, initial repo.ModelProfile) (repo.ModelProfile, error) {
	current := initial
	visited := map[string]struct{}{
		current.LogicalProfileID: {},
	}

	for hop := 0; hop < 3; hop++ {
		if current.FallbackProfileID == nil || strings.TrimSpace(*current.FallbackProfileID) == "" {
			return current, nil
		}

		nextLogicalID := strings.TrimSpace(*current.FallbackProfileID)
		if _, exists := visited[nextLogicalID]; exists {
			return repo.ModelProfile{}, ErrFallbackCycle
		}
		visited[nextLogicalID] = struct{}{}

		next, err := r.profiles.GetCurrentByLogicalID(ctx, orgID, nextLogicalID)
		if err != nil {
			return repo.ModelProfile{}, err
		}
		current = next
	}

	if current.FallbackProfileID != nil && strings.TrimSpace(*current.FallbackProfileID) != "" {
		return repo.ModelProfile{}, ErrFallbackCycle
	}
	return current, nil
}

func normalizeScopes(orgID uuid.UUID, scopes []Scope) ([]Scope, error) {
	normalized := make(map[string]uuid.UUID, len(scopes)+1)
	normalized["organization"] = orgID

	for _, scope := range scopes {
		scopeType := strings.TrimSpace(scope.Type)
		switch scopeType {
		case "flow_node", "agent", "project", "organization":
		default:
			return nil, fmt.Errorf("%w: %q", ErrInvalidScopeType, scope.Type)
		}
		if scope.ID == uuid.Nil {
			return nil, fmt.Errorf("scope id is required for %q", scopeType)
		}
		normalized[scopeType] = scope.ID
	}

	ordered := make([]Scope, 0, len(normalized))
	for _, scopeType := range []string{"flow_node", "agent", "project", "organization"} {
		if id, ok := normalized[scopeType]; ok {
			ordered = append(ordered, Scope{Type: scopeType, ID: id})
		}
	}
	return ordered, nil
}
