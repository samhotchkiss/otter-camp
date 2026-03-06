package taskplan

import "strings"

const (
	FollowOnStatusProposed = "proposed"
	FollowOnStatusStopped  = "stopped"

	FollowOnActionReadyTask    = "ready_task"
	FollowOnActionDraftTask    = "draft_task"
	FollowOnActionReviewPacket = "review_packet"
)

type FollowOnCandidate struct {
	ActionType         string `json:"action_type"`
	WorkStatus         string `json:"work_status,omitempty"`
	Title              string `json:"title"`
	Summary            string `json:"summary,omitempty"`
	Reason             string `json:"reason,omitempty"`
	TargetPlaybook     string `json:"target_playbook,omitempty"`
	SourceArtifactSlug string `json:"source_artifact_slug,omitempty"`
}

type FollowOnPlan struct {
	Status     string              `json:"status,omitempty"`
	Reason     string              `json:"reason,omitempty"`
	StopReason string              `json:"stop_reason,omitempty"`
	Candidates []FollowOnCandidate `json:"candidates,omitempty"`
}

func hydrateFollowOnPlan(plan Plan) Plan {
	plan.FollowOn = inferFollowOnPlan(plan)
	return plan
}

func inferFollowOnPlan(plan Plan) FollowOnPlan {
	candidates := followOnCandidates(plan)
	stopReason := strings.TrimSpace(plan.FollowOnStopReason)
	if stopReason != "" {
		return FollowOnPlan{
			Status:     FollowOnStatusStopped,
			Reason:     "Stopped intentionally after the current planning artifact package.",
			StopReason: stopReason,
			Candidates: append([]FollowOnCandidate(nil), candidates...),
		}
	}
	if len(candidates) == 0 {
		return FollowOnPlan{
			Status: FollowOnStatusStopped,
			Reason: "No routine follow-on planning step was inferred from this playbook.",
		}
	}
	return FollowOnPlan{
		Status:     FollowOnStatusProposed,
		Reason:     followOnContinuationReason(plan),
		Candidates: append([]FollowOnCandidate(nil), candidates...),
	}
}

func followOnCandidates(plan Plan) []FollowOnCandidate {
	actionType := followOnActionType(plan)
	switch strings.TrimSpace(plan.Playbook) {
	case PlaybookDiscovery:
		title := "Translate validated discovery into a PRD/spec"
		summary := "Turn the problem brief, assumption log, and validation plan into scoped requirements, constraints, success metrics, and open questions."
		reason := "Discovery should flow into execution/spec planning once the strongest assumptions and experiments are documented."
		if plan.DiscoveryMode == DiscoveryModeExistingProduct {
			title = "Write a PRD/spec for the highest-signal existing-product opportunity"
			summary = "Use the problem brief, prior feedback, instrumentation baseline, and validation plan to scope the next existing-product bet."
			reason = "Existing-product discovery should feed a spec once usage signals and experiments identify the best opportunity."
		}
		return []FollowOnCandidate{
			newFollowOnCandidate(actionType, title, summary, reason, PlaybookExecutionSpec, "validation-plan"),
		}
	case PlaybookStrategy:
		return []FollowOnCandidate{
			newFollowOnCandidate(
				actionType,
				"Turn the chosen strategy into a roadmap and milestone plan",
				"Translate the strategy brief, tradeoff matrix, and decision log into a roadmap with operating milestones and explicit sequencing.",
				"Strategy work should hand off to roadmap planning so the chosen direction has milestones, owners, and timing.",
				PlaybookStrategy,
				"success-narrative",
			),
			newFollowOnCandidate(
				actionType,
				"Define OKRs and metric guardrails for the strategy",
				"Convert the success narrative into measurable OKRs, metric guardrails, and review checkpoints for the chosen strategy.",
				"Strategy should produce measurable success criteria before delivery teams inherit it.",
				PlaybookMetrics,
				"success-narrative",
			),
			newFollowOnCandidate(
				actionType,
				"Create a GTM brief for the selected segment and positioning",
				"Turn the strategy brief and tradeoff matrix into messaging, channel, and launch follow-ons for the chosen market direction.",
				"Strategy should flow into GTM planning once the segment and positioning are selected.",
				PlaybookGTMLaunch,
				"strategy-brief",
			),
		}
	case PlaybookExecutionSpec:
		return []FollowOnCandidate{
			newFollowOnCandidate(
				actionType,
				"Break the approved spec into backlog-ready work",
				"Use the implementation plan and dependency log to decompose the approved spec into sequenced stories, owners, and queue-ready slices.",
				"Execution/spec planning should hand off to backlog decomposition once scope and dependencies are clear.",
				PlaybookBacklogDecomposition,
				"implementation-plan",
			),
			newFollowOnCandidate(
				actionType,
				"Create test scenarios for the acceptance and edge cases",
				"Turn the acceptance criteria into a test-scenario pack that covers primary paths, edge cases, and rollout verification.",
				"Specs should produce explicit test scenarios so delivery and review can verify the agreed behavior.",
				"",
				"acceptance-criteria",
			),
		}
	case PlaybookBacklogDecomposition:
		return []FollowOnCandidate{
			newFollowOnCandidate(
				actionType,
				"Create test-scenario coverage for the sequenced stories",
				"Convert the story cards, sequencing plan, and definition of done into a test-scenario pack for the queued work.",
				"Backlog decomposition should continue into concrete verification so execution can start without re-planning quality gates.",
				"",
				"story-cards",
			),
		}
	case PlaybookRiskReadiness:
		return []FollowOnCandidate{
			newFollowOnCandidate(
				actionType,
				"Create targeted test scenarios for the highest-risk failure modes",
				"Turn the pre-mortem and risk register into explicit test scenarios that exercise the highest-severity risks before sign-off.",
				"Risk/readiness planning should continue into targeted verification for the riskiest failure modes.",
				"",
				"premortem",
			),
			newFollowOnCandidate(
				actionType,
				"Run a readiness checkpoint on unresolved blockers",
				"Package the mitigation plan and readiness checklist into a checkpoint review once owners update blocker status.",
				"Risk/readiness work should end with a concrete checkpoint review before launch or migration approval.",
				"",
				"readiness-checklist",
			),
		}
	case PlaybookMetrics:
		return []FollowOnCandidate{
			newFollowOnCandidate(
				actionType,
				"Create dashboard and success-tracking tasks from the metric tree",
				"Turn the metric tree, dashboard spec, and review cadence into concrete scorecard, dashboard, and alerting follow-on work.",
				"Metrics planning should continue into success-tracking execution once the framework is defined.",
				"",
				"metric-tree",
			),
			newFollowOnCandidate(
				actionType,
				"Turn the metric framework into OKRs and operating reviews",
				"Use the north star, input metrics, and thresholds to define operating reviews, OKRs, and owner check-ins.",
				"Metrics planning should define how the team will operate on the framework, not just document it.",
				PlaybookStrategy,
				"review-cadence",
			),
		}
	case PlaybookGTMLaunch:
		return []FollowOnCandidate{
			newFollowOnCandidate(
				actionType,
				"Create launch checklist and channel-execution tasks",
				"Turn the launch brief, messaging, and channel plan into owned execution tasks for launch readiness and channel delivery.",
				"GTM planning should continue into owned launch execution once messaging and timing are defined.",
				"",
				"launch-checklist",
			),
			newFollowOnCandidate(
				actionType,
				"Create success-tracking tasks for launch metrics",
				"Use the launch brief and success metrics to define post-launch dashboards, scorecards, and owner follow-ups.",
				"GTM plans should create success-tracking work before rollout day so launch outcomes are measured immediately.",
				PlaybookMetrics,
				"launch-brief",
			),
		}
	default:
		return nil
	}
}

func followOnActionType(plan Plan) string {
	if plan.RequiresReviewAndRefinement() || normalizePolicyMode(plan.ReviewPolicyMode) == PolicyHumanReviewRequired {
		return FollowOnActionReviewPacket
	}
	switch strings.TrimSpace(plan.RiskLevel) {
	case RiskHigh, RiskCritical:
		return FollowOnActionDraftTask
	}
	if strings.TrimSpace(plan.EvidenceMaturity) == EvidenceUnknown {
		return FollowOnActionDraftTask
	}
	return FollowOnActionReadyTask
}

func followOnContinuationReason(plan Plan) string {
	switch followOnActionType(plan) {
	case FollowOnActionReviewPacket:
		return "The next planning step is obvious, but existing review rules require packaging it as a review packet before downstream work is created."
	case FollowOnActionDraftTask:
		return "The next planning step is obvious, but current risk or evidence signals keep it as draft planning work until someone confirms it."
	default:
		return "The next planning step is routine enough to queue immediately."
	}
}

func followOnWorkStatus(actionType string) string {
	switch actionType {
	case FollowOnActionReadyTask:
		return "queued"
	case FollowOnActionDraftTask:
		return "draft"
	default:
		return ""
	}
}

func newFollowOnCandidate(actionType, title, summary, reason, targetPlaybook, sourceArtifactSlug string) FollowOnCandidate {
	candidate := FollowOnCandidate{
		ActionType:         strings.TrimSpace(actionType),
		Title:              strings.TrimSpace(title),
		Summary:            strings.TrimSpace(summary),
		Reason:             strings.TrimSpace(reason),
		TargetPlaybook:     strings.TrimSpace(targetPlaybook),
		SourceArtifactSlug: strings.TrimSpace(sourceArtifactSlug),
	}
	if workStatus := followOnWorkStatus(candidate.ActionType); workStatus != "" {
		candidate.WorkStatus = workStatus
	}
	return candidate
}
