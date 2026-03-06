package taskplan

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAnalyzeSelectsBaselinePlaybooks(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		playbook    string
	}{
		{
			name:        "discovery",
			title:       "Validate the onboarding problem before writing specs",
			description: "Run customer interviews, capture assumptions, and design a lightweight validation plan for this new product idea.",
			playbook:    PlaybookDiscovery,
		},
		{
			name:        "strategy",
			title:       "Positioning and roadmap for analytics expansion",
			description: "Define the product strategy, positioning tradeoffs, and the roadmap sequence for the analytics platform.",
			playbook:    PlaybookStrategy,
		},
		{
			name:        "execution spec",
			title:       "PRD for billing migration",
			description: "Write the PRD, requirements, implementation plan, and acceptance criteria for the billing migration.",
			playbook:    PlaybookExecutionSpec,
		},
		{
			name:        "backlog decomposition",
			title:       "Break the approved PRD into delivery work",
			description: "Decompose the scope into epics, user stories, sprint-ready tickets, and sequencing constraints.",
			playbook:    PlaybookBacklogDecomposition,
		},
		{
			name:        "risk readiness",
			title:       "Launch readiness pre-mortem",
			description: "Build the pre-mortem, risk register, mitigation plan, and readiness checklist for the regulated rollout.",
			playbook:    PlaybookRiskReadiness,
		},
		{
			name:        "metrics",
			title:       "Metric tree and instrumentation plan",
			description: "Define north-star metrics, KPI instrumentation, the dashboard spec, and weekly metric review cadence.",
			playbook:    PlaybookMetrics,
		},
		{
			name:        "gtm launch",
			title:       "Go-to-market launch plan",
			description: "Create the GTM launch plan, messaging brief, channel plan, and sales enablement checklist for release.",
			playbook:    PlaybookGTMLaunch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Analyze(tc.title, &tc.description)
			if plan.Playbook != tc.playbook {
				t.Fatalf("Playbook = %q, want %q", plan.Playbook, tc.playbook)
			}
			if plan.WorkType == "" || plan.ProjectStage == "" || plan.EvidenceMaturity == "" || plan.RiskLevel == "" {
				t.Fatalf("planning context should be fully populated, got %#v", plan)
			}
			if len(plan.Artifacts) == 0 {
				t.Fatalf("Artifacts = %#v, want non-empty artifact plan", plan.Artifacts)
			}
			if len(plan.FollowOnSuggestions) == 0 {
				t.Fatalf("FollowOnSuggestions = %#v, want non-empty follow-on suggestions", plan.FollowOnSuggestions)
			}
		})
	}
}

func TestAnalyzeDefaultsRiskyInitiativesToRiskReadiness(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
	}{
		{
			name:        "billing migration",
			title:       "Billing migration sign-off plan",
			description: "Prepare the planning package for the billing migration, mitigation plan, and readiness review before we move customer traffic.",
		},
		{
			name:        "auth security change",
			title:       "Authentication hardening rollout",
			description: "Plan the auth and security rollout for the customer-facing login changes, including readiness and mitigation owners.",
		},
		{
			name:        "high visibility launch",
			title:       "Public launch readiness",
			description: "Prepare the risky public launch and high-visibility customer-facing rollout checklist before go live.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Analyze(tc.title, &tc.description)
			if plan.Playbook != PlaybookRiskReadiness {
				t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookRiskReadiness)
			}
		})
	}
}

func TestAnalyzeClassifiesSubjectiveMultiOptionRequestsAsReviewAndRefinement(t *testing.T) {
	description := "Generate 10 homepage design options, compare them, shortlist the best two, and recommend a direction with tradeoffs."

	plan := Analyze("Homepage design options", &description)
	if !plan.RequiresReviewAndRefinement() {
		t.Fatalf("RequiresReviewAndRefinement = false, want true")
	}
	if plan.DefaultTemplateSlug != ReviewRefinementTemplate {
		t.Fatalf("DefaultTemplateSlug = %q, want %q", plan.DefaultTemplateSlug, ReviewRefinementTemplate)
	}
	if len(plan.PlannedStages) != 3 {
		t.Fatalf("planned stages len = %d, want 3", len(plan.PlannedStages))
	}
	if got := plan.PlannedStages[0]; got != "generation" {
		t.Fatalf("planned stage[0] = %q, want generation", got)
	}
	if len(plan.ReviewPacket.Sections) < 4 {
		t.Fatalf("review packet sections len = %d, want >= 4", len(plan.ReviewPacket.Sections))
	}
	if plan.Playbook != PlaybookStrategy {
		t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookStrategy)
	}
}

func TestAnalyzeClassifiesVerifiableRequestsAsExecutionFirst(t *testing.T) {
	description := "Verify whether the webhook retry policy still uses exponential backoff and confirm the current maximum delay."

	plan := Analyze("Does the webhook retry policy still back off exponentially?", &description)
	if plan.RequiresReviewAndRefinement() {
		t.Fatalf("RequiresReviewAndRefinement = true, want false")
	}
	if plan.Mode != ModeExecutionFirst {
		t.Fatalf("Mode = %q, want %q", plan.Mode, ModeExecutionFirst)
	}
	if plan.Playbook != PlaybookExecutionSpec {
		t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookExecutionSpec)
	}
}

func TestAnalyzeDiscoveryModeClassification(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		wantMode    string
	}{
		{
			name:        "new product discovery",
			title:       "Validate the onboarding concept before we spec it",
			description: "Run customer interviews, capture assumptions, and design low-cost validation for this new product idea before committing scope.",
			wantMode:    DiscoveryModeNewProduct,
		},
		{
			name:        "existing product discovery",
			title:       "Investigate checkout drop-off in the existing product",
			description: "Review support tickets, current funnel instrumentation, and usage data for the existing product before defining experiments.",
			wantMode:    DiscoveryModeExistingProduct,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Analyze(tc.title, &tc.description)
			if plan.Playbook != PlaybookDiscovery {
				t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookDiscovery)
			}
			if plan.DiscoveryMode != tc.wantMode {
				t.Fatalf("DiscoveryMode = %q, want %q", plan.DiscoveryMode, tc.wantMode)
			}

			metadata := ApplyMetadata(json.RawMessage(`{}`), plan)
			parsed, ok := Parse(metadata)
			if !ok {
				t.Fatal("Parse(metadata) = false, want true")
			}
			if parsed.DiscoveryMode != tc.wantMode {
				t.Fatalf("parsed DiscoveryMode = %q, want %q", parsed.DiscoveryMode, tc.wantMode)
			}
		})
	}
}

func TestAnalyzeWithDelegatedPolicyChoosesAutonomousInternalReview(t *testing.T) {
	description := "Generate 10 homepage design options, compare them, and recommend a direction that stays on-brand."

	plan := AnalyzeWithPolicy("Homepage design options", &description, ReviewPolicy{
		Mode:       PolicyDelegatedAuthority,
		Guardrails: []string{"Use OtterCamp voice", "Avoid unsupported claims"},
	})
	if plan.Mode != ModeAutonomousInternal {
		t.Fatalf("Mode = %q, want %q", plan.Mode, ModeAutonomousInternal)
	}
	if plan.DefaultTemplateSlug != InternalReviewTemplate {
		t.Fatalf("DefaultTemplateSlug = %q, want %q", plan.DefaultTemplateSlug, InternalReviewTemplate)
	}
	if plan.ReviewPolicyMode != PolicyDelegatedAuthority {
		t.Fatalf("ReviewPolicyMode = %q, want %q", plan.ReviewPolicyMode, PolicyDelegatedAuthority)
	}
	if !reflect.DeepEqual(plan.Guardrails, []string{"Use OtterCamp voice", "Avoid unsupported claims"}) {
		t.Fatalf("Guardrails = %#v, want delegated guardrails", plan.Guardrails)
	}
	if !reflect.DeepEqual(plan.PlannedStages, []string{"generation", "internal_review", "autonomous_delivery"}) {
		t.Fatalf("PlannedStages = %#v, want autonomous internal review stages", plan.PlannedStages)
	}
	if plan.Playbook != PlaybookStrategy {
		t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookStrategy)
	}
}

func TestAnalyzeWithPreferredPolicyAddsPeriodicReviewSummary(t *testing.T) {
	description := "Brainstorm 12 campaign concepts, compare them, and recommend which ones to keep shipping."

	plan := AnalyzeWithPolicy("Campaign concepts", &description, ReviewPolicy{
		Mode: PolicyHumanReviewPreferred,
	})
	if plan.Mode != ModeAutonomousInternal {
		t.Fatalf("Mode = %q, want %q", plan.Mode, ModeAutonomousInternal)
	}
	if plan.SummaryCadence != "weekly" {
		t.Fatalf("SummaryCadence = %q, want weekly", plan.SummaryCadence)
	}
	if !reflect.DeepEqual(plan.PlannedStages, []string{"generation", "internal_review", "periodic_review_summary"}) {
		t.Fatalf("PlannedStages = %#v, want periodic review summary stages", plan.PlannedStages)
	}
}

func TestAnalyzeAmbiguousInputsFallbackDeterministically(t *testing.T) {
	description := "Need a plan for this work."

	plan := Analyze("Plan next steps", &description)
	if plan.Playbook != PlaybookExecutionSpec {
		t.Fatalf("Playbook = %q, want %q", plan.Playbook, PlaybookExecutionSpec)
	}
	if plan.ProjectStage != StageDefinition {
		t.Fatalf("ProjectStage = %q, want %q", plan.ProjectStage, StageDefinition)
	}
	if plan.EvidenceMaturity != EvidenceDirectional {
		t.Fatalf("EvidenceMaturity = %q, want %q", plan.EvidenceMaturity, EvidenceDirectional)
	}
	if plan.RiskLevel != RiskLow {
		t.Fatalf("RiskLevel = %q, want %q", plan.RiskLevel, RiskLow)
	}
}

func TestAnalyzeArtifactsPersistKindMetadataAndRepoBackedFields(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		playbook    string
		kind        string
	}{
		{
			name:        "discovery",
			title:       "Validate the onboarding problem",
			description: "Run customer interviews, document assumptions, and build a validation plan for this new product idea before we commit scope.",
			playbook:    PlaybookDiscovery,
			kind:        ArtifactKindDiscoveryPlan,
		},
		{
			name:        "strategy",
			title:       "Homepage design options",
			description: "Generate 10 homepage design options, compare them, and recommend a direction with tradeoffs.",
			playbook:    PlaybookStrategy,
			kind:        ArtifactKindStrategyArtifact,
		},
		{
			name:        "execution spec",
			title:       "PRD for billing migration",
			description: "Write the PRD, implementation plan, acceptance criteria, and dependency log for the billing migration.",
			playbook:    PlaybookExecutionSpec,
			kind:        ArtifactKindPRDSpec,
		},
		{
			name:        "backlog decomposition",
			title:       "Break the approved PRD into delivery work",
			description: "Decompose the scope into epics, user stories, sprint-ready tickets, and sequencing constraints.",
			playbook:    PlaybookBacklogDecomposition,
			kind:        ArtifactKindBacklogStoryPack,
		},
		{
			name:        "risk readiness",
			title:       "Launch readiness pre-mortem",
			description: "Build the pre-mortem, risk register, mitigation plan, and readiness checklist for the regulated rollout.",
			playbook:    PlaybookRiskReadiness,
			kind:        ArtifactKindPreMortemReadinessArtifact,
		},
		{
			name:        "metrics",
			title:       "Metric tree and instrumentation plan",
			description: "Define north-star metrics, KPI instrumentation, the dashboard spec, and weekly metric review cadence.",
			playbook:    PlaybookMetrics,
			kind:        ArtifactKindMetricsFramework,
		},
		{
			name:        "gtm launch",
			title:       "Go-to-market launch plan",
			description: "Create the GTM launch plan, messaging brief, channel plan, and sales enablement checklist for release.",
			playbook:    PlaybookGTMLaunch,
			kind:        ArtifactKindGTMLaunchPlan,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := Analyze(tc.title, &tc.description)
			if plan.Playbook != tc.playbook {
				t.Fatalf("Playbook = %q, want %q", plan.Playbook, tc.playbook)
			}
			if len(plan.Artifacts) == 0 {
				t.Fatalf("Artifacts = %#v, want non-empty artifact list", plan.Artifacts)
			}
			for i, artifact := range plan.Artifacts {
				if artifact.Kind != tc.kind {
					t.Fatalf("artifact[%d].Kind = %q, want %q", i, artifact.Kind, tc.kind)
				}
			}

			plan.Artifacts[0].ArtifactID = "artifact-1"
			plan.Artifacts[0].RepoPath = "planning/test/oc-1-artifact.md"
			plan.Artifacts[0].Version = 2
			plan.Artifacts[0].ContentSHA256 = "abc123"

			metadata := ApplyMetadata(json.RawMessage(`{}`), plan)
			parsed, ok := Parse(metadata)
			if !ok {
				t.Fatal("Parse(metadata) = false, want true")
			}
			if len(parsed.Artifacts) != len(plan.Artifacts) {
				t.Fatalf("parsed artifacts len = %d, want %d", len(parsed.Artifacts), len(plan.Artifacts))
			}
			first := parsed.Artifacts[0]
			if first.Kind != tc.kind {
				t.Fatalf("parsed artifact kind = %q, want %q", first.Kind, tc.kind)
			}
			if first.ArtifactID != "artifact-1" {
				t.Fatalf("parsed artifact_id = %q, want artifact-1", first.ArtifactID)
			}
			if first.RepoPath != "planning/test/oc-1-artifact.md" {
				t.Fatalf("parsed repo_path = %q, want planning/test/oc-1-artifact.md", first.RepoPath)
			}
			if first.Version != 2 {
				t.Fatalf("parsed version = %d, want 2", first.Version)
			}
			if first.ContentSHA256 != "abc123" {
				t.Fatalf("parsed content_sha256 = %q, want abc123", first.ContentSHA256)
			}
		})
	}
}

func TestParseHydratesArtifactKindDefaultsWhenMissing(t *testing.T) {
	parsed, ok := Parse(json.RawMessage(`{
		"planning": {
			"mode": "execution_first",
			"playbook": "execution_spec",
			"artifacts": [
				{
					"slug": "prd",
					"title": "PRD / requirements spec",
					"repo_path": "planning/prd-spec/oc-7-prd.md"
				}
			]
		}
	}`))
	if !ok {
		t.Fatal("Parse(metadata) = false, want true")
	}
	if len(parsed.Artifacts) != 1 {
		t.Fatalf("parsed artifacts len = %d, want 1", len(parsed.Artifacts))
	}
	if parsed.Artifacts[0].Kind != ArtifactKindPRDSpec {
		t.Fatalf("parsed artifact kind = %q, want %q", parsed.Artifacts[0].Kind, ArtifactKindPRDSpec)
	}
}

func TestApplyMetadataRoundTripsReviewPlanning(t *testing.T) {
	description := "Brainstorm 12 launch-week blog post ideas, compare them, and recommend a shortlist."

	plan := Analyze("Launch-week blog post ideas", &description)
	metadata := ApplyMetadata(json.RawMessage(`{"existing":true}`), plan)

	parsed, ok := Parse(metadata)
	if !ok {
		t.Fatal("Parse(metadata) = false, want true")
	}
	if parsed.Mode != ModeReviewAndRefinement {
		t.Fatalf("Mode = %q, want %q", parsed.Mode, ModeReviewAndRefinement)
	}
	if parsed.ReviewPacket.Summary == "" {
		t.Fatal("review packet summary = empty, want value")
	}
	if len(parsed.ReviewPacket.Sections) == 0 {
		t.Fatal("review packet sections = empty, want values")
	}
	if parsed.Playbook != PlaybookGTMLaunch {
		t.Fatalf("Playbook = %q, want %q", parsed.Playbook, PlaybookGTMLaunch)
	}
	if len(parsed.Artifacts) == 0 {
		t.Fatalf("Artifacts = %#v, want non-empty artifact list", parsed.Artifacts)
	}
	if len(parsed.FollowOnSuggestions) == 0 {
		t.Fatalf("FollowOnSuggestions = %#v, want non-empty follow-ons", parsed.FollowOnSuggestions)
	}
}
