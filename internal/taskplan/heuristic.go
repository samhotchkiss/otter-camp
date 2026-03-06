package taskplan

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	ModeExecutionFirst       = "execution_first"
	ModeReviewAndRefinement  = "review_and_refinement"
	ModeAutonomousInternal   = "autonomous_internal_review"
	InternalReviewTemplate   = "default-review"
	ReviewRefinementTemplate = "default-review-refinement"
	metadataKeyPlanning      = "planning"
	reviewPacketSummary      = "Package generated options with internal critique before human review."
)

var (
	multiOptionCountPattern = regexp.MustCompile(`\b\d+\s+(?:[a-z]+\s+)?(?:options|ideas|directions|concepts|names|taglines|themes|approaches|alternatives|variants)\b`)
	leadingQuestionPattern  = regexp.MustCompile(`^(does|is|are|did|has|have|can|will|should)\b`)

	multiOptionSignals = []string{
		"options",
		"ideas",
		"directions",
		"concepts",
		"names",
		"taglines",
		"themes",
		"approaches",
		"alternatives",
		"variants",
		"brainstorm",
	}
	subjectiveSignals = []string{
		"design",
		"brand",
		"branding",
		"homepage",
		"landing page",
		"blog post",
		"naming",
		"name ideas",
		"content strategy",
		"creative",
		"tone",
		"style",
		"concept",
		"direction",
		"copy",
		"tagline",
		"slogan",
	}
	comparativeSignals = []string{
		"compare",
		"comparison",
		"shortlist",
		"recommend",
		"recommendation",
		"tradeoff",
		"tradeoffs",
		"pros and cons",
		"best option",
		"choose",
	}
	verifiableSignals = []string{
		"verify",
		"validate",
		"confirm",
		"check whether",
		"bug",
		"fix",
		"implement",
		"migrate",
		"endpoint",
		"api",
		"schema",
		"regression",
		"test",
		"compliance",
	}
	reviewPacketSections = []string{
		"options",
		"comparison",
		"shortlist",
		"recommendation",
		"tradeoffs",
		"rationale",
	}
)

type ReviewPacket struct {
	Summary  string   `json:"summary"`
	Sections []string `json:"sections"`
}

type Plan struct {
	Mode                string             `json:"mode"`
	Playbook            string             `json:"playbook,omitempty"`
	WorkType            string             `json:"work_type,omitempty"`
	ProjectStage        string             `json:"project_stage,omitempty"`
	EvidenceMaturity    string             `json:"evidence_maturity,omitempty"`
	RiskLevel           string             `json:"risk_level,omitempty"`
	ProcessEnforced     bool               `json:"process_enforced,omitempty"`
	Subjective          bool               `json:"subjective"`
	Comparative         bool               `json:"comparative"`
	MultiOption         bool               `json:"multi_option"`
	ReviewPolicyMode    string             `json:"review_policy_mode,omitempty"`
	Guardrails          []string           `json:"guardrails,omitempty"`
	SummaryCadence      string             `json:"summary_cadence,omitempty"`
	PlannedStages       []string           `json:"planned_stages,omitempty"`
	DefaultTemplateSlug string             `json:"default_template_slug,omitempty"`
	Artifacts           []PlannedArtifact  `json:"artifacts,omitempty"`
	ArtifactEvidence    []ArtifactEvidence `json:"artifact_evidence,omitempty"`
	FollowOnSuggestions []string           `json:"follow_on_suggestions,omitempty"`
	ReviewPacket        ReviewPacket       `json:"review_packet,omitempty"`
	Override            *PlanningOverride  `json:"override,omitempty"`
}

func (p Plan) RequiresReviewAndRefinement() bool {
	return p.Mode == ModeReviewAndRefinement
}

func (p Plan) RequiresPlanningFlow() bool {
	return p.Mode != "" && p.Mode != ModeExecutionFirst
}

func (p Plan) HasSelection() bool {
	return strings.TrimSpace(p.Playbook) != ""
}

func Analyze(title string, description *string) Plan {
	return AnalyzeWithPolicy(title, description, ReviewPolicy{})
}

func AnalyzeWithPolicy(title string, description *string, policy ReviewPolicy) Plan {
	text := normalizeText(title, description)
	playbook, workType, projectStage, evidenceMaturity, riskLevel := selectPlaybookContext(text)

	multiOption := multiOptionCountPattern.MatchString(text) || containsAny(text, multiOptionSignals)
	subjective := containsAny(text, subjectiveSignals)
	comparative := containsAny(text, comparativeSignals)
	verifiable := leadingQuestionPattern.MatchString(strings.TrimSpace(text)) || containsAny(text, verifiableSignals)

	plan := Plan{
		Mode:                ModeExecutionFirst,
		Playbook:            playbook,
		WorkType:            workType,
		ProjectStage:        projectStage,
		EvidenceMaturity:    evidenceMaturity,
		RiskLevel:           riskLevel,
		ProcessEnforced:     playbookRequiresContract(playbook, text),
		Subjective:          subjective,
		Comparative:         comparative,
		MultiOption:         multiOption,
		Artifacts:           playbookArtifacts(playbook),
		FollowOnSuggestions: playbookFollowOnSuggestions(playbook, projectStage, evidenceMaturity, riskLevel),
	}

	if text == "" {
		return plan
	}

	reviewScore := 0
	if multiOption {
		reviewScore += 2
	}
	if subjective {
		reviewScore++
	}
	if comparative {
		reviewScore++
	}
	if verifiable {
		reviewScore -= 2
	}

	if reviewScore >= 2 && (multiOption || subjective) {
		policy = normalizeReviewPolicy(policy)
		if policy.AllowsAutonomousContinuation() {
			plan.Mode = ModeAutonomousInternal
			plan.ReviewPolicyMode = policy.Mode
			plan.Guardrails = append([]string(nil), policy.Guardrails...)
			plan.SummaryCadence = policy.SummaryCadence
			plan.PlannedStages = []string{"generation", "internal_review", "autonomous_delivery"}
			plan.DefaultTemplateSlug = InternalReviewTemplate
			if policy.Mode == PolicyHumanReviewPreferred {
				plan.PlannedStages = []string{"generation", "internal_review", "periodic_review_summary"}
				if plan.SummaryCadence == "" {
					plan.SummaryCadence = "weekly"
				}
			}
			return plan
		}

		plan.Mode = ModeReviewAndRefinement
		plan.ReviewPolicyMode = PolicyHumanReviewRequired
		plan.PlannedStages = []string{"generation", "internal_review", "human_review"}
		plan.DefaultTemplateSlug = ReviewRefinementTemplate
		plan.ReviewPacket = ReviewPacket{
			Summary:  reviewPacketSummary,
			Sections: append([]string(nil), reviewPacketSections...),
		}
		return plan
	}

	return plan
}

func ApplyMetadata(existing json.RawMessage, plan Plan) json.RawMessage {
	if !plan.HasSelection() {
		return normalizeJSON(existing)
	}

	payload := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}

	planningPayload := map[string]any{}
	if existingPlanning, ok := payload[metadataKeyPlanning].(map[string]any); ok {
		for key, value := range existingPlanning {
			planningPayload[key] = value
		}
	}

	planningPayload["mode"] = plan.Mode
	planningPayload["playbook"] = plan.Playbook
	planningPayload["work_type"] = plan.WorkType
	planningPayload["project_stage"] = plan.ProjectStage
	planningPayload["evidence_maturity"] = plan.EvidenceMaturity
	planningPayload["risk_level"] = plan.RiskLevel
	planningPayload["process_enforced"] = plan.ProcessEnforced
	planningPayload["subjective"] = plan.Subjective
	planningPayload["comparative"] = plan.Comparative
	planningPayload["multi_option"] = plan.MultiOption
	planningPayload["review_policy_mode"] = plan.ReviewPolicyMode
	planningPayload["guardrails"] = append([]string(nil), plan.Guardrails...)
	planningPayload["summary_cadence"] = plan.SummaryCadence
	planningPayload["planned_stages"] = append([]string(nil), plan.PlannedStages...)
	planningPayload["default_template_slug"] = plan.DefaultTemplateSlug
	planningPayload["artifacts"] = append([]PlannedArtifact(nil), plan.Artifacts...)
	planningPayload["follow_on_suggestions"] = append([]string(nil), plan.FollowOnSuggestions...)
	planningPayload["review_packet"] = map[string]any{
		"summary":  plan.ReviewPacket.Summary,
		"sections": append([]string(nil), plan.ReviewPacket.Sections...),
	}
	payload[metadataKeyPlanning] = planningPayload

	encoded, err := json.Marshal(payload)
	if err != nil {
		return normalizeJSON(existing)
	}
	return normalizeJSON(encoded)
}

func Parse(metadata json.RawMessage) (Plan, bool) {
	if len(metadata) == 0 {
		return Plan{}, false
	}

	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return Plan{}, false
	}

	rawPlanning, ok := payload[metadataKeyPlanning].(map[string]any)
	if !ok {
		return Plan{}, false
	}

	mode, _ := rawPlanning["mode"].(string)
	mode = strings.TrimSpace(mode)
	playbook := strings.TrimSpace(readString(rawPlanning["playbook"]))
	if mode == "" && playbook == "" {
		return Plan{}, false
	}

	plan := Plan{
		Mode:                mode,
		Playbook:            playbook,
		WorkType:            strings.TrimSpace(readString(rawPlanning["work_type"])),
		ProjectStage:        strings.TrimSpace(readString(rawPlanning["project_stage"])),
		EvidenceMaturity:    strings.TrimSpace(readString(rawPlanning["evidence_maturity"])),
		RiskLevel:           strings.TrimSpace(readString(rawPlanning["risk_level"])),
		ProcessEnforced:     readBool(rawPlanning["process_enforced"]),
		Subjective:          readBool(rawPlanning["subjective"]),
		Comparative:         readBool(rawPlanning["comparative"]),
		MultiOption:         readBool(rawPlanning["multi_option"]),
		ReviewPolicyMode:    strings.TrimSpace(readString(rawPlanning["review_policy_mode"])),
		Guardrails:          readStringSlice(rawPlanning["guardrails"]),
		SummaryCadence:      strings.TrimSpace(readString(rawPlanning["summary_cadence"])),
		DefaultTemplateSlug: strings.TrimSpace(readString(rawPlanning["default_template_slug"])),
		PlannedStages:       readStringSlice(rawPlanning["planned_stages"]),
		Artifacts:           readPlannedArtifacts(rawPlanning["artifacts"]),
		ArtifactEvidence:    readArtifactEvidence(rawPlanning["artifact_evidence"]),
		FollowOnSuggestions: readStringSlice(rawPlanning["follow_on_suggestions"]),
		Override:            readPlanningOverride(rawPlanning["override"]),
	}

	if rawPacket, ok := rawPlanning["review_packet"].(map[string]any); ok {
		plan.ReviewPacket = ReviewPacket{
			Summary:  strings.TrimSpace(readString(rawPacket["summary"])),
			Sections: readStringSlice(rawPacket["sections"]),
		}
	}

	return plan, true
}

func containsAny(text string, signals []string) bool {
	for _, signal := range signals {
		if strings.Contains(text, signal) {
			return true
		}
	}
	return false
}

func normalizeText(title string, description *string) string {
	var builder strings.Builder
	builder.WriteString(strings.ToLower(strings.TrimSpace(title)))
	if description != nil && strings.TrimSpace(*description) != "" {
		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(strings.ToLower(strings.TrimSpace(*description)))
	}
	return strings.TrimSpace(builder.String())
}

func normalizeJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(`{}`)
	}
	return value
}

func readBool(value any) bool {
	flag, ok := value.(bool)
	return ok && flag
}

func readString(value any) string {
	text, _ := value.(string)
	return text
}

func readStringSlice(value any) []string {
	raw, ok := value.([]any)
	if ok {
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	}

	typed, ok := value.([]string)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(typed))
	for _, item := range typed {
		if strings.TrimSpace(item) != "" {
			out = append(out, strings.TrimSpace(item))
		}
	}
	return out
}

func readPlannedArtifacts(value any) []PlannedArtifact {
	raw, ok := value.([]any)
	if !ok {
		typed, typedOK := value.([]PlannedArtifact)
		if !typedOK {
			return nil
		}
		return append([]PlannedArtifact(nil), typed...)
	}

	artifacts := make([]PlannedArtifact, 0, len(raw))
	for _, item := range raw {
		artifactMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		artifact := PlannedArtifact{
			Slug:  strings.TrimSpace(readString(artifactMap["slug"])),
			Title: strings.TrimSpace(readString(artifactMap["title"])),
		}
		if artifact.Slug == "" && artifact.Title == "" {
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts
}
