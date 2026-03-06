package planningasset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

type artifactRepository interface {
	UpsertVersion(ctx context.Context, artifact repo.PlanningArtifactUpsert) (repo.PlanningArtifact, bool, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.PlanningArtifact, error)
	ListBySourceTask(ctx context.Context, taskID uuid.UUID) ([]repo.PlanningArtifact, error)
	ListVersions(ctx context.Context, artifactID uuid.UUID) ([]repo.PlanningArtifactVersion, error)
}

type environmentRepository interface {
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error)
}

type Actor struct {
	Type string
	ID   *uuid.UUID
}

type Options struct {
	Artifacts    artifactRepository
	Environments environmentRepository
	Clock        func() time.Time
}

type Service struct {
	artifacts    artifactRepository
	environments environmentRepository
	clock        func() time.Time
}

func New(opts Options) *Service {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Service{
		artifacts:    opts.Artifacts,
		environments: opts.Environments,
		clock:        clock,
	}
}

func (s *Service) SyncTask(ctx context.Context, task repo.ProjectTask, actor Actor) (taskplan.Plan, error) {
	plan, ok := taskplan.Parse(task.Metadata)
	if !ok || !plan.HasSelection() || len(plan.Artifacts) == 0 {
		return plan, nil
	}
	if s.artifacts == nil || s.environments == nil {
		return plan, nil
	}

	repoRoot, err := s.projectRepoRoot(ctx, task.ProjectID)
	if err != nil || repoRoot == "" {
		return plan, err
	}

	synced := make([]taskplan.PlannedArtifact, 0, len(plan.Artifacts))
	for _, artifact := range plan.Artifacts {
		updated, syncErr := s.syncArtifact(ctx, repoRoot, task, plan, artifact, actor)
		if syncErr != nil {
			return plan, syncErr
		}
		synced = append(synced, updated)
	}
	plan.Artifacts = synced
	return plan, nil
}

func (s *Service) ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.PlanningArtifact, error) {
	if s == nil || s.artifacts == nil {
		return nil, nil
	}
	return s.artifacts.ListByProject(ctx, projectID)
}

func (s *Service) ListByTask(ctx context.Context, taskID uuid.UUID) ([]repo.PlanningArtifact, error) {
	if s == nil || s.artifacts == nil {
		return nil, nil
	}
	return s.artifacts.ListBySourceTask(ctx, taskID)
}

func (s *Service) ListVersions(ctx context.Context, artifactID uuid.UUID) ([]repo.PlanningArtifactVersion, error) {
	if s == nil || s.artifacts == nil {
		return nil, nil
	}
	return s.artifacts.ListVersions(ctx, artifactID)
}

func OverlayPlanArtifacts(plan taskplan.Plan, records []repo.PlanningArtifact) taskplan.Plan {
	if len(records) == 0 {
		return plan
	}

	bySlug := make(map[string]repo.PlanningArtifact, len(records))
	byPath := make(map[string]repo.PlanningArtifact, len(records))
	for _, record := range records {
		if slug := strings.TrimSpace(record.ArtifactSlug); slug != "" {
			bySlug[slug] = record
		}
		if repoPath := strings.TrimSpace(record.RepoPath); repoPath != "" {
			byPath[repoPath] = record
		}
	}

	out := plan
	if len(out.Artifacts) == 0 {
		out.Artifacts = PlannedArtifactsFromRecords(records)
		return out
	}

	merged := make([]taskplan.PlannedArtifact, 0, len(out.Artifacts))
	for _, artifact := range out.Artifacts {
		record, ok := byPath[strings.TrimSpace(artifact.RepoPath)]
		if !ok {
			record, ok = bySlug[strings.TrimSpace(artifact.Slug)]
		}
		if !ok {
			merged = append(merged, artifact)
			continue
		}
		artifact.Kind = strings.TrimSpace(record.ArtifactKind)
		artifact.ArtifactID = record.ID.String()
		artifact.RepoPath = strings.TrimSpace(record.RepoPath)
		artifact.Version = record.CurrentVersion
		artifact.ContentSHA256 = strings.TrimSpace(record.LatestContentSHA256)
		merged = append(merged, artifact)
	}
	out.Artifacts = merged
	return out
}

func PlannedArtifactsFromRecords(records []repo.PlanningArtifact) []taskplan.PlannedArtifact {
	out := make([]taskplan.PlannedArtifact, 0, len(records))
	for _, record := range records {
		out = append(out, taskplan.PlannedArtifact{
			Slug:          strings.TrimSpace(record.ArtifactSlug),
			Title:         strings.TrimSpace(record.Title),
			Kind:          strings.TrimSpace(record.ArtifactKind),
			ArtifactID:    record.ID.String(),
			RepoPath:      strings.TrimSpace(record.RepoPath),
			Version:       record.CurrentVersion,
			ContentSHA256: strings.TrimSpace(record.LatestContentSHA256),
		})
	}
	return out
}

func (s *Service) projectRepoRoot(ctx context.Context, projectID uuid.UUID) (string, error) {
	environments, err := s.environments.ListByProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	for _, environment := range environments {
		if !environment.IsActive {
			continue
		}
		if root := strings.TrimSpace(derefString(environment.RepoPath)); root != "" {
			return filepath.Abs(root)
		}
	}
	for _, environment := range environments {
		if root := strings.TrimSpace(derefString(environment.RepoPath)); root != "" {
			return filepath.Abs(root)
		}
	}
	return "", nil
}

func (s *Service) syncArtifact(
	ctx context.Context,
	repoRoot string,
	task repo.ProjectTask,
	plan taskplan.Plan,
	artifact taskplan.PlannedArtifact,
	actor Actor,
) (taskplan.PlannedArtifact, error) {
	artifact.Kind = taskplan.NormalizeArtifactKind(artifact.Kind)
	if artifact.Kind == "" {
		artifact.Kind = taskplan.DefaultArtifactKindForPlaybook(plan.Playbook)
	}
	relPath := sanitizeRepoPath(artifact.RepoPath)
	if relPath == "" {
		relPath = defaultRepoPath(task, artifact)
	}
	absPath, err := resolveArtifactPath(repoRoot, relPath)
	if err != nil {
		return artifact, err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return artifact, err
	}
	if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
		if err := os.WriteFile(absPath, []byte(scaffoldContent(s.clock(), task, plan, artifact)), 0o644); err != nil {
			return artifact, err
		}
	} else if statErr != nil {
		return artifact, statErr
	}

	payload, err := os.ReadFile(absPath)
	if err != nil {
		return artifact, err
	}
	contentHash := sha256Hex(payload)
	record, _, err := s.artifacts.UpsertVersion(ctx, repo.PlanningArtifactUpsert{
		OrganizationID:      task.OrganizationID,
		ProjectID:           task.ProjectID,
		SourceTaskID:        task.ID,
		ArtifactKind:        artifact.Kind,
		ArtifactSlug:        strings.TrimSpace(artifact.Slug),
		Title:               strings.TrimSpace(artifact.Title),
		RepoPath:            filepath.ToSlash(relPath),
		LatestContentSHA256: contentHash,
		ByteSize:            len(payload),
		CreatedByType:       normalizeActorType(actor.Type),
		CreatedByID:         actor.ID,
	})
	if err != nil {
		return artifact, err
	}

	artifact.ArtifactID = record.ID.String()
	artifact.RepoPath = strings.TrimSpace(record.RepoPath)
	artifact.Version = record.CurrentVersion
	artifact.ContentSHA256 = strings.TrimSpace(record.LatestContentSHA256)
	return artifact, nil
}

func sanitizeRepoPath(repoPath string) string {
	trimmed := strings.TrimSpace(repoPath)
	if trimmed == "" || path.IsAbs(trimmed) {
		return ""
	}
	cleaned := path.Clean(strings.TrimPrefix(trimmed, "./"))
	if cleaned == "." || cleaned == "" || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return ""
	}
	return cleaned
}

func defaultRepoPath(task repo.ProjectTask, artifact taskplan.PlannedArtifact) string {
	kindDir := strings.ReplaceAll(strings.TrimSpace(artifact.Kind), "_", "-")
	if kindDir == "" {
		kindDir = "planning-artifact"
	}
	base := normalizeSlug(strings.TrimSpace(artifact.Slug))
	if base == "" {
		base = normalizeSlug(strings.TrimSpace(artifact.Title))
	}
	if base == "" {
		base = "artifact"
	}
	prefix := fmt.Sprintf("oc-%d", task.TaskNumber)
	if task.TaskNumber <= 0 {
		prefix = "task-" + strings.ToLower(task.ID.String()[:8])
	}
	return path.Join("planning", kindDir, prefix+"-"+base+".md")
}

func resolveArtifactPath(repoRoot, repoPath string) (string, error) {
	root, err := filepath.Abs(strings.TrimSpace(repoRoot))
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(repoPath))
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path escapes repo root")
	}
	return target, nil
}

func scaffoldContent(now time.Time, task repo.ProjectTask, plan taskplan.Plan, artifact taskplan.PlannedArtifact) string {
	if plan.Playbook == taskplan.PlaybookDiscovery {
		return discoveryScaffoldContent(now, task, plan, artifact)
	}
	return genericScaffoldContent(now, task, plan, artifact)
}

func genericScaffoldContent(now time.Time, task repo.ProjectTask, plan taskplan.Plan, artifact taskplan.PlannedArtifact) string {
	taskLabel := task.ID.String()
	if task.TaskNumber > 0 {
		taskLabel = fmt.Sprintf("OC-%d", task.TaskNumber)
	}
	sections := scaffoldSectionBlock(plan, artifact)
	contextDetails := []string{
		"- Project stage: " + strings.TrimSpace(plan.ProjectStage),
		"- Evidence maturity: " + strings.TrimSpace(plan.EvidenceMaturity),
		"- Risk level: " + strings.TrimSpace(plan.RiskLevel),
	}
	if plan.Playbook == taskplan.PlaybookBacklogDecomposition && plan.BacklogFormat != "" {
		contextDetails = append(contextDetails, "- Backlog format: "+plan.BacklogFormat)
	}
	return strings.TrimSpace(fmt.Sprintf(`
# %s

- Kind: %s
- Playbook: %s
- Source task: %s
- Generated at: %s

## Purpose
Replace this scaffold with the durable planning output for this artifact.

## Context
%s

%s

## Notes
- Keep decisions, trade-offs, and unresolved questions in this file so downstream work can link to it directly.
`, strings.TrimSpace(artifact.Title), strings.TrimSpace(artifact.Kind), strings.TrimSpace(plan.Playbook), taskLabel, now.UTC().Format(time.RFC3339), strings.Join(contextDetails, "\n"), sections))
}

func scaffoldSectionBlock(plan taskplan.Plan, artifact taskplan.PlannedArtifact) string {
	required := requiredSectionsForArtifact(plan, artifact.Slug)
	if len(required) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, section := range required {
		builder.WriteString("\n## ")
		builder.WriteString(formatSectionHeading(section))
		builder.WriteString("\n")
		builder.WriteString("- ")
		builder.WriteString(scaffoldPrompt(section))
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func requiredSectionsForArtifact(plan taskplan.Plan, slug string) []string {
	target := strings.TrimSpace(slug)
	for _, contract := range taskplan.ArtifactContractForPlan(plan) {
		if strings.TrimSpace(contract.Slug) != target {
			continue
		}
		return append([]string(nil), contract.RequiredSections...)
	}
	return nil
}

func formatSectionHeading(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "go/no-go checklist":
		return "Go / No-Go Checklist"
	case "icp":
		return "ICP"
	}

	normalized := strings.ReplaceAll(strings.TrimSpace(value), "-", " ")
	normalized = strings.ReplaceAll(normalized, "/", " / ")
	fields := strings.Fields(normalized)
	for i, field := range fields {
		if field == "" || field == "/" {
			continue
		}
		fields[i] = strings.ToUpper(field[:1]) + field[1:]
	}
	return strings.Join(fields, " ")
}

func scaffoldPrompt(section string) string {
	switch strings.TrimSpace(strings.ToLower(section)) {
	case "goal":
		return "State the single outcome this strategy is optimizing for."
	case "goals":
		return "State the concrete outcomes this spec must achieve."
	case "themes":
		return "Group the work into major themes or workstreams."
	case "target segments":
		return "Name the primary segments, users, or buyers this work serves."
	case "beachhead segment":
		return "Name the first segment the launch is built to win decisively."
	case "icp":
		return "Define the ideal customer profile in enough detail to guide targeting and messaging."
	case "launch scope":
		return "State what this launch includes, excludes, and which release surface it covers."
	case "not serving":
		return "List the excluded segments, use cases, or requests and why they stay out of scope."
	case "core capabilities":
		return "Describe the capabilities the chosen direction depends on."
	case "options":
		return "List the credible options that were considered."
	case "tradeoffs":
		return "Make the upside, downside, and deliberate sacrifices explicit."
	case "decision":
		return "Record the chosen direction or answer."
	case "rationale":
		return "Explain why this choice wins now."
	case "owner", "owners":
		return "Name the directly accountable owner or owners."
	case "north star":
		return "Define the single metric this initiative is ultimately trying to move."
	case "input metrics":
		return "Define the controllable leading metrics that should move the north star."
	case "health metrics":
		return "Capture the quality, reliability, or retention metrics that keep growth from hiding damage."
	case "counter metrics":
		return "List the guardrail metrics that must not degrade while the north star improves."
	case "positioning":
		return "State the positioning the market should remember after the launch."
	case "scope slices":
		return "Break the parent scope into the smallest independently completable slices."
	case "stories":
		return "List the backlog items in the team's preferred story shape."
	case "user stories":
		return "List the backlog items in user-story form without losing delivery detail."
	case "job stories":
		return "List the backlog items in job-story form using situation, motivation, and outcome."
	case "why":
		return "Explain why each item exists and what outcome it protects."
	case "what":
		return "Describe the concrete deliverable, change, or behavior for each item."
	case "acceptance criteria":
		return "Write testable pass/fail acceptance criteria for every item; do not leave them implied."
	case "key metrics":
		return "Define the leading and lagging metrics that will show this is working."
	case "defensibility":
		return "Describe the moat, or say explicitly that no defensibility exists yet."
	case "milestones":
		return "Break the work or outcome into major checkpoints."
	case "risks":
		return "List the material risks that could invalidate the plan."
	case "major risks":
		return "List the major risks that could stop, damage, or materially delay the initiative."
	case "severity":
		return "Classify each risk by severity so the team can prioritize mitigation and review."
	case "impact":
		return "Describe the business, customer, or operational impact if the risk lands."
	case "failure modes":
		return "Describe how this initiative could fail in the real world before launch or rollout."
	case "triggers":
		return "Call out the signals or conditions that would indicate the failure mode is becoming real."
	case "responses":
		return "Record the concrete response the team should take if the risk starts materializing."
	case "hypotheses":
		return "Label assumptions that still need validation; do not present them as facts."
	case "open questions":
		return "List unresolved questions, missing inputs, or decisions still pending."
	case "non-goals":
		return "State the explicit exclusions to prevent scope creep."
	case "scope":
		return "Describe what is in scope for this phase."
	case "constraints":
		return "Capture the technical, business, legal, or time constraints."
	case "success metrics":
		return "Define measurable outcomes and thresholds for success."
	case "phasing":
		return "Split the work into phases, or state why a single phase is sufficient."
	case "rollout":
		return "Describe rollout sequencing, launch controls, and fallback expectations."
	case "scenarios":
		return "List the primary user or system scenarios that must pass."
	case "edge cases":
		return "Call out important edge conditions and failure modes."
	case "verification":
		return "Describe how this will be tested or verified."
	case "proof":
		return "List the proof points, evidence, or product truths that make the messaging credible."
	case "events":
		return "List the events, properties, or telemetry needed to measure the framework."
	case "qa":
		return "Describe how the team will verify the instrumentation and reporting before relying on it."
	case "views":
		return "Describe the dashboard views or scorecards each operating audience needs."
	case "slices":
		return "Call out the cuts, segments, or cohorts the dashboard must support."
	case "alerts":
		return "Define the thresholds or alerts that should trigger review or action."
	case "schedule":
		return "Set the operating review cadence and when the team will look at the metrics."
	case "thresholds":
		return "Record the ranges or targets that define healthy, concerning, and critical movement."
	case "messaging":
		return "Write the core launch messages and how they differ by audience or channel."
	case "dependencies":
		return "List upstream or downstream dependencies and external commitments."
	case "channel strategy":
		return "Describe the channel mix, role of each channel, and why it fits the target segment."
	case "launch timeline":
		return "Lay out the launch sequence, milestones, and timing owners will manage against."
	case "readiness":
		return "List the readiness checks that must be satisfied before the team launches."
	case "approvals":
		return "Record the approvals or sign-offs required before launch."
	case "contingency":
		return "Describe what the team will do if launch assumptions fail or timing slips."
	case "expansion plan":
		return "Explain how the team will expand after the beachhead segment if launch signals are strong."
	case "order":
		return "Define the delivery order and why that sequence is safest."
	case "design input":
		return "Identify the items that need design, content, or UX input before execution."
	case "technical spikes":
		return "List the research or enabling work needed before the team can estimate or commit."
	case "technical notes":
		return "Capture implementation notes, constraints, or architecture considerations that affect delivery."
	case "mitigations":
		return "Describe how each material dependency or delivery risk will be managed."
	case "dates":
		return "Capture the due dates or checkpoints for each mitigation."
	case "go/no-go checklist":
		return "Record the go / no-go checklist the team must satisfy before sign-off."
	case "blockers":
		return "List any unresolved blockers that prevent a go decision today."
	case "rollback":
		return "Describe the rollback or containment plan if the rollout must stop."
	case "exit criteria":
		return "State what must be true before the team can call the work complete."
	case "release gates":
		return "List approvals, rollout checks, or launch controls required before release."
	default:
		return "Capture the durable decision for this section."
	}
}

func discoveryScaffoldContent(now time.Time, task repo.ProjectTask, plan taskplan.Plan, artifact taskplan.PlannedArtifact) string {
	taskLabel := task.ID.String()
	if task.TaskNumber > 0 {
		taskLabel = fmt.Sprintf("OC-%d", task.TaskNumber)
	}

	mode := taskplan.NormalizeDiscoveryMode(plan.DiscoveryMode)
	if mode == "" {
		mode = taskplan.DiscoveryModeNewProduct
	}

	var body string
	switch strings.TrimSpace(artifact.Slug) {
	case "problem-brief":
		body = strings.TrimSpace(`
## Problem
- Describe the customer problem and why it matters now.

## Target User
- Name the user or buyer segment and what context they are in.

## Evidence Gaps
- Call out what is still unknown and what evidence is missing before the team should commit scope.
`)
	case "research-plan":
		body = strings.TrimSpace(`
## Objectives
- State the decisions this discovery pass must unlock.

## Methods
- List the research methods, interview format, or evidence collection approach you will use.

## Participants
- Name the participant profile, sample size, or internal data sources you will rely on.
`)
	case "assumption-log":
		body = strings.TrimSpace(`
## Assumptions
- Record the highest-risk assumptions the team is making today.

## Risks
- Note what could go wrong if those assumptions are wrong.

## Open Questions
- Capture unresolved questions that still block a confident decision.
`)
	case "validation-plan":
		if mode == taskplan.DiscoveryModeExistingProduct {
			body = strings.TrimSpace(`
## Ideas Explored
- Capture the current product problems, workflow changes, or solution directions considered.

## Assumptions
- Record the assumptions about current user behavior and the likely causes behind it.

## Validation Experiments
- Define experiments on observed usage before expanding scope or committing delivery work.

## Prior Feedback
- Summarize the support tickets, customer feedback, research notes, or win/loss evidence that already exists.

## Instrumentation Baseline
- Note the events, funnels, cohorts, dashboards, or telemetry gaps needed to measure the change.

## Decision Framework
- State the continue / pivot / stop criteria and who will make the call once the evidence comes back.
`)
		} else {
			body = strings.TrimSpace(`
## Ideas Explored
- Capture the concepts, problem framings, or opportunity spaces explored so far.

## Assumptions
- Record the highest-risk assumptions about customer demand, willingness to try, and market reach.

## Validation Experiments
- List the experiments you will run before writing specs or committing build scope.

## Low-Cost Tests
- Prefer interviews, concierge workflows, landing pages, prototypes, or smoke tests before heavy implementation.

## Desirability Signals
- Define the desirability and market-reach evidence that proves the concept is worth pursuing further.

## Decision Framework
- State the go / no-go criteria, what would invalidate the concept, and who will make the call.
`)
		}
	default:
		return genericScaffoldContent(now, task, plan, artifact)
	}

	return strings.TrimSpace(fmt.Sprintf(`
# %s

- Kind: %s
- Playbook: %s
- Discovery mode: %s
- Source task: %s
- Generated at: %s

## Purpose
Capture durable discovery output that downstream strategy, spec, and backlog work can reference directly.

%s

## Context
- Project stage: %s
- Evidence maturity: %s
- Risk level: %s

## Notes
- Keep decisions, trade-offs, and unresolved questions in this file so downstream work can link to it directly.
`, strings.TrimSpace(artifact.Title), strings.TrimSpace(artifact.Kind), strings.TrimSpace(plan.Playbook), mode, taskLabel, now.UTC().Format(time.RFC3339), body, strings.TrimSpace(plan.ProjectStage), strings.TrimSpace(plan.EvidenceMaturity), strings.TrimSpace(plan.RiskLevel)))
}

func normalizeSlug(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	var builder strings.Builder
	lastDash := false
	for _, r := range trimmed {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func normalizeActorType(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "system"
	}
	return trimmed
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
