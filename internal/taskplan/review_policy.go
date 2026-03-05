package taskplan

import (
	"encoding/json"
	"strings"
)

const (
	PolicyHumanReviewRequired  = "human_review_required"
	PolicyHumanReviewPreferred = "human_review_preferred"
	PolicyDelegatedAuthority   = "delegated_authority"
	reviewPolicyKey            = "review_policy"
)

type ReviewPolicy struct {
	Mode           string   `json:"mode"`
	Guardrails     []string `json:"guardrails,omitempty"`
	SummaryCadence string   `json:"summary_cadence,omitempty"`
	Source         string   `json:"source,omitempty"`
}

func (p ReviewPolicy) IsConfigured() bool {
	return normalizePolicyMode(p.Mode) != ""
}

func (p ReviewPolicy) AllowsAutonomousContinuation() bool {
	switch normalizePolicyMode(p.Mode) {
	case PolicyHumanReviewPreferred, PolicyDelegatedAuthority:
		return true
	default:
		return false
	}
}

func ParseReviewPolicy(raw json.RawMessage) (ReviewPolicy, bool) {
	if len(raw) == 0 {
		return ReviewPolicy{}, false
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ReviewPolicy{}, false
	}

	return ParseReviewPolicyValue(payload[reviewPolicyKey])
}

func ParseReviewPolicyValue(value any) (ReviewPolicy, bool) {
	parsed, ok := value.(map[string]any)
	if !ok {
		return ReviewPolicy{}, false
	}

	mode := normalizePolicyMode(readPolicyString(parsed["mode"]))
	if mode == "" {
		return ReviewPolicy{}, false
	}

	policy := ReviewPolicy{
		Mode:           mode,
		Guardrails:     readPolicyStringSlice(parsed["guardrails"]),
		SummaryCadence: strings.TrimSpace(readPolicyString(parsed["summary_cadence"])),
	}
	return policy, true
}

func ResolveReviewPolicy(projectSettings, taskMetadata json.RawMessage) ReviewPolicy {
	// Workstreams are not a first-class persisted entity yet, so durable planner
	// policy resolves from project defaults with explicit task-level overrides.
	if policy, ok := ParseReviewPolicy(taskMetadata); ok {
		policy.Source = "task"
		return policy
	}
	if policy, ok := ParseReviewPolicy(projectSettings); ok {
		policy.Source = "project"
		return policy
	}
	return ReviewPolicy{}
}

func ApplyReviewPolicy(existing json.RawMessage, policy ReviewPolicy) json.RawMessage {
	payload := parsePolicyEnvelope(existing)
	policy = normalizeReviewPolicy(policy)
	if !policy.IsConfigured() {
		delete(payload, reviewPolicyKey)
		return marshalPolicyEnvelope(payload, existing)
	}

	policyPayload := map[string]any{
		"mode": policy.Mode,
	}
	if len(policy.Guardrails) > 0 {
		policyPayload["guardrails"] = append([]string(nil), policy.Guardrails...)
	}
	if policy.SummaryCadence != "" {
		policyPayload["summary_cadence"] = policy.SummaryCadence
	}

	payload[reviewPolicyKey] = policyPayload
	return marshalPolicyEnvelope(payload, existing)
}

func ClearReviewPolicy(existing json.RawMessage) json.RawMessage {
	payload := parsePolicyEnvelope(existing)
	delete(payload, reviewPolicyKey)
	return marshalPolicyEnvelope(payload, existing)
}

func normalizeReviewPolicy(policy ReviewPolicy) ReviewPolicy {
	policy.Mode = normalizePolicyMode(policy.Mode)
	policy.SummaryCadence = strings.TrimSpace(policy.SummaryCadence)
	policy.Guardrails = normalizeGuardrails(policy.Guardrails)
	policy.Source = strings.TrimSpace(policy.Source)
	return policy
}

func normalizePolicyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case PolicyHumanReviewRequired, "required", "human_review":
		return PolicyHumanReviewRequired
	case PolicyHumanReviewPreferred, "preferred", "review_preferred":
		return PolicyHumanReviewPreferred
	case PolicyDelegatedAuthority, "delegated", "delegated_authority_within_guardrails":
		return PolicyDelegatedAuthority
	default:
		return ""
	}
}

func normalizeGuardrails(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func parsePolicyEnvelope(raw json.RawMessage) map[string]any {
	payload := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload
}

func marshalPolicyEnvelope(payload map[string]any, fallback json.RawMessage) json.RawMessage {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizeJSON(fallback)
	}
	return normalizeJSON(encoded)
}

func readPolicyString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func readPolicyStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return normalizeGuardrails(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return normalizeGuardrails(out)
	default:
		return nil
	}
}
