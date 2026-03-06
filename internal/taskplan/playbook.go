package taskplan

import "strings"

const (
	PlaybookDiscovery            = "discovery"
	PlaybookStrategy             = "strategy"
	PlaybookExecutionSpec        = "execution_spec"
	PlaybookBacklogDecomposition = "backlog_decomposition"
	PlaybookRiskReadiness        = "risk_readiness"
	PlaybookMetrics              = "metrics"
	PlaybookGTMLaunch            = "gtm_launch"
)

const (
	ArtifactKindDiscoveryPlan              = "discovery_plan"
	ArtifactKindStrategyArtifact           = "strategy_artifact"
	ArtifactKindPRDSpec                    = "prd_spec"
	ArtifactKindBacklogStoryPack           = "backlog_story_pack"
	ArtifactKindPreMortemReadinessArtifact = "pre_mortem_readiness_artifact"
	ArtifactKindMetricsFramework           = "metrics_framework"
	ArtifactKindGTMLaunchPlan              = "gtm_launch_plan"
)

const (
	DiscoveryModeNewProduct      = "new_product"
	DiscoveryModeExistingProduct = "existing_product"
)

const (
	StageConcept    = "concept"
	StageValidation = "validation"
	StageDefinition = "definition"
	StageDelivery   = "delivery"
	StageLaunch     = "launch"
	StagePostLaunch = "post_launch"
)

const (
	EvidenceUnknown     = "unknown"
	EvidenceDirectional = "directional"
	EvidenceValidated   = "validated"
)

const (
	RiskLow      = "low"
	RiskMedium   = "medium"
	RiskHigh     = "high"
	RiskCritical = "critical"
)

const (
	workTypeGeneric = "generic"
)

type PlannedArtifact struct {
	Slug          string `json:"slug"`
	Title         string `json:"title"`
	Kind          string `json:"kind,omitempty"`
	ArtifactID    string `json:"artifact_id,omitempty"`
	RepoPath      string `json:"repo_path,omitempty"`
	Version       int    `json:"version,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}

var (
	playbookOrder = []string{
		PlaybookDiscovery,
		PlaybookStrategy,
		PlaybookExecutionSpec,
		PlaybookBacklogDecomposition,
		PlaybookRiskReadiness,
		PlaybookMetrics,
		PlaybookGTMLaunch,
	}

	playbookSignals = map[string][]string{
		PlaybookDiscovery: {
			"discovery",
			"discover",
			"user research",
			"user interviews",
			"customer research",
			"customer interview",
			"customer interviews",
			"problem validation",
			"validation plan",
			"validation experiment",
			"validation experiments",
			"market research",
			"validate demand",
			"prototype",
			"mvp",
			"hypothesis",
			"assumption",
			"assumptions",
			"experiment",
			"experiments",
		},
		PlaybookStrategy: {
			"strategy",
			"positioning",
			"roadmap",
			"prioritization",
			"portfolio",
			"vision",
			"pricing strategy",
			"packaging",
			"strategic",
			"homepage",
			"design",
			"brand",
			"branding",
			"creative",
			"concept",
			"copy",
		},
		PlaybookExecutionSpec: {
			"prd",
			"spec",
			"requirements",
			"implementation plan",
			"implement",
			"technical design",
			"architecture",
			"acceptance criteria",
			"delivery plan",
			"execution plan",
			"verify",
			"fix",
			"bug",
			"migration",
			"api",
			"webhook",
		},
		PlaybookBacklogDecomposition: {
			"backlog",
			"epic",
			"epics",
			"user story",
			"user stories",
			"tickets",
			"break down",
			"decompose",
			"sprint plan",
			"sprint backlog",
		},
		PlaybookRiskReadiness: {
			"risk",
			"readiness",
			"pre-mortem",
			"premortem",
			"mitigation",
			"contingency",
			"dependency risk",
			"launch readiness",
			"compliance",
			"security review",
		},
		PlaybookMetrics: {
			"metric",
			"metrics",
			"kpi",
			"north star",
			"instrumentation",
			"measurement",
			"dashboard",
			"funnel",
			"retention",
			"activation",
		},
		PlaybookGTMLaunch: {
			"go-to-market",
			"gtm",
			"launch",
			"rollout",
			"release announcement",
			"campaign",
			"sales enablement",
			"messaging",
			"launch plan",
		},
	}

	conceptStageSignals = []string{
		"new product",
		"net new",
		"0 to 1",
		"zero to one",
		"concept",
		"idea",
		"greenfield",
	}
	validationStageSignals = []string{
		"validation",
		"validate",
		"prototype",
		"pilot",
		"experiment",
		"customer interview",
		"user research",
	}
	definitionStageSignals = []string{
		"strategy",
		"positioning",
		"roadmap",
		"spec",
		"prd",
		"requirements",
		"scope",
	}
	deliveryStageSignals = []string{
		"implementation",
		"build",
		"delivery",
		"decompose",
		"backlog",
		"sprint",
		"ticket",
	}
	launchStageSignals = []string{
		"launch",
		"rollout",
		"go-to-market",
		"gtm",
		"release",
		"announce",
		"beta launch",
	}
	postLaunchStageSignals = []string{
		"post-launch",
		"post launch",
		"adoption",
		"retention",
		"engagement",
		"instrumentation",
		"dashboard",
	}
	existingProductSignals = []string{
		"existing product",
		"current product",
		"existing feature",
		"existing users",
		"current users",
		"current funnel",
		"live product",
		"usage data",
		"instrumentation",
		"support ticket",
		"support tickets",
		"retention",
		"feedback",
		"today's product",
	}
	newProductSignals = []string{
		"new product",
		"new idea",
		"unvalidated concept",
		"unproven concept",
		"net new",
		"0 to 1",
		"zero to one",
		"greenfield",
	}

	evidenceUnknownSignals = []string{
		"hypothesis",
		"unknown",
		"unclear",
		"explore",
		"exploratory",
		"no data",
		"greenfield",
		"assumption",
	}
	evidenceDirectionalSignals = []string{
		"beta feedback",
		"qualitative",
		"directional",
		"initial data",
		"early signal",
		"some evidence",
		"customer feedback",
	}
	evidenceValidatedSignals = []string{
		"validated",
		"confirmed",
		"measured",
		"production data",
		"usage data",
		"experiment result",
		"win/loss",
		"benchmark",
	}

	riskMediumSignals = []string{
		"risk",
		"tradeoff",
		"trade-off",
		"dependency",
		"cross-functional",
		"deadline",
	}
	riskHighSignals = []string{
		"high risk",
		"security",
		"migration",
		"compliance",
		"launch blocker",
		"replatform",
		"executive",
		"board",
		"public launch",
	}
	riskCriticalSignals = []string{
		"critical",
		"sev1",
		"regulatory",
		"incident",
		"customer outage",
		"irreversible",
	}
	executionSpecPlanningSignals = []string{
		"prd",
		"spec",
		"requirements",
		"implementation plan",
		"technical design",
		"architecture",
		"acceptance criteria",
		"delivery plan",
		"execution plan",
	}
)

func selectPlaybookContext(text string) (playbook, workType, projectStage, evidenceMaturity, riskLevel, discoveryMode string) {
	workType = inferWorkType(text)
	projectStage = inferProjectStage(text, workType)
	evidenceMaturity = inferEvidenceMaturity(text, projectStage)
	riskLevel = inferRiskLevel(text, projectStage, evidenceMaturity)
	playbook = selectPlaybook(text, workType, projectStage, evidenceMaturity, riskLevel)
	if playbook == PlaybookDiscovery {
		discoveryMode = inferDiscoveryMode(text, projectStage, evidenceMaturity)
	}
	return playbook, workType, projectStage, evidenceMaturity, riskLevel, discoveryMode
}

func inferWorkType(text string) string {
	bestType := workTypeGeneric
	bestScore := 0
	for _, candidate := range playbookOrder {
		score := signalScore(text, playbookSignals[candidate])
		if score > bestScore {
			bestType = candidate
			bestScore = score
		}
	}
	return bestType
}

func inferProjectStage(text, workType string) string {
	switch {
	case containsAny(text, postLaunchStageSignals):
		return StagePostLaunch
	case containsAny(text, launchStageSignals):
		return StageLaunch
	case containsAny(text, deliveryStageSignals):
		return StageDelivery
	case containsAny(text, definitionStageSignals):
		return StageDefinition
	case containsAny(text, validationStageSignals):
		return StageValidation
	case containsAny(text, conceptStageSignals):
		return StageConcept
	}

	switch workType {
	case PlaybookDiscovery:
		if containsAny(text, existingProductSignals) {
			return StageValidation
		}
		return StageConcept
	case PlaybookStrategy:
		return StageDefinition
	case PlaybookBacklogDecomposition, PlaybookExecutionSpec, PlaybookRiskReadiness:
		return StageDelivery
	case PlaybookGTMLaunch:
		return StageLaunch
	case PlaybookMetrics:
		return StagePostLaunch
	default:
		return StageDefinition
	}
}

func inferEvidenceMaturity(text, projectStage string) string {
	switch {
	case containsAny(text, evidenceValidatedSignals):
		return EvidenceValidated
	case containsAny(text, evidenceDirectionalSignals):
		return EvidenceDirectional
	case containsAny(text, evidenceUnknownSignals):
		return EvidenceUnknown
	}

	switch projectStage {
	case StageConcept, StageValidation:
		return EvidenceUnknown
	case StageLaunch:
		return EvidenceDirectional
	case StageDelivery, StagePostLaunch:
		return EvidenceValidated
	default:
		return EvidenceDirectional
	}
}

func inferRiskLevel(text, projectStage, evidenceMaturity string) string {
	switch {
	case containsAny(text, riskCriticalSignals):
		return RiskCritical
	case containsAny(text, riskHighSignals):
		return RiskHigh
	case containsAny(text, riskMediumSignals):
		return RiskMedium
	}

	if projectStage == StageLaunch && evidenceMaturity != EvidenceValidated {
		return RiskHigh
	}
	if projectStage == StageDelivery && evidenceMaturity == EvidenceUnknown {
		return RiskMedium
	}
	return RiskLow
}

func inferDiscoveryMode(text, projectStage, evidenceMaturity string) string {
	switch {
	case containsAny(text, existingProductSignals):
		return DiscoveryModeExistingProduct
	case containsAny(text, newProductSignals):
		return DiscoveryModeNewProduct
	}

	switch projectStage {
	case StageConcept:
		return DiscoveryModeNewProduct
	case StageDelivery, StageLaunch, StagePostLaunch:
		return DiscoveryModeExistingProduct
	}

	if evidenceMaturity == EvidenceValidated {
		return DiscoveryModeExistingProduct
	}
	return DiscoveryModeNewProduct
}

func selectPlaybook(text, workType, projectStage, evidenceMaturity, riskLevel string) string {
	if workType == PlaybookRiskReadiness || riskLevel == RiskCritical {
		return PlaybookRiskReadiness
	}

	switch workType {
	case PlaybookDiscovery, PlaybookStrategy, PlaybookExecutionSpec, PlaybookBacklogDecomposition, PlaybookMetrics, PlaybookGTMLaunch:
		return workType
	}

	if riskLevel == RiskHigh && projectStage == StageLaunch && evidenceMaturity != EvidenceValidated {
		return PlaybookRiskReadiness
	}

	switch projectStage {
	case StageLaunch:
		return PlaybookGTMLaunch
	case StagePostLaunch:
		return PlaybookMetrics
	case StageConcept:
		return PlaybookDiscovery
	case StageValidation:
		if evidenceMaturity == EvidenceValidated {
			return PlaybookStrategy
		}
		return PlaybookDiscovery
	case StageDefinition:
		return PlaybookExecutionSpec
	case StageDelivery:
		if strings.Contains(text, "backlog") || strings.Contains(text, "story") || strings.Contains(text, "decompose") {
			return PlaybookBacklogDecomposition
		}
		return PlaybookExecutionSpec
	default:
		return PlaybookExecutionSpec
	}
}

func playbookArtifacts(playbook string) []PlannedArtifact {
	kind := DefaultArtifactKindForPlaybook(playbook)
	switch playbook {
	case PlaybookDiscovery:
		return []PlannedArtifact{
			{Slug: "problem-brief", Title: "Problem brief", Kind: kind},
			{Slug: "research-plan", Title: "Research plan", Kind: kind},
			{Slug: "assumption-log", Title: "Assumption log", Kind: kind},
			{Slug: "validation-plan", Title: "Validation plan", Kind: kind},
		}
	case PlaybookStrategy:
		return []PlannedArtifact{
			{Slug: "strategy-brief", Title: "Strategy brief", Kind: kind},
			{Slug: "tradeoff-matrix", Title: "Tradeoff matrix", Kind: kind},
			{Slug: "decision-log", Title: "Decision log", Kind: kind},
			{Slug: "success-narrative", Title: "Success narrative", Kind: kind},
		}
	case PlaybookExecutionSpec:
		return []PlannedArtifact{
			{Slug: "prd", Title: "PRD / requirements spec", Kind: kind},
			{Slug: "implementation-plan", Title: "Implementation plan", Kind: kind},
			{Slug: "acceptance-criteria", Title: "Acceptance criteria", Kind: kind},
			{Slug: "dependency-log", Title: "Dependency log", Kind: kind},
		}
	case PlaybookBacklogDecomposition:
		return []PlannedArtifact{
			{Slug: "epic-breakdown", Title: "Epic breakdown", Kind: kind},
			{Slug: "story-cards", Title: "Story cards", Kind: kind},
			{Slug: "sequencing-plan", Title: "Sequencing plan", Kind: kind},
			{Slug: "definition-of-done", Title: "Definition of done", Kind: kind},
		}
	case PlaybookRiskReadiness:
		return []PlannedArtifact{
			{Slug: "risk-register", Title: "Risk register", Kind: kind},
			{Slug: "premortem", Title: "Pre-mortem", Kind: kind},
			{Slug: "mitigation-plan", Title: "Mitigation plan", Kind: kind},
			{Slug: "readiness-checklist", Title: "Readiness checklist", Kind: kind},
		}
	case PlaybookMetrics:
		return []PlannedArtifact{
			{Slug: "metric-tree", Title: "Metric tree", Kind: kind},
			{Slug: "instrumentation-plan", Title: "Instrumentation plan", Kind: kind},
			{Slug: "dashboard-spec", Title: "Dashboard spec", Kind: kind},
			{Slug: "review-cadence", Title: "Metric review cadence", Kind: kind},
		}
	case PlaybookGTMLaunch:
		return []PlannedArtifact{
			{Slug: "launch-brief", Title: "Launch brief", Kind: kind},
			{Slug: "audience-messaging", Title: "Audience and messaging brief", Kind: kind},
			{Slug: "channel-plan", Title: "Channel plan", Kind: kind},
			{Slug: "launch-checklist", Title: "Launch checklist", Kind: kind},
		}
	default:
		return []PlannedArtifact{
			{Slug: "implementation-plan", Title: "Implementation plan", Kind: kind},
		}
	}
}

func DefaultArtifactKindForPlaybook(playbook string) string {
	switch playbook {
	case PlaybookDiscovery:
		return ArtifactKindDiscoveryPlan
	case PlaybookStrategy:
		return ArtifactKindStrategyArtifact
	case PlaybookExecutionSpec:
		return ArtifactKindPRDSpec
	case PlaybookBacklogDecomposition:
		return ArtifactKindBacklogStoryPack
	case PlaybookRiskReadiness:
		return ArtifactKindPreMortemReadinessArtifact
	case PlaybookMetrics:
		return ArtifactKindMetricsFramework
	case PlaybookGTMLaunch:
		return ArtifactKindGTMLaunchPlan
	default:
		return ArtifactKindPRDSpec
	}
}

func NormalizeArtifactKind(value string) string {
	trimmed := strings.TrimSpace(value)
	switch trimmed {
	case ArtifactKindDiscoveryPlan,
		ArtifactKindStrategyArtifact,
		ArtifactKindPRDSpec,
		ArtifactKindBacklogStoryPack,
		ArtifactKindPreMortemReadinessArtifact,
		ArtifactKindMetricsFramework,
		ArtifactKindGTMLaunchPlan:
		return trimmed
	default:
		return ""
	}
}

func NormalizeDiscoveryMode(value string) string {
	switch strings.TrimSpace(value) {
	case DiscoveryModeNewProduct:
		return DiscoveryModeNewProduct
	case DiscoveryModeExistingProduct:
		return DiscoveryModeExistingProduct
	default:
		return ""
	}
}

func DiscoveryValidationPlanSections(mode string) []string {
	sections := []string{
		"ideas explored",
		"assumptions",
		"validation experiments",
	}

	switch NormalizeDiscoveryMode(mode) {
	case DiscoveryModeExistingProduct:
		sections = append(sections, "prior feedback", "instrumentation baseline")
	default:
		sections = append(sections, "low-cost tests", "desirability signals")
	}

	sections = append(sections, "decision framework")
	return sections
}

func playbookFollowOnSuggestions(playbook, projectStage, evidenceMaturity, riskLevel, discoveryMode string) []string {
	switch playbook {
	case PlaybookDiscovery:
		switch NormalizeDiscoveryMode(discoveryMode) {
		case DiscoveryModeExistingProduct:
			return []string{
				"Pull instrumentation, usage data, and prior feedback for the current product surface before changing scope.",
				"Turn observed behavior into targeted experiments on the live product before escalating to delivery planning.",
				"Promote validated findings into a strategy or execution/spec planning pass once the strongest usage signals are clear.",
			}
		default:
			return []string{
				"Schedule research or discovery interviews and low-cost desirability tests before committing delivery scope.",
				"Use prototypes, concierge workflows, or smoke tests to retire the highest-risk assumptions before writing specs.",
				"Promote validated findings into a strategy or execution/spec planning pass once the concept earns more investment.",
			}
		}
	case PlaybookStrategy:
		return []string{
			"Resolve the highest-impact tradeoffs with stakeholders before writing delivery tickets.",
			"Convert the chosen direction into an execution/spec artifact once decisions are locked.",
			"Set explicit success criteria so downstream backlog work stays aligned.",
		}
	case PlaybookExecutionSpec:
		return []string{
			"Review the spec with delivery owners before queuing implementation.",
			"Break approved scope into backlog-ready work once dependencies are clear.",
			"Run a readiness pass if hidden dependencies or launch risk emerge during planning.",
		}
	case PlaybookBacklogDecomposition:
		return []string{
			"Estimate and sequence the decomposed work before sprint or queue commitment.",
			"Assign owners and dependency order so execution can start without re-planning.",
			"Promote blocked stories back into execution/spec or risk/readiness planning when needed.",
		}
	case PlaybookRiskReadiness:
		return []string{
			"Assign mitigation owners and dates for every high-risk item before sign-off.",
			"Hold a readiness review once mitigation status is updated.",
			"Feed unresolved blockers back into execution, GTM, or metrics planning as follow-up work.",
		}
	case PlaybookMetrics:
		return []string{
			"Instrument the agreed events and baselines before launch or experiment review.",
			"Review the metric tree with product and GTM owners on a fixed cadence.",
			"Turn weak signal areas into discovery tasks if the evidence remains directional.",
		}
	case PlaybookGTMLaunch:
		return []string{
			"Align sales, support, and product owners on the launch brief before public rollout.",
			"Run a launch-readiness checkpoint if risk stays " + riskLevel + ".",
			"Pair launch execution with a metrics plan so adoption is measured immediately after release.",
		}
	default:
		suggestions := []string{
			"Review the plan with the project manager before queueing execution.",
			"Promote the task into a more specific planning playbook if new evidence changes the shape of the work.",
		}
		if projectStage == StageLaunch || evidenceMaturity == EvidenceUnknown {
			suggestions = append(suggestions, "Add a risk/readiness review before final sign-off.")
		}
		return suggestions
	}
}

func signalScore(text string, signals []string) int {
	score := 0
	for _, signal := range signals {
		if strings.Contains(text, signal) {
			score++
		}
	}
	return score
}

func playbookRequiresContract(playbook, text string) bool {
	switch strings.TrimSpace(playbook) {
	case PlaybookDiscovery,
		PlaybookStrategy,
		PlaybookBacklogDecomposition,
		PlaybookRiskReadiness,
		PlaybookMetrics,
		PlaybookGTMLaunch:
		return true
	case PlaybookExecutionSpec:
		return containsAny(text, executionSpecPlanningSignals)
	default:
		return false
	}
}
