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
	Slug  string `json:"slug"`
	Title string `json:"title"`
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
			"customer research",
			"customer interview",
			"problem validation",
			"market research",
			"validate demand",
			"prototype",
			"mvp",
			"hypothesis",
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
		"existing users",
		"current funnel",
		"today's product",
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
)

func selectPlaybookContext(text string) (playbook, workType, projectStage, evidenceMaturity, riskLevel string) {
	workType = inferWorkType(text)
	projectStage = inferProjectStage(text, workType)
	evidenceMaturity = inferEvidenceMaturity(text, projectStage)
	riskLevel = inferRiskLevel(text, projectStage, evidenceMaturity)
	playbook = selectPlaybook(text, workType, projectStage, evidenceMaturity, riskLevel)
	return playbook, workType, projectStage, evidenceMaturity, riskLevel
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
	switch playbook {
	case PlaybookDiscovery:
		return []PlannedArtifact{
			{Slug: "problem-brief", Title: "Problem brief"},
			{Slug: "research-plan", Title: "Research plan"},
			{Slug: "assumption-log", Title: "Assumption log"},
			{Slug: "validation-plan", Title: "Validation plan"},
		}
	case PlaybookStrategy:
		return []PlannedArtifact{
			{Slug: "strategy-brief", Title: "Strategy brief"},
			{Slug: "tradeoff-matrix", Title: "Tradeoff matrix"},
			{Slug: "decision-log", Title: "Decision log"},
			{Slug: "success-narrative", Title: "Success narrative"},
		}
	case PlaybookExecutionSpec:
		return []PlannedArtifact{
			{Slug: "prd", Title: "PRD / requirements spec"},
			{Slug: "implementation-plan", Title: "Implementation plan"},
			{Slug: "acceptance-criteria", Title: "Acceptance criteria"},
			{Slug: "dependency-log", Title: "Dependency log"},
		}
	case PlaybookBacklogDecomposition:
		return []PlannedArtifact{
			{Slug: "epic-breakdown", Title: "Epic breakdown"},
			{Slug: "story-cards", Title: "Story cards"},
			{Slug: "sequencing-plan", Title: "Sequencing plan"},
			{Slug: "definition-of-done", Title: "Definition of done"},
		}
	case PlaybookRiskReadiness:
		return []PlannedArtifact{
			{Slug: "risk-register", Title: "Risk register"},
			{Slug: "premortem", Title: "Pre-mortem"},
			{Slug: "mitigation-plan", Title: "Mitigation plan"},
			{Slug: "readiness-checklist", Title: "Readiness checklist"},
		}
	case PlaybookMetrics:
		return []PlannedArtifact{
			{Slug: "metric-tree", Title: "Metric tree"},
			{Slug: "instrumentation-plan", Title: "Instrumentation plan"},
			{Slug: "dashboard-spec", Title: "Dashboard spec"},
			{Slug: "review-cadence", Title: "Metric review cadence"},
		}
	case PlaybookGTMLaunch:
		return []PlannedArtifact{
			{Slug: "launch-brief", Title: "Launch brief"},
			{Slug: "audience-messaging", Title: "Audience and messaging brief"},
			{Slug: "channel-plan", Title: "Channel plan"},
			{Slug: "launch-checklist", Title: "Launch checklist"},
		}
	default:
		return []PlannedArtifact{
			{Slug: "implementation-plan", Title: "Implementation plan"},
		}
	}
}

func playbookFollowOnSuggestions(playbook, projectStage, evidenceMaturity, riskLevel string) []string {
	switch playbook {
	case PlaybookDiscovery:
		return []string{
			"Schedule research or discovery interviews before committing delivery scope.",
			"Synthesize evidence into a go/no-go recommendation once the first discovery pass completes.",
			"Promote validated findings into a strategy or execution/spec planning pass.",
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
