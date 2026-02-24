package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const instancePolicyWriteMessage = "instance-layer policies are managed by the system and cannot be modified via the API"

var (
	validPolicyLayers = map[string]struct{}{
		"instance":      {},
		"org":           {},
		"project":       {},
		"agent_profile": {},
		"request":       {},
	}
	validPolicyEffects = map[string]struct{}{
		"allow": {},
		"deny":  {},
	}
)

type capabilityPolicyRepository interface {
	Create(ctx context.Context, policy repo.CapabilityPolicy) (repo.CapabilityPolicy, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.CapabilityPolicy, error)
	ListByOrganization(ctx context.Context, organizationID uuid.UUID, filters repo.CapabilityPolicyFilters) ([]repo.CapabilityPolicy, error)
	Update(ctx context.Context, policy repo.CapabilityPolicy) (repo.CapabilityPolicy, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type policyProjectLookupRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type policyAgentLookupRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type policyEvaluator interface {
	EvaluateWithTrace(ctx context.Context, req policy.EvaluationRequest) (policy.PolicyDecision, []policy.PolicyTraceEntry)
	InvalidateCache(orgID uuid.UUID, capability string)
	LoadInstancePolicies(ctx context.Context) error
}

type CapabilityPolicyRouteOptions struct {
	Policies      capabilityPolicyRepository
	Projects      policyProjectLookupRepository
	Agents        policyAgentLookupRepository
	Evaluator     policyEvaluator
	AuditRecorder audit.AuditRecorder
	BootstrapMode bool
}

type CapabilityPolicyRouteRegistrar struct {
	handlers capabilityPolicyHandlers
}

type capabilityPolicyHandlers struct {
	policies      capabilityPolicyRepository
	projects      policyProjectLookupRepository
	agents        policyAgentLookupRepository
	evaluator     policyEvaluator
	auditRecorder audit.AuditRecorder
	bootstrapMode bool
}

type writeCapabilityPolicyRequest struct {
	PolicyLayer    string          `json:"policy_layer"`
	OrganizationID *uuid.UUID      `json:"organization_id"`
	ProjectID      *uuid.UUID      `json:"project_id"`
	AgentID        *uuid.UUID      `json:"agent_id"`
	Capability     string          `json:"capability"`
	Effect         string          `json:"effect"`
	Conditions     json.RawMessage `json:"conditions"`
	Priority       *int            `json:"priority"`
}

type evaluateCapabilityPolicyRequest struct {
	Capability     string         `json:"capability"`
	OrganizationID uuid.UUID      `json:"organization_id"`
	ProjectID      *uuid.UUID     `json:"project_id"`
	AgentID        *uuid.UUID     `json:"agent_id"`
	Context        map[string]any `json:"context"`
}

type capabilityPolicyResponse struct {
	ID             uuid.UUID       `json:"id"`
	PolicyLayer    string          `json:"policy_layer"`
	OrganizationID *uuid.UUID      `json:"organization_id,omitempty"`
	ProjectID      *uuid.UUID      `json:"project_id,omitempty"`
	AgentID        *uuid.UUID      `json:"agent_id,omitempty"`
	Capability     string          `json:"capability"`
	Effect         string          `json:"effect"`
	Conditions     json.RawMessage `json:"conditions"`
	Priority       int             `json:"priority"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type policyTraceResponse struct {
	Layer         string     `json:"layer"`
	Effect        string     `json:"effect"`
	MatchedRuleID *uuid.UUID `json:"matched_rule_id,omitempty"`
	Reason        string     `json:"reason"`
}

type policyEvaluationResponse struct {
	Effect string                `json:"effect"`
	Layer  string                `json:"layer"`
	Reason string                `json:"reason"`
	Trace  []policyTraceResponse `json:"trace"`
}

func NewCapabilityPolicyRouteRegistrar(opts CapabilityPolicyRouteOptions) *CapabilityPolicyRouteRegistrar {
	recorder := opts.AuditRecorder
	if recorder == nil {
		recorder = audit.NewNoopRecorder()
	}

	return &CapabilityPolicyRouteRegistrar{
		handlers: capabilityPolicyHandlers{
			policies:      opts.Policies,
			projects:      opts.Projects,
			agents:        opts.Agents,
			evaluator:     opts.Evaluator,
			auditRecorder: recorder,
			bootstrapMode: opts.BootstrapMode,
		},
	}
}

func (r *CapabilityPolicyRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(middleware.RequireRole("admin")).Get("/control/policies", r.handlers.listPolicies)
	router.With(middleware.RequireRole("admin")).Post("/control/policies", r.handlers.createPolicy)
	router.With(middleware.RequireRole("admin")).Put("/control/policies/{id}", r.handlers.updatePolicy)
	router.With(middleware.RequireRole("admin")).Delete("/control/policies/{id}", r.handlers.deletePolicy)
	router.Post("/control/policies/evaluate", r.handlers.evaluatePolicy)
}

func (h capabilityPolicyHandlers) listPolicies(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.policies == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "capability policy repository unavailable")
		return
	}

	filters, err := h.parsePolicyFilters(r)
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}

	if filters.ProjectID != nil {
		if validationErr := h.validateProjectScope(r.Context(), principal.OrganizationID, *filters.ProjectID); validationErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, validationErr.Error())
			return
		}
	}
	if filters.AgentID != nil {
		if validationErr := h.validateAgentScope(r.Context(), principal.OrganizationID, *filters.AgentID); validationErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, validationErr.Error())
			return
		}
	}

	items, listErr := h.policies.ListByOrganization(r.Context(), principal.OrganizationID, filters)
	if listErr != nil {
		status, code, message := mapPolicyRepoError(listErr)
		responder.Error(w, status, code, message)
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	sortCapabilityPoliciesByCreatedAt(items, params.Order == "asc")
	page, nextCursor, pageErr := paginateByTimeID(items, params, func(item repo.CapabilityPolicy) time.Time {
		return item.CreatedAt
	}, func(item repo.CapabilityPolicy) uuid.UUID {
		return item.ID
	})
	if pageErr != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	responseItems := make([]capabilityPolicyResponse, 0, len(page))
	for _, item := range page {
		responseItems = append(responseItems, toCapabilityPolicyResponse(item))
	}
	total := len(items)
	responder.JSONList(w, http.StatusOK, responseItems, api.PaginationMeta{
		NextCursor: nextCursor,
		Limit:      params.Limit,
		Total:      &total,
	})
}

func (h capabilityPolicyHandlers) createPolicy(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.policies == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "capability policy repository unavailable")
		return
	}

	var req writeCapabilityPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	normalizedLayer := strings.ToLower(strings.TrimSpace(req.PolicyLayer))
	if h.instanceWriteForbidden(normalizedLayer) {
		h.recordInstanceWriteAttempt(r.Context(), principal, nil, normalizedLayer, strings.TrimSpace(req.Capability))
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, instancePolicyWriteMessage)
		return
	}

	policyRow, validationErr := h.buildPolicyRow(r.Context(), principal.OrganizationID, req)
	if validationErr != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, validationErr.Error())
		return
	}
	policyRow.CreatedByType = "human_user"
	policyRow.CreatedByID = pointerToUUIDValue(principal.UserID)

	created, createErr := h.policies.Create(r.Context(), policyRow)
	if createErr != nil {
		status, code, message := mapPolicyRepoError(createErr)
		responder.Error(w, status, code, message)
		return
	}

	h.invalidatePolicyCache(r.Context(), principal.OrganizationID, "", created.Capability, created.PolicyLayer)
	h.recordPolicyWrite(r.Context(), principal, "policy.created", created.ID, map[string]any{
		"policy_layer": created.PolicyLayer,
		"capability":   created.Capability,
	})
	responder.JSON(w, http.StatusCreated, toCapabilityPolicyResponse(created))
}

func (h capabilityPolicyHandlers) updatePolicy(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.policies == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "capability policy repository unavailable")
		return
	}

	policyID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	existing, err := h.policies.GetByID(r.Context(), policyID)
	if errors.Is(err, repo.ErrNotFound) {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if err != nil {
		status, code, message := mapPolicyRepoError(err)
		responder.Error(w, status, code, message)
		return
	}
	if !h.policyVisibleToOrganization(r.Context(), existing, principal.OrganizationID) {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if h.instanceWriteForbidden(existing.PolicyLayer) {
		h.recordInstanceWriteAttempt(r.Context(), principal, &policyID, existing.PolicyLayer, existing.Capability)
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, instancePolicyWriteMessage)
		return
	}

	var req writeCapabilityPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	normalizedLayer := strings.ToLower(strings.TrimSpace(req.PolicyLayer))
	if h.instanceWriteForbidden(normalizedLayer) {
		h.recordInstanceWriteAttempt(r.Context(), principal, &policyID, normalizedLayer, strings.TrimSpace(req.Capability))
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, instancePolicyWriteMessage)
		return
	}

	policyRow, validationErr := h.buildPolicyRow(r.Context(), principal.OrganizationID, req)
	if validationErr != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, validationErr.Error())
		return
	}
	policyRow.ID = policyID

	updated, updateErr := h.policies.Update(r.Context(), policyRow)
	if updateErr != nil {
		status, code, message := mapPolicyRepoError(updateErr)
		responder.Error(w, status, code, message)
		return
	}

	h.invalidatePolicyCache(r.Context(), principal.OrganizationID, existing.Capability, updated.Capability, updated.PolicyLayer)
	h.recordPolicyWrite(r.Context(), principal, "policy.updated", updated.ID, map[string]any{
		"policy_layer": updated.PolicyLayer,
		"capability":   updated.Capability,
	})
	responder.JSON(w, http.StatusOK, toCapabilityPolicyResponse(updated))
}

func (h capabilityPolicyHandlers) deletePolicy(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.policies == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "capability policy repository unavailable")
		return
	}

	policyID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}

	existing, err := h.policies.GetByID(r.Context(), policyID)
	if errors.Is(err, repo.ErrNotFound) {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if err != nil {
		status, code, message := mapPolicyRepoError(err)
		responder.Error(w, status, code, message)
		return
	}
	if !h.policyVisibleToOrganization(r.Context(), existing, principal.OrganizationID) {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	if h.instanceWriteForbidden(existing.PolicyLayer) {
		h.recordInstanceWriteAttempt(r.Context(), principal, &policyID, existing.PolicyLayer, existing.Capability)
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, instancePolicyWriteMessage)
		return
	}

	if err := h.policies.Delete(r.Context(), policyID); err != nil {
		status, code, message := mapPolicyRepoError(err)
		responder.Error(w, status, code, message)
		return
	}

	h.invalidatePolicyCache(r.Context(), principal.OrganizationID, existing.Capability, "", existing.PolicyLayer)
	h.recordPolicyWrite(r.Context(), principal, "policy.deleted", existing.ID, map[string]any{
		"policy_layer": existing.PolicyLayer,
		"capability":   existing.Capability,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h capabilityPolicyHandlers) evaluatePolicy(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.evaluator == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "policy evaluator unavailable")
		return
	}

	var req evaluateCapabilityPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Capability) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "capability is required")
		return
	}
	if req.OrganizationID == uuid.Nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "organization_id is required")
		return
	}
	if req.OrganizationID != principal.OrganizationID {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	if req.ProjectID != nil {
		if validationErr := h.validateProjectScope(r.Context(), principal.OrganizationID, *req.ProjectID); validationErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, validationErr.Error())
			return
		}
	}
	if req.AgentID != nil {
		if validationErr := h.validateAgentScope(r.Context(), principal.OrganizationID, *req.AgentID); validationErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, validationErr.Error())
			return
		}
	}

	decision, trace := h.evaluator.EvaluateWithTrace(r.Context(), policy.EvaluationRequest{
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		AgentID:        req.AgentID,
		Capability:     req.Capability,
		Context:        req.Context,
	})

	traceResponse := make([]policyTraceResponse, 0, len(trace))
	for _, item := range trace {
		traceResponse = append(traceResponse, policyTraceResponse{
			Layer:         item.Layer,
			Effect:        item.Effect,
			MatchedRuleID: item.MatchedRuleID,
			Reason:        item.Reason,
		})
	}

	responder.JSON(w, http.StatusOK, policyEvaluationResponse{
		Effect: decision.Effect,
		Layer:  decision.Layer,
		Reason: decision.Reason,
		Trace:  traceResponse,
	})
}

func (h capabilityPolicyHandlers) parsePolicyFilters(r *http.Request) (repo.CapabilityPolicyFilters, error) {
	query := r.URL.Query()
	filters := repo.CapabilityPolicyFilters{}

	if raw := strings.TrimSpace(query.Get("policy_layer")); raw != "" {
		layer := strings.ToLower(raw)
		if _, ok := validPolicyLayers[layer]; !ok {
			return repo.CapabilityPolicyFilters{}, errors.New("policy_layer must be one of instance, org, project, agent_profile, request")
		}
		filters.PolicyLayer = &layer
	}
	if raw := strings.TrimSpace(query.Get("capability")); raw != "" {
		capability := raw
		filters.Capability = &capability
	}
	if raw := strings.TrimSpace(query.Get("project_id")); raw != "" {
		projectID, err := uuid.Parse(raw)
		if err != nil {
			return repo.CapabilityPolicyFilters{}, errors.New("project_id must be a valid uuid")
		}
		filters.ProjectID = &projectID
	}
	if raw := strings.TrimSpace(query.Get("agent_id")); raw != "" {
		agentID, err := uuid.Parse(raw)
		if err != nil {
			return repo.CapabilityPolicyFilters{}, errors.New("agent_id must be a valid uuid")
		}
		filters.AgentID = &agentID
	}
	return filters, nil
}

func (h capabilityPolicyHandlers) buildPolicyRow(ctx context.Context, organizationID uuid.UUID, req writeCapabilityPolicyRequest) (repo.CapabilityPolicy, error) {
	layer := strings.ToLower(strings.TrimSpace(req.PolicyLayer))
	if _, ok := validPolicyLayers[layer]; !ok {
		return repo.CapabilityPolicy{}, errors.New("policy_layer must be one of instance, org, project, agent_profile, request")
	}

	capability := strings.TrimSpace(req.Capability)
	if capability == "" {
		return repo.CapabilityPolicy{}, errors.New("capability is required")
	}

	effect := strings.ToLower(strings.TrimSpace(req.Effect))
	if _, ok := validPolicyEffects[effect]; !ok {
		return repo.CapabilityPolicy{}, errors.New("effect must be allow or deny")
	}

	priority := 100
	if req.Priority != nil {
		priority = *req.Priority
	}
	if priority < 1 || priority > 1000 {
		return repo.CapabilityPolicy{}, errors.New("priority must be between 1 and 1000")
	}

	if req.OrganizationID != nil && *req.OrganizationID != organizationID {
		return repo.CapabilityPolicy{}, errors.New("organization_id must match caller organization")
	}

	conditions, err := normalizePolicyConditionsJSON(req.Conditions)
	if err != nil {
		return repo.CapabilityPolicy{}, err
	}

	record := repo.CapabilityPolicy{
		PolicyLayer: layer,
		Capability:  capability,
		Effect:      effect,
		Conditions:  conditions,
		Priority:    priority,
	}

	switch layer {
	case "instance":
		if req.ProjectID != nil || req.AgentID != nil {
			return repo.CapabilityPolicy{}, errors.New("instance policies cannot set project_id or agent_id")
		}
	case "org", "request":
		if req.ProjectID != nil || req.AgentID != nil {
			return repo.CapabilityPolicy{}, errors.New("org and request policies cannot set project_id or agent_id")
		}
		record.OrganizationID = pointerToUUIDValue(organizationID)
	case "project":
		if req.ProjectID == nil {
			return repo.CapabilityPolicy{}, errors.New("project_id is required when policy_layer=project")
		}
		if req.AgentID != nil {
			return repo.CapabilityPolicy{}, errors.New("project policies cannot set agent_id")
		}
		if validationErr := h.validateProjectScope(ctx, organizationID, *req.ProjectID); validationErr != nil {
			return repo.CapabilityPolicy{}, validationErr
		}
		record.OrganizationID = pointerToUUIDValue(organizationID)
		record.ProjectID = req.ProjectID
	case "agent_profile":
		if req.AgentID == nil {
			return repo.CapabilityPolicy{}, errors.New("agent_id is required when policy_layer=agent_profile")
		}
		if req.ProjectID != nil {
			return repo.CapabilityPolicy{}, errors.New("agent_profile policies cannot set project_id")
		}
		if validationErr := h.validateAgentScope(ctx, organizationID, *req.AgentID); validationErr != nil {
			return repo.CapabilityPolicy{}, validationErr
		}
		record.OrganizationID = pointerToUUIDValue(organizationID)
		record.AgentID = req.AgentID
	}

	return record, nil
}

func (h capabilityPolicyHandlers) validateProjectScope(ctx context.Context, organizationID uuid.UUID, projectID uuid.UUID) error {
	if h.projects == nil {
		return errors.New("project repository unavailable")
	}
	found, err := h.projects.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return errors.New("project_id must reference a project in the caller organization")
		}
		return errors.New("project lookup failed")
	}
	if found.OrganizationID != organizationID {
		return errors.New("project_id must reference a project in the caller organization")
	}
	return nil
}

func (h capabilityPolicyHandlers) validateAgentScope(ctx context.Context, organizationID uuid.UUID, agentID uuid.UUID) error {
	if h.agents == nil {
		return errors.New("agent repository unavailable")
	}
	found, err := h.agents.GetByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return errors.New("agent_id must reference an agent in the caller organization")
		}
		return errors.New("agent lookup failed")
	}
	if found.OrganizationID != organizationID {
		return errors.New("agent_id must reference an agent in the caller organization")
	}
	return nil
}

func (h capabilityPolicyHandlers) policyVisibleToOrganization(ctx context.Context, item repo.CapabilityPolicy, organizationID uuid.UUID) bool {
	switch strings.TrimSpace(item.PolicyLayer) {
	case "instance":
		return true
	case "org", "request":
		return item.OrganizationID != nil && *item.OrganizationID == organizationID
	case "project":
		if item.ProjectID == nil {
			return false
		}
		return h.validateProjectScope(ctx, organizationID, *item.ProjectID) == nil
	case "agent_profile":
		if item.AgentID == nil {
			return false
		}
		return h.validateAgentScope(ctx, organizationID, *item.AgentID) == nil
	default:
		return false
	}
}

func (h capabilityPolicyHandlers) instanceWriteForbidden(policyLayer string) bool {
	return !h.bootstrapMode && strings.EqualFold(strings.TrimSpace(policyLayer), "instance")
}

func (h capabilityPolicyHandlers) invalidatePolicyCache(ctx context.Context, organizationID uuid.UUID, oldCapability, newCapability, layer string) {
	if h.evaluator == nil {
		return
	}
	oldCapability = strings.TrimSpace(oldCapability)
	newCapability = strings.TrimSpace(newCapability)
	if oldCapability != "" {
		h.evaluator.InvalidateCache(organizationID, oldCapability)
	}
	if newCapability != "" && newCapability != oldCapability {
		h.evaluator.InvalidateCache(organizationID, newCapability)
	}
	if strings.EqualFold(strings.TrimSpace(layer), "instance") {
		_ = h.evaluator.LoadInstancePolicies(ctx)
	}
}

func (h capabilityPolicyHandlers) recordPolicyWrite(ctx context.Context, principal middleware.Principal, eventType string, policyID uuid.UUID, metadata map[string]any) {
	if h.auditRecorder == nil {
		return
	}
	targetType := "capability_policy"
	targetID := policyID
	h.auditRecorder.RecordAsync(ctx, audit.Event{
		OrgID:         principal.OrganizationID,
		EventType:     eventType,
		PrincipalType: "human",
		PrincipalID:   principal.UserID,
		TargetType:    &targetType,
		TargetID:      &targetID,
		Metadata:      metadata,
	})
}

func (h capabilityPolicyHandlers) recordInstanceWriteAttempt(ctx context.Context, principal middleware.Principal, policyID *uuid.UUID, policyLayer, capability string) {
	if h.auditRecorder == nil {
		return
	}

	var targetType *string
	var targetID *uuid.UUID
	if policyID != nil && *policyID != uuid.Nil {
		text := "capability_policy"
		targetType = &text
		targetID = policyID
	}

	h.auditRecorder.RecordAsync(ctx, audit.Event{
		OrgID:         principal.OrganizationID,
		EventType:     "policy.instance_write_attempt",
		PrincipalType: "human",
		PrincipalID:   principal.UserID,
		TargetType:    targetType,
		TargetID:      targetID,
		Metadata: map[string]any{
			"policy_layer": strings.TrimSpace(policyLayer),
			"capability":   strings.TrimSpace(capability),
		},
	})
}

func mapPolicyRepoError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		return http.StatusNotFound, api.ErrCodeNotFound, "resource not found"
	case errors.Is(err, repo.ErrConflict):
		return http.StatusConflict, api.ErrCodeConflict, "conflict"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "internal server error"
	}
}

func normalizePolicyConditionsJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, errors.New("conditions must be valid json")
	}

	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || strings.EqualFold(trimmed, "null") {
		return json.RawMessage(`{}`), nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, errors.New("conditions must be a json object")
	}
	if decoded == nil {
		return json.RawMessage(`{}`), nil
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return nil, errors.New("conditions must be a json object")
	}
	return normalized, nil
}

func sortCapabilityPoliciesByCreatedAt(items []repo.CapabilityPolicy, asc bool) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.CreatedAt.Equal(right.CreatedAt) {
			if asc {
				return left.ID.String() < right.ID.String()
			}
			return left.ID.String() > right.ID.String()
		}
		if asc {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.CreatedAt.After(right.CreatedAt)
	})
}

func toCapabilityPolicyResponse(item repo.CapabilityPolicy) capabilityPolicyResponse {
	conditions := item.Conditions
	if len(conditions) == 0 {
		conditions = json.RawMessage(`{}`)
	}

	return capabilityPolicyResponse{
		ID:             item.ID,
		PolicyLayer:    item.PolicyLayer,
		OrganizationID: item.OrganizationID,
		ProjectID:      item.ProjectID,
		AgentID:        item.AgentID,
		Capability:     item.Capability,
		Effect:         item.Effect,
		Conditions:     conditions,
		Priority:       item.Priority,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func pointerToUUIDValue(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	copied := value
	return &copied
}
