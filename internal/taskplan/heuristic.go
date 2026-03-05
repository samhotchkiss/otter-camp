package taskplan

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	ModeExecutionFirst       = "execution_first"
	ModeReviewAndRefinement  = "review_and_refinement"
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
	Mode                string       `json:"mode"`
	Subjective          bool         `json:"subjective"`
	Comparative         bool         `json:"comparative"`
	MultiOption         bool         `json:"multi_option"`
	PlannedStages       []string     `json:"planned_stages,omitempty"`
	DefaultTemplateSlug string       `json:"default_template_slug,omitempty"`
	ReviewPacket        ReviewPacket `json:"review_packet,omitempty"`
}

func (p Plan) RequiresReviewAndRefinement() bool {
	return p.Mode == ModeReviewAndRefinement
}

func Analyze(title string, description *string) Plan {
	text := normalizeText(title, description)
	if text == "" {
		return Plan{Mode: ModeExecutionFirst}
	}

	multiOption := multiOptionCountPattern.MatchString(text) || containsAny(text, multiOptionSignals)
	subjective := containsAny(text, subjectiveSignals)
	comparative := containsAny(text, comparativeSignals)
	verifiable := leadingQuestionPattern.MatchString(strings.TrimSpace(text)) || containsAny(text, verifiableSignals)

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
		return Plan{
			Mode:                ModeReviewAndRefinement,
			Subjective:          subjective,
			Comparative:         comparative,
			MultiOption:         multiOption,
			PlannedStages:       []string{"generation", "internal_review", "human_review"},
			DefaultTemplateSlug: ReviewRefinementTemplate,
			ReviewPacket: ReviewPacket{
				Summary:  reviewPacketSummary,
				Sections: append([]string(nil), reviewPacketSections...),
			},
		}
	}

	return Plan{
		Mode:        ModeExecutionFirst,
		Subjective:  subjective,
		Comparative: comparative,
		MultiOption: multiOption,
	}
}

func ApplyMetadata(existing json.RawMessage, plan Plan) json.RawMessage {
	if !plan.RequiresReviewAndRefinement() {
		return normalizeJSON(existing)
	}

	payload := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}

	payload[metadataKeyPlanning] = map[string]any{
		"mode":                  plan.Mode,
		"subjective":            plan.Subjective,
		"comparative":           plan.Comparative,
		"multi_option":          plan.MultiOption,
		"planned_stages":        append([]string(nil), plan.PlannedStages...),
		"default_template_slug": plan.DefaultTemplateSlug,
		"review_packet": map[string]any{
			"summary":  plan.ReviewPacket.Summary,
			"sections": append([]string(nil), plan.ReviewPacket.Sections...),
		},
	}

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
	if mode == "" {
		return Plan{}, false
	}

	plan := Plan{
		Mode:                mode,
		Subjective:          readBool(rawPlanning["subjective"]),
		Comparative:         readBool(rawPlanning["comparative"]),
		MultiOption:         readBool(rawPlanning["multi_option"]),
		DefaultTemplateSlug: strings.TrimSpace(readString(rawPlanning["default_template_slug"])),
		PlannedStages:       readStringSlice(rawPlanning["planned_stages"]),
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
