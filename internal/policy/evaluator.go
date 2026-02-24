package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const defaultCacheTTL = 5 * time.Minute

type EvaluationRequest struct {
	OrganizationID uuid.UUID
	ProjectID      *uuid.UUID
	AgentID        *uuid.UUID
	Capability     string
	Context        map[string]any
}

type PolicyDecision struct {
	Effect     string
	Layer      string
	Reason     string
	Conditions map[string]any
}

type CapabilityPolicyRepository interface {
	ListByLayer(ctx context.Context, layer string, capability string, organizationID, projectID, agentID *uuid.UUID) ([]repo.CapabilityPolicy, error)
}

type EvaluatorOptions struct {
	Policies CapabilityPolicyRepository
	Budgets  budget.BudgetService
	Clock    clock.Clock
	CacheTTL time.Duration
}

type cacheEntry struct {
	policies  []repo.CapabilityPolicy
	expiresAt time.Time
}

type PolicyEvaluator struct {
	policies CapabilityPolicyRepository
	budgets  budget.BudgetService
	clock    clock.Clock
	cacheTTL time.Duration

	instanceMu        sync.RWMutex
	instanceByCap     map[string][]repo.CapabilityPolicy
	orgCache          sync.Map
	projectCache      sync.Map
	agentProfileCache sync.Map
}

func NewPolicyEvaluator(opts EvaluatorOptions) (*PolicyEvaluator, error) {
	if opts.Policies == nil {
		return nil, fmt.Errorf("policy evaluator requires a policy repository")
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = defaultCacheTTL
	}

	return &PolicyEvaluator{
		policies:      opts.Policies,
		budgets:       opts.Budgets,
		clock:         opts.Clock,
		cacheTTL:      opts.CacheTTL,
		instanceByCap: make(map[string][]repo.CapabilityPolicy),
	}, nil
}

func (e *PolicyEvaluator) LoadInstancePolicies(ctx context.Context) error {
	instanceRows, err := e.policies.ListByLayer(ctx, "instance", "", nil, nil, nil)
	if err != nil {
		return err
	}

	compiled := make(map[string][]repo.CapabilityPolicy)
	for _, row := range instanceRows {
		capability := strings.TrimSpace(row.Capability)
		if capability == "" {
			continue
		}
		compiled[capability] = append(compiled[capability], row)
	}

	e.instanceMu.Lock()
	e.instanceByCap = compiled
	e.instanceMu.Unlock()
	return nil
}

func (e *PolicyEvaluator) Evaluate(ctx context.Context, req EvaluationRequest) PolicyDecision {
	capability := strings.TrimSpace(req.Capability)
	if capability == "" {
		return PolicyDecision{
			Effect: "allow",
			Layer:  "none",
			Reason: "silence passes",
		}
	}

	var allowDecision *PolicyDecision
	consider := func(decision PolicyDecision, ok bool) *PolicyDecision {
		if !ok {
			return nil
		}
		if decision.Effect == "deny" {
			return &decision
		}
		allowDecision = &decision
		return nil
	}

	instance := e.instancePolicies(capability)
	if matched := consider(evaluateLayer("instance", instance, req.Context)); matched != nil {
		return *matched
	}

	orgPolicies, err := e.cachedOrgPolicies(ctx, req.OrganizationID, capability)
	if err != nil {
		return errorDecision(err)
	}
	if matched := consider(evaluateLayer("org", orgPolicies, req.Context)); matched != nil {
		return *matched
	}

	if req.ProjectID != nil {
		projectPolicies, cacheErr := e.cachedProjectPolicies(ctx, *req.ProjectID, req.OrganizationID, capability)
		if cacheErr != nil {
			return errorDecision(cacheErr)
		}
		if matched := consider(evaluateLayer("project", projectPolicies, req.Context)); matched != nil {
			return *matched
		}
	}

	if req.AgentID != nil {
		agentPolicies, cacheErr := e.cachedAgentPolicies(ctx, *req.AgentID, req.OrganizationID, capability)
		if cacheErr != nil {
			return errorDecision(cacheErr)
		}
		if matched := consider(evaluateLayer("agent_profile", agentPolicies, req.Context)); matched != nil {
			return *matched
		}
	}

	requestPolicies, err := e.policies.ListByLayer(ctx, "request", capability, &req.OrganizationID, req.ProjectID, req.AgentID)
	if err != nil {
		return errorDecision(err)
	}
	if matched := consider(evaluateLayer("request", requestPolicies, req.Context)); matched != nil {
		return *matched
	}

	if allowDecision != nil {
		return *allowDecision
	}

	return PolicyDecision{
		Effect: "allow",
		Layer:  "none",
		Reason: "silence passes",
	}
}

func (e *PolicyEvaluator) CheckBudgetGate(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID) (allowed bool, reason string) {
	if e.budgets == nil {
		return true, "budget service not configured"
	}

	result, err := e.budgets.CheckBudget(ctx, orgID, projectID, agentID, 0)
	if err != nil {
		return false, fmt.Sprintf("budget check failed: %v", err)
	}
	if result == nil {
		return true, "budget service returned no result"
	}
	if !result.Allowed && result.HardLimitHit {
		return false, "budget hard limit exceeded"
	}
	return true, "budget gate passed"
}

func (e *PolicyEvaluator) InvalidateOrgCache(orgID uuid.UUID) {
	prefix := orgID.String() + "|"
	deleteByPrefix(&e.orgCache, prefix)
}

func (e *PolicyEvaluator) InvalidateProjectCache(projectID uuid.UUID) {
	prefix := projectID.String() + "|"
	deleteByPrefix(&e.projectCache, prefix)
}

func (e *PolicyEvaluator) InvalidateAgentCache(agentID uuid.UUID) {
	prefix := agentID.String() + "|"
	deleteByPrefix(&e.agentProfileCache, prefix)
}

func (e *PolicyEvaluator) instancePolicies(capability string) []repo.CapabilityPolicy {
	e.instanceMu.RLock()
	defer e.instanceMu.RUnlock()
	return clonePolicies(e.instanceByCap[capability])
}

func (e *PolicyEvaluator) cachedOrgPolicies(ctx context.Context, orgID uuid.UUID, capability string) ([]repo.CapabilityPolicy, error) {
	cacheKey := orgID.String() + "|" + capability
	if cached, ok := loadTTLCache(&e.orgCache, cacheKey, e.clock.Now()); ok {
		return cached, nil
	}

	organizationID := orgID
	fresh, err := e.policies.ListByLayer(ctx, "org", capability, &organizationID, nil, nil)
	if err != nil {
		return nil, err
	}
	storeTTLCache(&e.orgCache, cacheKey, fresh, e.clock.Now().Add(e.cacheTTL))
	return clonePolicies(fresh), nil
}

func (e *PolicyEvaluator) cachedProjectPolicies(ctx context.Context, projectID uuid.UUID, orgID uuid.UUID, capability string) ([]repo.CapabilityPolicy, error) {
	cacheKey := projectID.String() + "|" + capability
	if cached, ok := loadTTLCache(&e.projectCache, cacheKey, e.clock.Now()); ok {
		return cached, nil
	}

	organizationID := orgID
	project := projectID
	fresh, err := e.policies.ListByLayer(ctx, "project", capability, &organizationID, &project, nil)
	if err != nil {
		return nil, err
	}
	storeTTLCache(&e.projectCache, cacheKey, fresh, e.clock.Now().Add(e.cacheTTL))
	return clonePolicies(fresh), nil
}

func (e *PolicyEvaluator) cachedAgentPolicies(ctx context.Context, agentID uuid.UUID, orgID uuid.UUID, capability string) ([]repo.CapabilityPolicy, error) {
	cacheKey := agentID.String() + "|" + capability
	if cached, ok := loadSessionCache(&e.agentProfileCache, cacheKey); ok {
		return cached, nil
	}

	organizationID := orgID
	agent := agentID
	fresh, err := e.policies.ListByLayer(ctx, "agent_profile", capability, &organizationID, nil, &agent)
	if err != nil {
		return nil, err
	}
	storeSessionCache(&e.agentProfileCache, cacheKey, fresh)
	return clonePolicies(fresh), nil
}

func evaluateLayer(layer string, policies []repo.CapabilityPolicy, evalContext map[string]any) (PolicyDecision, bool) {
	if len(policies) == 0 {
		return PolicyDecision{}, false
	}

	for _, policyRow := range policies {
		if strings.EqualFold(policyRow.Effect, "deny") {
			return PolicyDecision{
				Effect: "deny",
				Layer:  layer,
				Reason: "denied by policy",
			}, true
		}
	}

	for _, policyRow := range policies {
		if !strings.EqualFold(policyRow.Effect, "allow") {
			continue
		}
		conditions := decodeConditions(policyRow.Conditions)
		if conditionsSatisfied(conditions, evalContext) {
			return PolicyDecision{
				Effect:     "allow",
				Layer:      layer,
				Reason:     "allowed by policy",
				Conditions: conditions,
			}, true
		}
	}

	return PolicyDecision{}, false
}

func errorDecision(err error) PolicyDecision {
	return PolicyDecision{
		Effect: "deny",
		Layer:  "error",
		Reason: fmt.Sprintf("policy evaluation failed: %v", err),
	}
}

func conditionsSatisfied(conditions map[string]any, evalContext map[string]any) bool {
	if len(conditions) == 0 {
		return true
	}

	if maxFileRaw, ok := conditions["max_file_size_kb"]; ok {
		maxFileSize, ok := toInt64(maxFileRaw)
		if ok {
			fileSize, exists := toInt64FromContext(evalContext, "file_size_kb")
			if !exists || fileSize > maxFileSize {
				return false
			}
		}
	}

	if maxTokensRaw, ok := conditions["max_token_count"]; ok {
		maxTokens, ok := toInt64(maxTokensRaw)
		if ok {
			tokenCount, exists := toInt64FromContext(evalContext, "token_count")
			if !exists || tokenCount > maxTokens {
				return false
			}
		}
	}

	if allowedDomainsRaw, ok := conditions["allowed_domains"]; ok {
		allowedDomains := toStringSlice(allowedDomainsRaw)
		if len(allowedDomains) > 0 {
			domain := contextString(evalContext, "domain")
			if domain == "" {
				domain = contextString(evalContext, "request_domain")
			}
			if domain == "" || !containsString(allowedDomains, domain) {
				return false
			}
		}
	}

	return true
}

func decodeConditions(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func loadTTLCache(store *sync.Map, key string, now time.Time) ([]repo.CapabilityPolicy, bool) {
	value, ok := store.Load(key)
	if !ok {
		return nil, false
	}
	entry, ok := value.(cacheEntry)
	if !ok || now.After(entry.expiresAt) {
		store.Delete(key)
		return nil, false
	}
	return clonePolicies(entry.policies), true
}

func storeTTLCache(store *sync.Map, key string, policies []repo.CapabilityPolicy, expiresAt time.Time) {
	store.Store(key, cacheEntry{
		policies:  clonePolicies(policies),
		expiresAt: expiresAt,
	})
}

func loadSessionCache(store *sync.Map, key string) ([]repo.CapabilityPolicy, bool) {
	value, ok := store.Load(key)
	if !ok {
		return nil, false
	}
	policies, ok := value.([]repo.CapabilityPolicy)
	if !ok {
		store.Delete(key)
		return nil, false
	}
	return clonePolicies(policies), true
}

func storeSessionCache(store *sync.Map, key string, policies []repo.CapabilityPolicy) {
	// NOTE: cache is per-process - multiple workers will keep independent cache state and TTL windows.
	store.Store(key, clonePolicies(policies))
}

func deleteByPrefix(store *sync.Map, prefix string) {
	store.Range(func(key, _ any) bool {
		keyText, ok := key.(string)
		if ok && strings.HasPrefix(keyText, prefix) {
			store.Delete(key)
		}
		return true
	})
}

func clonePolicies(in []repo.CapabilityPolicy) []repo.CapabilityPolicy {
	if in == nil {
		return nil
	}
	out := make([]repo.CapabilityPolicy, len(in))
	copy(out, in)
	return out
}

func toInt64FromContext(ctx map[string]any, key string) (int64, bool) {
	if ctx == nil {
		return 0, false
	}
	value, ok := ctx[key]
	if !ok {
		return 0, false
	}
	return toInt64(value)
}

func toInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		return int64(typed), true
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case float64:
		return int64(typed), true
	default:
		return 0, false
	}
}

func toStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, strings.TrimSpace(item))
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func contextString(ctx map[string]any, key string) string {
	if ctx == nil {
		return ""
	}
	value, ok := ctx[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}
