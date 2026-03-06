package taskplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	ProcessStatusPending    = "pending"
	ProcessStatusFollowed   = "followed"
	ProcessStatusOverridden = "overridden"
)

var (
	ErrPlanningStateRequired              = errors.New("planning playbook state is required before planning artifacts can be recorded")
	ErrPlanningArtifactContractIncomplete = errors.New("planning artifact contract is incomplete")
	ErrPlanningOverrideNotNeeded          = errors.New("planning override reason requires missing planning contract items")
)

type ArtifactContract struct {
	Slug             string   `json:"slug"`
	Title            string   `json:"title"`
	RequiredSections []string `json:"required_sections,omitempty"`
}

type ArtifactEvidence struct {
	Slug      string   `json:"slug"`
	Title     string   `json:"title,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Sections  []string `json:"sections,omitempty"`
	AssetRefs []string `json:"asset_refs,omitempty"`
	Notes     string   `json:"notes,omitempty"`
}

type PlanningOverride struct {
	Reason              string   `json:"reason"`
	SkippedRequirements []string `json:"skipped_requirements,omitempty"`
	RecordedAt          string   `json:"recorded_at,omitempty"`
	RecordedByType      string   `json:"recorded_by_type,omitempty"`
	RecordedByID        string   `json:"recorded_by_id,omitempty"`
}

type ValidationIssue struct {
	ArtifactSlug    string   `json:"artifact_slug"`
	ArtifactTitle   string   `json:"artifact_title"`
	MissingArtifact bool     `json:"missing_artifact,omitempty"`
	MissingSummary  bool     `json:"missing_summary,omitempty"`
	MissingSections []string `json:"missing_sections,omitempty"`
}

type ValidationReport struct {
	Enforced         bool               `json:"enforced"`
	Playbook         string             `json:"playbook,omitempty"`
	ProcessStatus    string             `json:"process_status,omitempty"`
	ArtifactContract []ArtifactContract `json:"artifact_contract,omitempty"`
	ReviewChecklist  []string           `json:"review_checklist,omitempty"`
	Issues           []ValidationIssue  `json:"issues,omitempty"`
	Override         *PlanningOverride  `json:"override,omitempty"`
}

type ProcessUpdate struct {
	Artifacts          []ArtifactEvidence
	HasArtifactChanges bool
	OverrideReason     *string
	FollowOnStopReason *string
	ActorType          string
	ActorID            uuid.UUID
	RecordedAt         time.Time
}

type ContractError struct {
	Report ValidationReport
}

func (e ContractError) Error() string {
	missing := e.Report.MissingRequirements()
	if len(missing) == 0 {
		return ErrPlanningArtifactContractIncomplete.Error()
	}
	return fmt.Sprintf("%s: %s", ErrPlanningArtifactContractIncomplete.Error(), strings.Join(missing, "; "))
}

func (e ContractError) Is(target error) bool {
	return target == ErrPlanningArtifactContractIncomplete
}

var playbookArtifactContracts = map[string][]ArtifactContract{
	PlaybookDiscovery: {
		{Slug: "problem-brief", Title: "Problem brief", RequiredSections: []string{"problem", "target user", "evidence gaps"}},
		{Slug: "research-plan", Title: "Research plan", RequiredSections: []string{"objectives", "methods", "participants"}},
		{Slug: "assumption-log", Title: "Assumption log", RequiredSections: []string{"assumptions", "risks", "open questions"}},
		{Slug: "validation-plan", Title: "Validation plan", RequiredSections: []string{"ideas explored", "assumptions", "validation experiments", "decision framework"}},
	},
	PlaybookStrategy: {
		{Slug: "strategy-brief", Title: "Strategy brief", RequiredSections: []string{"goal", "target segments", "not serving", "core capabilities"}},
		{Slug: "tradeoff-matrix", Title: "Tradeoff matrix", RequiredSections: []string{"options", "tradeoffs", "decision"}},
		{Slug: "decision-log", Title: "Decision log", RequiredSections: []string{"decision", "rationale", "owner"}},
		{Slug: "success-narrative", Title: "Success narrative", RequiredSections: []string{"key metrics", "defensibility", "milestones", "risks"}},
	},
	PlaybookExecutionSpec: {
		{Slug: "prd", Title: "PRD / requirements spec", RequiredSections: []string{"goals", "non-goals", "scope", "constraints", "success metrics", "open questions"}},
		{Slug: "implementation-plan", Title: "Implementation plan", RequiredSections: []string{"milestones", "phasing", "owners", "rollout"}},
		{Slug: "acceptance-criteria", Title: "Acceptance criteria", RequiredSections: []string{"scenarios", "edge cases", "verification"}},
		{Slug: "dependency-log", Title: "Dependency log", RequiredSections: []string{"dependencies", "risks", "mitigations"}},
	},
	PlaybookBacklogDecomposition: {
		{Slug: "epic-breakdown", Title: "Epic breakdown", RequiredSections: []string{"themes", "goals", "scope slices", "technical notes", "open questions"}},
		{Slug: "story-cards", Title: "Story cards", RequiredSections: []string{"stories", "acceptance criteria", "owners", "technical notes", "open questions"}},
		{Slug: "sequencing-plan", Title: "Sequencing plan", RequiredSections: []string{"order", "dependencies", "design input", "technical spikes"}},
		{Slug: "definition-of-done", Title: "Definition of done", RequiredSections: []string{"exit criteria", "verification", "release gates"}},
	},
	PlaybookRiskReadiness: {
		{Slug: "risk-register", Title: "Risk register", RequiredSections: []string{"major risks", "severity", "impact"}},
		{Slug: "premortem", Title: "Pre-mortem", RequiredSections: []string{"failure modes", "triggers", "responses"}},
		{Slug: "mitigation-plan", Title: "Mitigation plan", RequiredSections: []string{"mitigations", "owners", "dates"}},
		{Slug: "readiness-checklist", Title: "Readiness checklist", RequiredSections: []string{"go/no-go checklist", "blockers", "rollback"}},
	},
	PlaybookMetrics: {
		{Slug: "metric-tree", Title: "Metric tree", RequiredSections: []string{"north star", "input metrics", "health metrics", "counter metrics"}},
		{Slug: "instrumentation-plan", Title: "Instrumentation plan", RequiredSections: []string{"events", "owners", "qa"}},
		{Slug: "dashboard-spec", Title: "Dashboard spec", RequiredSections: []string{"views", "slices", "alerts"}},
		{Slug: "review-cadence", Title: "Metric review cadence", RequiredSections: []string{"schedule", "owners", "thresholds"}},
	},
	PlaybookGTMLaunch: {
		{Slug: "launch-brief", Title: "Launch brief", RequiredSections: []string{"launch scope", "beachhead segment", "icp", "success metrics"}},
		{Slug: "audience-messaging", Title: "Audience and messaging brief", RequiredSections: []string{"positioning", "messaging", "proof"}},
		{Slug: "channel-plan", Title: "Channel plan", RequiredSections: []string{"channel strategy", "owners", "launch timeline"}},
		{Slug: "launch-checklist", Title: "Launch checklist", RequiredSections: []string{"readiness", "approvals", "contingency", "expansion plan"}},
	},
}

var playbookReviewChecklists = map[string][]string{
	PlaybookDiscovery: {
		"Verify the problem, target user, and evidence gaps are explicit before approving downstream planning.",
		"Check that the research plan defines objectives, methods, and participant coverage.",
		"Confirm assumptions and validation criteria are concrete enough to support a go/no-go decision.",
	},
	PlaybookStrategy: {
		"Verify the strategy brief names the goal, target segments, exclusions, and core capabilities.",
		"Check that tradeoffs, decision rationale, key metrics, and ownership are explicit rather than implied.",
		"Confirm defensibility is named (or explicitly absent) and thin-context claims are marked as hypotheses or open questions.",
	},
	PlaybookExecutionSpec: {
		"Verify the spec defines goals, non-goals, scope, constraints, and success metrics clearly enough to implement.",
		"Check that open questions, phasing, owners, and rollout sequencing are explicit before delivery work starts.",
		"Confirm acceptance criteria and dependency mitigations are testable, and that unknowns are called out instead of invented.",
	},
	PlaybookBacklogDecomposition: {
		"Verify the epic breakdown and selected story format preserve the parent intent without hidden scope drift.",
		"Check that sequencing, dependency order, design input, and technical spikes would let execution begin without re-planning.",
		"Confirm every backlog item has testable acceptance criteria plus technical notes or open questions where they materially affect delivery.",
	},
	PlaybookRiskReadiness: {
		"Verify the risk register names the major risks with explicit severity and impact classification.",
		"Check that the pre-mortem and mitigation plan describe realistic triggers, responses, and named owners.",
		"Confirm the readiness checklist includes a go/no-go checklist, blockers, and rollback expectations.",
	},
	PlaybookMetrics: {
		"Verify the metric tree names the north star plus explicit input, health, and counter-metrics.",
		"Check that instrumentation ownership, dashboards, and QA are explicit before launch or experiment review.",
		"Confirm thresholds and review cadence support real operating decisions instead of passive reporting.",
	},
	PlaybookGTMLaunch: {
		"Verify the launch brief defines scope, beachhead segment, ICP, and success metrics for the rollout.",
		"Check that positioning, messaging proof points, channel strategy, and launch timeline are explicit, not implied.",
		"Confirm the launch checklist covers approvals, contingencies, readiness gates, and the post-beachhead expansion plan.",
	},
}

func ArtifactContractForPlaybook(playbook string) []ArtifactContract {
	contracts := playbookArtifactContracts[strings.TrimSpace(playbook)]
	if len(contracts) == 0 {
		return nil
	}
	out := make([]ArtifactContract, 0, len(contracts))
	for _, contract := range contracts {
		out = append(out, ArtifactContract{
			Slug:             contract.Slug,
			Title:            contract.Title,
			RequiredSections: append([]string(nil), contract.RequiredSections...),
		})
	}
	return out
}

func ArtifactContractForPlan(plan Plan) []ArtifactContract {
	contracts := ArtifactContractForPlaybook(plan.Playbook)
	if len(contracts) == 0 {
		return nil
	}

	if strings.TrimSpace(plan.Playbook) == PlaybookDiscovery {
		mode := NormalizeDiscoveryMode(plan.DiscoveryMode)
		if mode != "" {
			for i := range contracts {
				if contracts[i].Slug == "validation-plan" {
					contracts[i].RequiredSections = DiscoveryValidationPlanSections(mode)
				}
			}
		}
	}

	if strings.TrimSpace(plan.Playbook) == PlaybookBacklogDecomposition {
		if sections := BacklogStoryCardSections(plan.BacklogFormat); len(sections) > 0 {
			contracts = replaceContractSections(contracts, "story-cards", sections)
		}
	}

	if requiresHypothesisLabels(plan) {
		contracts = appendContractSections(contracts, "strategy-brief", "hypotheses")
		contracts = appendContractSections(contracts, "decision-log", "open questions")
	}

	return contracts
}

func ReviewChecklistForPlaybook(playbook string) []string {
	checklist := playbookReviewChecklists[strings.TrimSpace(playbook)]
	if len(checklist) == 0 {
		return nil
	}
	return append([]string(nil), checklist...)
}

func ReviewChecklistForPlan(plan Plan) []string {
	if strings.TrimSpace(plan.Playbook) == PlaybookDiscovery {
		switch NormalizeDiscoveryMode(plan.DiscoveryMode) {
		case DiscoveryModeExistingProduct:
			return []string{
				"Verify the discovery mode is explicit and grounded in the current product surface, target user, and evidence gaps.",
				"Check that prior feedback, instrumentation baseline, and observed-usage experiments are explicit before approving downstream planning.",
				"Confirm the validation plan names ideas explored, assumptions, validation experiments, and a clear decision framework.",
			}
		default:
			return []string{
				"Verify the discovery mode is explicit and grounded in a new-product or unvalidated-concept framing.",
				"Check that low-cost tests and desirability signals are explicit before approving downstream planning.",
				"Confirm the validation plan names ideas explored, assumptions, validation experiments, and a clear decision framework.",
			}
		}
	}

	checklist := ReviewChecklistForPlaybook(plan.Playbook)
	if len(checklist) == 0 {
		return nil
	}
	if requiresHypothesisLabels(plan) {
		checklist = append(checklist, "Ensure low-confidence statements are labeled as hypotheses or open questions before approving the strategy.")
	}
	return checklist
}

func ApplyProcessUpdate(existing json.RawMessage, update ProcessUpdate) (json.RawMessage, Plan, ValidationReport, error) {
	if !update.HasArtifactChanges && update.OverrideReason == nil && update.FollowOnStopReason == nil {
		plan, ok := Parse(existing)
		if !ok {
			return normalizeJSON(existing), Plan{}, ValidationReport{}, nil
		}
		return normalizeJSON(existing), plan, Evaluate(plan), nil
	}

	payload := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}

	rawPlanning, ok := payload[metadataKeyPlanning].(map[string]any)
	if !ok {
		return nil, Plan{}, ValidationReport{}, ErrPlanningStateRequired
	}

	if update.HasArtifactChanges {
		current := readArtifactEvidence(rawPlanning["artifact_evidence"])
		next := mergeArtifactEvidence(current, update.Artifacts)
		rawPlanning["artifact_evidence"] = serializeArtifactEvidence(next)
	}
	if update.FollowOnStopReason != nil {
		reason := strings.TrimSpace(*update.FollowOnStopReason)
		if reason == "" {
			delete(rawPlanning, "follow_on_stop_reason")
		} else {
			rawPlanning["follow_on_stop_reason"] = reason
		}
	}
	payload[metadataKeyPlanning] = rawPlanning

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, Plan{}, ValidationReport{}, err
	}
	plan, ok := Parse(encoded)
	if !ok {
		return nil, Plan{}, ValidationReport{}, ErrPlanningStateRequired
	}
	report := Evaluate(plan)

	if update.OverrideReason != nil {
		reason := strings.TrimSpace(*update.OverrideReason)
		if reason == "" {
			delete(rawPlanning, "override")
		} else {
			if !report.Enforced {
				return nil, Plan{}, ValidationReport{}, ErrPlanningStateRequired
			}
			if !report.HasMissingRequirements() {
				return nil, Plan{}, ValidationReport{}, ErrPlanningOverrideNotNeeded
			}
			override := PlanningOverride{
				Reason:              reason,
				SkippedRequirements: report.MissingRequirements(),
				RecordedAt:          update.RecordedAt.UTC().Format(time.RFC3339),
				RecordedByType:      strings.TrimSpace(update.ActorType),
			}
			if update.ActorID != uuid.Nil {
				override.RecordedByID = update.ActorID.String()
			}
			rawPlanning["override"] = map[string]any{
				"reason":               override.Reason,
				"skipped_requirements": append([]string(nil), override.SkippedRequirements...),
				"recorded_at":          override.RecordedAt,
				"recorded_by_type":     override.RecordedByType,
				"recorded_by_id":       override.RecordedByID,
			}
		}
		payload[metadataKeyPlanning] = rawPlanning
		encoded, err = json.Marshal(payload)
		if err != nil {
			return nil, Plan{}, ValidationReport{}, err
		}
		plan, ok = Parse(encoded)
		if !ok {
			return nil, Plan{}, ValidationReport{}, ErrPlanningStateRequired
		}
		report = Evaluate(plan)
	}

	return normalizeJSON(encoded), plan, report, nil
}

func CompletionReport(metadata json.RawMessage) (ValidationReport, error) {
	plan, ok := Parse(metadata)
	if !ok {
		return ValidationReport{}, nil
	}
	report := Evaluate(plan)
	if report.CanComplete() {
		return report, nil
	}
	return report, ContractError{Report: report}
}

func Evaluate(plan Plan) ValidationReport {
	report := ValidationReport{
		Playbook:         strings.TrimSpace(plan.Playbook),
		ArtifactContract: ArtifactContractForPlan(plan),
		ReviewChecklist:  ReviewChecklistForPlan(plan),
		Override:         clonePlanningOverride(plan.Override),
	}
	report.Enforced = plan.ProcessEnforced && len(report.ArtifactContract) > 0
	if !report.Enforced {
		return report
	}

	evidenceBySlug := make(map[string]ArtifactEvidence, len(plan.ArtifactEvidence))
	for _, evidence := range plan.ArtifactEvidence {
		normalized := normalizeArtifactEvidence(evidence)
		if normalized.Slug == "" {
			continue
		}
		evidenceBySlug[normalized.Slug] = normalized
	}

	for _, contract := range report.ArtifactContract {
		evidence, ok := evidenceBySlug[normalizeArtifactSlug(contract.Slug)]
		issue := ValidationIssue{
			ArtifactSlug:  contract.Slug,
			ArtifactTitle: contract.Title,
		}
		if !ok {
			issue.MissingArtifact = true
			report.Issues = append(report.Issues, issue)
			continue
		}
		if strings.TrimSpace(evidence.Summary) == "" {
			issue.MissingSummary = true
		}
		sectionSet := make(map[string]struct{}, len(evidence.Sections))
		for _, section := range evidence.Sections {
			sectionSet[normalizeContractKey(section)] = struct{}{}
		}
		for _, required := range contract.RequiredSections {
			if _, present := sectionSet[normalizeContractKey(required)]; !present {
				issue.MissingSections = append(issue.MissingSections, required)
			}
		}
		if issue.MissingSummary || len(issue.MissingSections) > 0 {
			report.Issues = append(report.Issues, issue)
		}
	}

	switch {
	case len(report.Issues) == 0:
		report.ProcessStatus = ProcessStatusFollowed
	case report.Override != nil && strings.TrimSpace(report.Override.Reason) != "":
		report.ProcessStatus = ProcessStatusOverridden
	default:
		report.ProcessStatus = ProcessStatusPending
	}

	return report
}

func (r ValidationReport) HasMissingRequirements() bool {
	return len(r.Issues) > 0
}

func (r ValidationReport) CanComplete() bool {
	if !r.Enforced {
		return true
	}
	return !r.HasMissingRequirements() || r.ProcessStatus == ProcessStatusOverridden
}

func (r ValidationReport) MissingRequirements() []string {
	if len(r.Issues) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Issues))
	for _, issue := range r.Issues {
		out = append(out, issue.Description())
	}
	return out
}

func (r ValidationReport) Payload() map[string]any {
	if !r.Enforced {
		return nil
	}
	payload := map[string]any{
		"planning_playbook":             r.Playbook,
		"planning_process_status":       r.ProcessStatus,
		"planning_missing_requirements": r.MissingRequirements(),
	}
	if len(r.ReviewChecklist) > 0 {
		payload["planning_review_checklist"] = append([]string(nil), r.ReviewChecklist...)
	}
	if r.ProcessStatus == ProcessStatusOverridden && r.Override != nil && strings.TrimSpace(r.Override.Reason) != "" {
		payload["planning_override"] = map[string]any{
			"reason":               r.Override.Reason,
			"skipped_requirements": append([]string(nil), r.Override.SkippedRequirements...),
			"recorded_at":          r.Override.RecordedAt,
			"recorded_by_type":     r.Override.RecordedByType,
			"recorded_by_id":       r.Override.RecordedByID,
		}
		payload["planning_override_reason"] = r.Override.Reason
	}
	return payload
}

func (i ValidationIssue) Description() string {
	label := strings.TrimSpace(i.ArtifactTitle)
	if label == "" {
		label = strings.TrimSpace(i.ArtifactSlug)
	}
	if i.MissingArtifact {
		return label + " missing artifact"
	}
	parts := make([]string, 0, 2)
	if i.MissingSummary {
		parts = append(parts, "summary")
	}
	if len(i.MissingSections) > 0 {
		parts = append(parts, "sections: "+strings.Join(i.MissingSections, ", "))
	}
	if len(parts) == 0 {
		return label
	}
	return label + " missing " + strings.Join(parts, " and ")
}

func normalizeArtifactEvidence(evidence ArtifactEvidence) ArtifactEvidence {
	out := ArtifactEvidence{
		Slug:    normalizeArtifactSlug(firstNonEmpty(evidence.Slug, evidence.Title)),
		Title:   strings.TrimSpace(evidence.Title),
		Summary: strings.TrimSpace(evidence.Summary),
		Notes:   strings.TrimSpace(evidence.Notes),
	}
	out.Sections = normalizeStringList(evidence.Sections)
	out.AssetRefs = normalizeStringList(evidence.AssetRefs)
	return out
}

func appendContractSections(contracts []ArtifactContract, slug string, sections ...string) []ArtifactContract {
	if len(contracts) == 0 || len(sections) == 0 {
		return contracts
	}
	target := normalizeArtifactSlug(slug)
	out := make([]ArtifactContract, 0, len(contracts))
	for _, contract := range contracts {
		if normalizeArtifactSlug(contract.Slug) == target {
			contract.RequiredSections = appendNormalizedSections(contract.RequiredSections, sections...)
		}
		out = append(out, contract)
	}
	return out
}

func replaceContractSections(contracts []ArtifactContract, slug string, sections []string) []ArtifactContract {
	if len(contracts) == 0 {
		return contracts
	}
	target := normalizeArtifactSlug(slug)
	out := make([]ArtifactContract, 0, len(contracts))
	for _, contract := range contracts {
		if normalizeArtifactSlug(contract.Slug) == target {
			contract.RequiredSections = append([]string(nil), sections...)
		}
		out = append(out, contract)
	}
	return out
}

func appendNormalizedSections(existing []string, additions ...string) []string {
	if len(additions) == 0 {
		return append([]string(nil), existing...)
	}
	out := append([]string(nil), existing...)
	seen := make(map[string]struct{}, len(out))
	for _, value := range out {
		seen[normalizeContractKey(value)] = struct{}{}
	}
	for _, value := range additions {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := normalizeContractKey(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func requiresHypothesisLabels(plan Plan) bool {
	return strings.TrimSpace(plan.Playbook) == PlaybookStrategy && strings.TrimSpace(plan.EvidenceMaturity) == EvidenceUnknown
}

func mergeArtifactEvidence(existing, updates []ArtifactEvidence) []ArtifactEvidence {
	if len(updates) == 0 {
		return nil
	}
	merged := make(map[string]ArtifactEvidence, len(existing))
	for _, evidence := range existing {
		normalized := normalizeArtifactEvidence(evidence)
		if normalized.Slug == "" {
			continue
		}
		merged[normalized.Slug] = normalized
	}
	for _, evidence := range updates {
		normalized := normalizeArtifactEvidence(evidence)
		if normalized.Slug == "" {
			continue
		}
		if normalized.Summary == "" && len(normalized.Sections) == 0 && len(normalized.AssetRefs) == 0 && normalized.Notes == "" {
			delete(merged, normalized.Slug)
			continue
		}
		merged[normalized.Slug] = normalized
	}
	return orderedArtifactEvidence(merged)
}

func orderedArtifactEvidence(values map[string]ArtifactEvidence) []ArtifactEvidence {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]ArtifactEvidence, 0, len(keys))
	for _, key := range keys {
		out = append(out, values[key])
	}
	return out
}

func serializeArtifactEvidence(values []ArtifactEvidence) []map[string]any {
	if len(values) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(values))
	for _, evidence := range values {
		normalized := normalizeArtifactEvidence(evidence)
		if normalized.Slug == "" {
			continue
		}
		item := map[string]any{
			"slug": normalized.Slug,
		}
		if normalized.Title != "" {
			item["title"] = normalized.Title
		}
		if normalized.Summary != "" {
			item["summary"] = normalized.Summary
		}
		if len(normalized.Sections) > 0 {
			item["sections"] = append([]string(nil), normalized.Sections...)
		}
		if len(normalized.AssetRefs) > 0 {
			item["asset_refs"] = append([]string(nil), normalized.AssetRefs...)
		}
		if normalized.Notes != "" {
			item["notes"] = normalized.Notes
		}
		out = append(out, item)
	}
	return out
}

func readArtifactEvidence(value any) []ArtifactEvidence {
	raw, ok := value.([]any)
	if !ok {
		typed, typedOK := value.([]ArtifactEvidence)
		if !typedOK {
			return nil
		}
		out := make([]ArtifactEvidence, 0, len(typed))
		for _, evidence := range typed {
			normalized := normalizeArtifactEvidence(evidence)
			if normalized.Slug != "" {
				out = append(out, normalized)
			}
		}
		return out
	}

	out := make([]ArtifactEvidence, 0, len(raw))
	for _, item := range raw {
		evidenceMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		evidence := normalizeArtifactEvidence(ArtifactEvidence{
			Slug:      readString(evidenceMap["slug"]),
			Title:     readString(evidenceMap["title"]),
			Summary:   readString(evidenceMap["summary"]),
			Sections:  readStringSlice(evidenceMap["sections"]),
			AssetRefs: readStringSlice(evidenceMap["asset_refs"]),
			Notes:     readString(evidenceMap["notes"]),
		})
		if evidence.Slug == "" {
			continue
		}
		out = append(out, evidence)
	}
	return out
}

func readPlanningOverride(value any) *PlanningOverride {
	raw, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	reason := strings.TrimSpace(readString(raw["reason"]))
	if reason == "" {
		return nil
	}
	return &PlanningOverride{
		Reason:              reason,
		SkippedRequirements: readStringSlice(raw["skipped_requirements"]),
		RecordedAt:          strings.TrimSpace(readString(raw["recorded_at"])),
		RecordedByType:      strings.TrimSpace(readString(raw["recorded_by_type"])),
		RecordedByID:        strings.TrimSpace(readString(raw["recorded_by_id"])),
	}
}

func clonePlanningOverride(override *PlanningOverride) *PlanningOverride {
	if override == nil {
		return nil
	}
	cloned := *override
	cloned.SkippedRequirements = append([]string(nil), override.SkippedRequirements...)
	return &cloned
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := normalizeContractKey(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func normalizeArtifactSlug(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	for strings.Contains(trimmed, "--") {
		trimmed = strings.ReplaceAll(trimmed, "--", "-")
	}
	return strings.Trim(trimmed, "-")
}

func normalizeContractKey(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	trimmed = strings.ReplaceAll(trimmed, "_", " ")
	trimmed = strings.ReplaceAll(trimmed, "-", " ")
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	return trimmed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
