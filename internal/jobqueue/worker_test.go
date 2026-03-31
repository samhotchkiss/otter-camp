package jobqueue

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
)

func TestClaimHeartbeatIntervalClampsAgentTurnToHeartbeatGrace(t *testing.T) {
	worker := New(nil, nil, Config{
		StaleClaimThreshold: 5 * time.Minute,
	})

	got := worker.claimHeartbeatInterval(Job{JobType: agentTurnJobType})
	want := claimedAgentTurnHeartbeatGrace / 3
	if got != want {
		t.Fatalf("claimHeartbeatInterval(agent_turn) = %v, want %v", got, want)
	}
}

func TestAgentTurnRateLimitDelayCapsProviderHintAtBackoffCap(t *testing.T) {
	got := agentTurnRateLimitDelay(1, 42*time.Hour)
	if got != agentTurnRateLimitBackoffCap {
		t.Fatalf("agentTurnRateLimitDelay(1, 42h) = %v, want %v", got, agentTurnRateLimitBackoffCap)
	}
}

func TestAgentTurnRateLimitDelayUsesProviderHintWhenBelowBackoffCap(t *testing.T) {
	hint := 10 * time.Minute
	got := agentTurnRateLimitDelay(1, hint)
	if got != hint {
		t.Fatalf("agentTurnRateLimitDelay(1, %v) = %v, want %v", hint, got, hint)
	}
}

func TestRejitteredRateLimitedRunAfterClampsOversizedRunAfter(t *testing.T) {
	now := time.Date(2026, time.March, 25, 2, 0, 0, 0, time.UTC)
	runAfter := now.Add(42 * time.Hour)
	got := rejitteredRateLimitedRunAfter(now, runAfter, uuid.Nil, uuid.Nil, 1, true)
	want := now.Add(agentTurnRateLimitBackoffCap)
	if !got.Equal(want) {
		t.Fatalf("rejitteredRateLimitedRunAfter oversized = %v, want %v", got, want)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerIncludesBlockerReuseGuidance(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(38, "Import batch review", 0, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:    "Active project id: 123",
		ActiveTaskLine: "Already-active non-terminal tasks in the tree: task 38 (Import batch review) id=aaa title=\"Import batch review\" work_status=blocked deliverable_root=content/posts depends_on_path=content/technonymous-index.json flow_template_id=ft-1 resume_policy=needs_replacement_work blocker=\"review turn repeatedly hit file.read not_found across 3 consecutive turns\"",
	})

	if !strings.Contains(prompt, "The active project id above is not a task_id.") {
		t.Fatalf("prompt = %q, want project-id-is-not-task guidance", prompt)
	}
	if !strings.Contains(prompt, "do not call flow.list_templates just to reconfirm template availability") {
		t.Fatalf("prompt = %q, want no-flow-template-rediscovery guidance", prompt)
	}
	if !strings.Contains(prompt, "act directly on that blocker summary") {
		t.Fatalf("prompt = %q, want blocker reuse guidance", prompt)
	}
	if !strings.Contains(prompt, "create or queue the smallest replacement or follow-on work needed to recover it") {
		t.Fatalf("prompt = %q, want replacement-work guidance", prompt)
	}
	if !strings.Contains(prompt, "stay inside that exact root instead of broad content, templates, or planning rediscovery") {
		t.Fatalf("prompt = %q, want deliverable-root guidance", prompt)
	}
	if !strings.Contains(prompt, "inspect that prerequisite artifact first instead of broad search") {
		t.Fatalf("prompt = %q, want dependency-path guidance", prompt)
	}
}

func TestBuildRecoveredTaskQueueKickoffMessagePrefersSourceDescriptionForLegacySatisfiedCloseout(t *testing.T) {
	description := "## Already Satisfied — No Work Needed\n\nThis child task can be closed as the deliverable already exists in the target file."
	taskRecord := repo.ProjectTask{
		Title:       "Verify planning/sambot-example-conversations.md exists and contains all 5 required conversation scenarios",
		Description: &description,
		Metadata: taskdecomp.ApplyMetadata(nil, taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "Verify the existing conversations file and record which scenarios are present.",
			Deliverables:          []string{"Write a short verification note at the bottom of planning/sambot-example-conversations.md."},
		}, "## Closeout Verification Task\n\nThe deliverable file `planning/sambot-example-conversations.md` already exists on disk. This task verifies it contains all 5 required conversation scenarios and produces a short verification summary.\n\nWrite a short verification note at the bottom of `planning/sambot-example-conversations.md` confirming all 5 scenarios are present, or note which are missing. Do not rewrite the deliverable.", nil),
	}

	message := buildRecoveredTaskQueueKickoffMessage(taskRecord)
	if !strings.Contains(message, "## Closeout Verification Task") {
		t.Fatalf("kickoff message = %q, want source_description closeout brief", message)
	}
	if strings.Contains(message, "## Already Satisfied") {
		t.Fatalf("kickoff message = %q, did not want stale already-satisfied placeholder", message)
	}
}

func TestRecoveredTaskLooksLikeOrchestrationOnlyParentIgnoresLegacySatisfiedCloseout(t *testing.T) {
	description := "## Already Satisfied — No Work Needed\n\nThis child task can be closed as the deliverable already exists in the target file."
	taskRecord := repo.ProjectTask{
		Title:       "Verify planning/sambot-example-conversations.md exists and contains all 5 required conversation scenarios",
		Description: &description,
		Metadata: taskdecomp.ApplyMetadata(nil, taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "Verify the existing conversations file and record which scenarios are present.",
			Deliverables:          []string{"Write a short verification note at the bottom of planning/sambot-example-conversations.md."},
		}, "## Closeout Verification Task\n\nThe deliverable file `planning/sambot-example-conversations.md` already exists on disk. This task verifies it contains all 5 required conversation scenarios and produces a short verification summary.\n\nWrite a short verification note at the bottom of `planning/sambot-example-conversations.md` confirming all 5 scenarios are present, or note which are missing. Do not rewrite the deliverable.", nil),
	}

	if recoveredTaskLooksLikeOrchestrationOnlyParent(taskRecord) {
		t.Fatalf("expected closeout verification task not to be treated as orchestration-only parent")
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerIncludesLeafTaskGuidance(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(56, "Import post", 0, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:        "Active project id: 123",
		ActiveTaskLine:     "Already-active non-terminal tasks in the tree: task 56 (Import post) id=aaa title=\"Import post\" work_status=in_progress deliverable_path=content/posts/post.md",
		LeafActiveTaskLine: "Active leaf tasks already have no child tasks to inspect: task 56 (Import post) leaf_task_id=aaa",
	})

	if !strings.Contains(prompt, "Active leaf tasks already have no child tasks to inspect:") {
		t.Fatalf("prompt = %q, want leaf-task snapshot", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.list(parent_task_id=...) for those named leaf tasks") {
		t.Fatalf("prompt = %q, want leaf-task guidance", prompt)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerSkipsPathHintsWhenFocusHasMissingPrerequisites(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(6657, "Write paired SamBot example conversations", 1, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:    "Active project id: 123",
		ActiveTaskLine: "Already-active non-terminal tasks in the tree: task 6657 (Write paired SamBot example conversations) id=aaa title=\"Write paired SamBot example conversations\" work_status=blocked deliverable_path=planning/sambot-example-conversations.md assigned_agent_id=worker-1 flow_template_id=ft-1",
		DraftTaskLine:  "Actionable draft tasks already in the tree: task 6659 (Casual version) id=bbb title=\"Casual version\" work_status=draft deliverable_path=planning/sambot-example-conversations.md assigned_agent_id=missing flow_template_id=ft-1",
		FocusTaskLine:  "Start from this existing actionable draft before broad rediscovery if it is still the next bounded step: task 6659 (Casual version) id=bbb title=\"Casual version\" work_status=draft deliverable_path=planning/sambot-example-conversations.md depends_on_path=planning/sambot-feature-spec.md assigned_agent_id=missing flow_template_id=ft-1",
	})

	if strings.Contains(prompt, "If a named draft task above already shows deliverable_path=..., inspect or write that exact path instead of reopening broad workspace context.") {
		t.Fatalf("prompt = %q, did not want draft deliverable-path guidance while prerequisite fields are missing", prompt)
	}
	if strings.Contains(prompt, "When the focus task already includes exact deliverable or dependency hints, use those paths directly before any broader workspace search.") {
		t.Fatalf("prompt = %q, did not want focus-task path guidance while prerequisite fields are missing", prompt)
	}
	if !strings.Contains(prompt, "Because that focus parent still has explicit prerequisite fields missing") {
		t.Fatalf("prompt = %q, want explicit prerequisite repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Even if that focus task already names deliverable_path, deliverable_root, or depends_on_path, do not inspect those paths until the prerequisite repair succeeds.") {
		t.Fatalf("prompt = %q, want explicit prerequisite-before-path guard", prompt)
	}
	if !strings.Contains(prompt, "Because flow_template_id is already present on that focus task, your next assistant action should be one narrow task.update that sets assigned_agent_id and work_status=queued on that exact task.") {
		t.Fatalf("prompt = %q, want exact prerequisite repair mutation guidance", prompt)
	}
	if !strings.Contains(prompt, "Reuse one of the already-named project assignee ids from this continuation prompt directly with task.update; do not call agent.list just to rediscover the same roster.") {
		t.Fatalf("prompt = %q, want assignee reuse guidance", prompt)
	}
}

func TestBuildProjectExecutionContinuationBoundedSizeRetryPromptForWorkerIncludesSuggestedTitles(t *testing.T) {
	prompt := buildProjectExecutionContinuationBoundedSizeRetryPromptForWorker(6660, "Expert version", 1, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:   "Active project id: 123",
		FocusTaskLine: "Current focus parent: task 6659 (Casual version) id=bbb title=\"Casual version\" work_status=draft deliverable_path=planning/sambot-example-conversations.md assigned_agent_id=missing flow_template_id=ft-1",
	}, `task 6659 (Casual version) id=bbb title="Casual version" work_status=draft deliverable_path=planning/sambot-example-conversations.md assigned_agent_id=missing flow_template_id=ft-1`, []string{
		"Produce one example conversation (5-8 turn pairs) showing SamBot in casual mode on AI/ML orchestration",
		"Produce one example conversation (5-8 turn pairs) showing SamBot in casual mode on parenting in the digital age",
	})

	if !strings.Contains(prompt, `The last bounded-size tool result already suggested a concrete split:`) {
		t.Fatalf("prompt = %q, want suggested split guidance", prompt)
	}
	if !strings.Contains(prompt, `"Produce one example conversation (5-8 turn pairs) showing SamBot in casual mode on AI/ML orchestration"`) {
		t.Fatalf("prompt = %q, want first suggested child title", prompt)
	}
	if !strings.Contains(prompt, "Do not create one replacement child that still owns the whole deliverable; create multiple bounded child tasks that follow that split directly.") {
		t.Fatalf("prompt = %q, want anti-broad-child guidance", prompt)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerIncludesBlockedOnlyStopGuidance(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(243, "Write templates/template-08-replace.html - final replacement", 0, projectExecutionContinuationSnapshotForWorker{
		ProjectLine: "Active project id: 123",
		ActiveTaskLine: strings.Join([]string{
			"Already-active non-terminal tasks in the tree:",
			`task 235 (Verify planning/sambot-tech-architecture.md meets PRD acceptance criteria) id=aaa title="Verify planning/sambot-tech-architecture.md meets PRD acceptance criteria" work_status=blocked deliverable_path=planning/sambot-tech-architecture.md resume_policy=terminal_keep_blocked blocker="flow rejection max visits exceeded"`,
			`; task 181 (API contract) id=bbb title="API contract" work_status=blocked deliverable_path=planning/sambot-architecture.md resume_policy=requires_human_continuation blocker="review approval recorded but blocked task requires direct human operator continuation"`,
		}, " "),
		LeafActiveTaskLine: "Active leaf tasks already have no child tasks to inspect: task 235 (Verify planning/sambot-tech-architecture.md meets PRD acceptance criteria) leaf_task_id=aaa; task 181 (API contract) leaf_task_id=bbb",
	})

	if !strings.Contains(prompt, "All named remaining tasks are already blocked and none are project-lane actionable.") {
		t.Fatalf("prompt = %q, want blocked-only stop guidance", prompt)
	}
	if !strings.Contains(prompt, "your next assistant message must be one concrete blocker sentence naming the blocked deliverable or human/operator dependency") {
		t.Fatalf("prompt = %q, want blocker-sentence instruction", prompt)
	}
	if !strings.Contains(prompt, "do not read that blocked deliverable from disk first just to decide what to do") {
		t.Fatalf("prompt = %q, want blocked-deliverable no-reread guidance", prompt)
	}
}

func TestShouldReuseStructuredProjectContinuationResumeForWorker(t *testing.T) {
	resumeID := uuid.New()
	resumeContent := "Continue the active project execution now. Current focus parent: task 297 (Write planning/sambot-prompts/test-conversations-level3.md)."

	if !shouldReuseStructuredProjectContinuationResumeForWorker(false, false, false, false, false, false, false, false, resumeID, resumeContent) {
		t.Fatal("structured resume should be reused when no stronger retry has been selected")
	}
	if shouldReuseStructuredProjectContinuationResumeForWorker(false, false, true, false, false, false, false, false, resumeID, resumeContent) {
		t.Fatal("structured resume should not overwrite a closeout queue retry")
	}
	if shouldReuseStructuredProjectContinuationResumeForWorker(false, true, false, false, false, false, false, false, resumeID, resumeContent) {
		t.Fatal("structured resume should not overwrite a closeout retry")
	}
	if shouldReuseStructuredProjectContinuationResumeForWorker(false, false, false, true, false, false, false, false, resumeID, resumeContent) {
		t.Fatal("structured resume should not overwrite a focused closeout retry")
	}
	if shouldReuseStructuredProjectContinuationResumeForWorker(false, false, false, false, false, false, false, false, uuid.Nil, resumeContent) {
		t.Fatal("structured resume should not be reused without a reference message id")
	}
	if shouldReuseStructuredProjectContinuationResumeForWorker(false, false, false, false, false, false, false, false, resumeID, "Continue the active project execution now.") {
		t.Fatal("structured resume should not be reused without structured context")
	}
}

func TestProjectContinuationResumeMessageHasStructuredContextForWorkerRecognizesNamedActiveAndChildDraftLines(t *testing.T) {
	t.Parallel()

	activeOnly := "Continue the active project execution now. Already-active non-terminal tasks in the tree: task 814 (Level 3 conversations) id=aaa work_status=review"
	if !projectContinuationResumeMessageHasStructuredContextForWorker(activeOnly) {
		t.Fatalf("active-only content should count as structured context")
	}

	childDraftOnly := "Continue the active project execution now. Draft parent tasks already have child work: task 6639 (AI ethics conversation) id=bbb work_status=draft"
	if !projectContinuationResumeMessageHasStructuredContextForWorker(childDraftOnly) {
		t.Fatalf("child-draft-only content should count as structured context")
	}
}

func TestProjectContinuationResumePromptMatchesSnapshotForWorkerRejectsStaleStructuredPrompt(t *testing.T) {
	t.Parallel()

	stale := "Continue the active project execution now from the continuation summary above. Already-active non-terminal tasks in the tree: task 6655 (Closeout: confirm test-conversations-level3.md exists and close parent OC-6652) id=aaa work_status=blocked. Active leaf tasks already have no child tasks to inspect: task 6655 (Closeout: confirm test-conversations-level3.md exists and close parent OC-6652) leaf_task_id=aaa."
	snapshot := projectExecutionContinuationSnapshotForWorker{
		ProjectLine:   "Active project id: 123",
		DraftTaskLine: `Actionable draft tasks already in the tree: task 6657 (Write paired SamBot example conversations) id=bbb title="Write paired SamBot example conversations" work_status=draft`,
		FocusTaskLine: `Start from this existing actionable draft before broad rediscovery if it is still the next bounded step: task 6657 (Write paired SamBot example conversations) id=bbb title="Write paired SamBot example conversations" work_status=draft`,
	}

	if projectContinuationResumePromptMatchesSnapshotForWorker(stale, snapshot) {
		t.Fatal("stale structured resume should not match refreshed snapshot")
	}
}

func TestProjectContinuationWorkspaceDeliverableEvidenceEligibleForWorkerRejectsSharedSectionChildren(t *testing.T) {
	t.Parallel()

	parentID := uuid.New()
	parentDescription := "Create planning/sambot-example-conversations.md with paired casual and expert examples."
	childDescription := "Expert version only: add the expert conversation section in planning/sambot-example-conversations.md."
	parentTask := repo.ProjectTask{
		ID:          parentID,
		TaskNumber:  6657,
		Title:       "Write paired SamBot example conversations",
		Description: &parentDescription,
		WorkStatus:  "in_progress",
	}
	childMetadata, err := json.Marshal(map[string]any{
		"decomposition_parent_task_id": parentID.String(),
	})
	if err != nil {
		t.Fatalf("marshal child metadata: %v", err)
	}
	childTask := repo.ProjectTask{
		ID:          uuid.New(),
		TaskNumber:  6660,
		Title:       "Expert version",
		Description: &childDescription,
		WorkStatus:  "draft",
		Metadata:    childMetadata,
	}
	tasksByID := map[uuid.UUID]repo.ProjectTask{
		parentTask.ID: parentTask,
		childTask.ID:  childTask,
	}

	if projectContinuationWorkspaceDeliverableEvidenceEligibleForWorker(childTask, tasksByID, "planning/sambot-example-conversations.md") {
		t.Fatal("shared-file section child should not inherit workspace deliverable-present evidence from the parent file")
	}
}

func TestBuildProjectExecutionContinuationReplacementChildRetryPromptForWorkerFallsBackToNamedSnapshotContext(t *testing.T) {
	t.Parallel()

	prompt := buildProjectExecutionContinuationReplacementChildRetryPromptForWorker(
		297,
		"Write results/verify-test-conversations-level3.md",
		0,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:          "Active project id: a6dbd331-7205-42d9-b0df-10105d5b5330",
			ActiveTaskLine:       `Already-active non-terminal tasks in the tree: task 6650 (Verify planning/sambot-prompts/test-conversations-level3.md contains exactly 3 deeply technical multi-turn SamBot test conversations) id=aaa title="Verify planning/sambot-prompts/test-conversations-level3.md contains exactly 3 deeply technical multi-turn SamBot test conversations" work_status=review deliverable_path=planning/sambot-prompts/test-conversations-level3.md`,
			ChildActiveDraftLine: `Draft parent tasks already have child work: task 814 (Write the file planning/sambot-prompts/test-conversations-level3.md containing exactly 3 deeply technical multi-turn test conversations that exercise SamBot's ability to speak as Sam Hotchkiss on hard technical subjects.) id=bbb title="Write the file planning/sambot-prompts/test-conversations-level3.md containing exactly 3 deeply technical multi-turn test conversations that exercise SamBot's ability to speak as Sam Hotchkiss on hard technical subjects." work_status=draft`,
		},
	)

	if !strings.Contains(prompt, "Already-active non-terminal tasks in the tree: task 6650") {
		t.Fatalf("prompt = %q, want active-task snapshot carried into fallback", prompt)
	}
	if !strings.Contains(prompt, "Draft parent tasks already have child work: task 814") {
		t.Fatalf("prompt = %q, want child-active draft snapshot carried into fallback", prompt)
	}
	if !strings.Contains(prompt, "Your last continuation turn was blocked after broad rediscovery even though the active-task snapshot already names the remaining blocked lanes.") {
		t.Fatalf("prompt = %q, want active-lane rediscovery retry guidance", prompt)
	}
	if !strings.Contains(prompt, "Use one direct task.update or task.create to address the blocker or resume_policy on one named task above") {
		t.Fatalf("prompt = %q, want direct blocked-lane action guidance", prompt)
	}
}

func TestBuildProjectContinuationTaskHintsForWorkerIncludesDeliverableAndDependencyHints(t *testing.T) {
	indexDescription := "Crawl technonymous.org archive pages and build post URL index. Output a JSON index at content/technonymous-index.json with title, URL, and date for each post."
	scrapeDescription := "Scrape and import technonymous.org posts from the URL index under content/posts/"
	parentDescription := "Save as results/review-path-validation-summary.md"
	parentID := uuid.New()
	childID := uuid.New()
	scrapeID := uuid.New()
	tasks := []repo.ProjectTask{
		{
			ID:          uuid.New(),
			TaskNumber:  14,
			Title:       "Build post URL index",
			WorkStatus:  "done",
			Description: &indexDescription,
		},
		{
			ID:          scrapeID,
			TaskNumber:  35,
			Title:       "Scrape batch 1",
			WorkStatus:  "blocked",
			Description: &scrapeDescription,
		},
		{
			ID:          parentID,
			TaskNumber:  37,
			Title:       "Validate review path summary",
			WorkStatus:  "blocked",
			Description: &parentDescription,
		},
		{
			ID:         childID,
			TaskNumber: 38,
			Title:      "Repair review path summary",
			WorkStatus: "draft",
			Metadata:   []byte(`{"decomposition_parent_task_id":"` + parentID.String() + `"}`),
		},
	}

	hints := buildProjectContinuationTaskHintsForWorker(tasks, map[uuid.UUID]string{
		scrapeID: "review turn repeatedly hit file.read not_found across 3 consecutive turns",
	})

	if got := hints[scrapeID].DeliverableRoot; got != "content/posts" {
		t.Fatalf("scrape deliverable root = %q, want content/posts", got)
	}
	if got := hints[scrapeID].DependsOnPath; got != "content/technonymous-index.json" {
		t.Fatalf("scrape dependency path = %q, want content/technonymous-index.json", got)
	}
	if got := hints[childID].DeliverablePath; got != "results/review-path-validation-summary.md" {
		t.Fatalf("child deliverable path = %q, want inherited parent deliverable path", got)
	}
}

func TestBuildProjectContinuationTaskHintsForWorkerUsesDecompositionSourceDescription(t *testing.T) {
	parentID := uuid.New()
	childID := uuid.New()
	parentDescription := "Close the recovered Technonymous index workstream."
	sourceDescription := "## Deliverable\nA single file: `content/technonymous-index.json`\n\n## Important\nWrite the result to `content/technonymous-index.json`."
	parentMetadata, err := json.Marshal(map[string]any{
		"decomposition": map[string]any{
			"source_description": sourceDescription,
		},
	})
	if err != nil {
		t.Fatalf("marshal parent metadata: %v", err)
	}
	tasks := []repo.ProjectTask{
		{
			ID:          parentID,
			TaskNumber:  44,
			Title:       "Produce content/technonymous-index.json by crawling technonymous.org",
			WorkStatus:  "draft",
			Description: &parentDescription,
			Metadata:    parentMetadata,
		},
		{
			ID:         childID,
			TaskNumber: 75,
			Title:      "Verify content/technonymous-index.json delivered - close parent OC-44",
			WorkStatus: "done",
			Metadata:   []byte(`{"decomposition_parent_task_id":"` + parentID.String() + `"}`),
		},
	}

	hints := buildProjectContinuationTaskHintsForWorker(tasks, nil)
	if got := hints[parentID].DeliverablePath; got != "content/technonymous-index.json" {
		t.Fatalf("parent deliverable path = %q, want content/technonymous-index.json", got)
	}
	if got := hints[childID].DeliverablePath; got != "content/technonymous-index.json" {
		t.Fatalf("child deliverable path = %q, want inherited content/technonymous-index.json", got)
	}
}

func TestBuildProjectContinuationTaskHintsForWorkerDetectsLeadingPathTitleDeliverable(t *testing.T) {
	tasks := []repo.ProjectTask{
		{
			ID:         uuid.New(),
			TaskNumber: 175,
			Title:      "sambot/widget.html (or sambot/index.html) — the frontend chat widget",
			WorkStatus: "in_progress",
		},
	}

	hints := buildProjectContinuationTaskHintsForWorker(tasks, nil)
	if got := hints[tasks[0].ID].DeliverablePath; got != "sambot/widget.html" {
		t.Fatalf("deliverable path = %q, want sambot/widget.html", got)
	}
}

func TestBuildProjectContinuationTaskHintsForWorkerDetectsParenthesizedOptionPath(t *testing.T) {
	description := "Frontend chat widget — a self-contained HTML/CSS/JS component (sambot/widget.html or similar) that renders a floating chat bubble."
	tasks := []repo.ProjectTask{
		{
			ID:          uuid.New(),
			TaskNumber:  173,
			Title:       "Frontend chat widget",
			Description: &description,
			WorkStatus:  "in_progress",
		},
	}

	hints := buildProjectContinuationTaskHintsForWorker(tasks, nil)
	if got := hints[tasks[0].ID].DeliverablePath; got != "sambot/widget.html" {
		t.Fatalf("deliverable path = %q, want sambot/widget.html", got)
	}
}

func TestProjectContinuationExplicitDeliverablePathFromTextForWorkerDetectsOutputWriteAsPath(t *testing.T) {
	text := "Output: Write the complete style block as planning/template-08-css-foundation.txt"

	if got := projectContinuationExplicitDeliverablePathFromTextForWorker(text); got != "planning/template-08-css-foundation.txt" {
		t.Fatalf("deliverable path = %q, want planning/template-08-css-foundation.txt", got)
	}
}

func TestBuildProjectContinuationTaskHintsForWorkerPrefersOutputWriteAsPathFromDescription(t *testing.T) {
	description := "Produce the CSS foundation for the final template. Output: Write the complete style block as planning/template-08-css-foundation.txt"
	tasks := []repo.ProjectTask{
		{
			ID:          uuid.New(),
			TaskNumber:  244,
			Title:       "Write Dark Mode Editorial CSS foundation",
			Description: &description,
			WorkStatus:  "blocked",
		},
	}

	hints := buildProjectContinuationTaskHintsForWorker(tasks, nil)
	if got := hints[tasks[0].ID].DeliverablePath; got != "planning/template-08-css-foundation.txt" {
		t.Fatalf("deliverable path = %q, want planning/template-08-css-foundation.txt", got)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerIncludesCompletedBatchSupersessionGuidance(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(
		67,
		"Fetch posts 25-35 from technonymous-index.json via web_fetch and save as markdown under content/posts/",
		2,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:          "Active project id: 123",
			CompletedTaskLine:    "Recently implementation-complete bounded tasks already in the tree: task 67 (Fetch posts 25-35) id=bbb work_status=done deliverable_root=content/posts depends_on_path=content/technonymous-index.json batch_range=25-35 proof_state=approved",
			ReplacementDraftLine: "Draft parent tasks need fresh replacement child work: task 44 (Replacement scrape batch) id=aaa title=\"Replacement scrape batch\" work_status=draft deliverable_root=content/posts batch_range=25-35 replaceable_blocked_child_tasks=1",
		},
	)

	if !strings.Contains(prompt, "That completed task covers batch_range=25-35.") {
		t.Fatalf("prompt = %q, want completed batch range context", prompt)
	}
	if !strings.Contains(prompt, "superseded by the latest completed batch") {
		t.Fatalf("prompt = %q, want batch supersession guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not create another replacement task for batch_range=25-35") {
		t.Fatalf("prompt = %q, want duplicate replacement guard for completed batch", prompt)
	}
	if !strings.Contains(prompt, "do not reread that prerequisite just to verify batch_range=25-35") {
		t.Fatalf("prompt = %q, want no dependency reread guidance for completed batch", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.list with status=done or other broad project filters just to verify batch_range=25-35") {
		t.Fatalf("prompt = %q, want no broad task.list verification guidance for completed batch", prompt)
	}
	if !strings.Contains(prompt, "Recently implementation-complete bounded tasks already in the tree: task 67") {
		t.Fatalf("prompt = %q, want completed batch coverage surfaced in prompt body", prompt)
	}
	if !strings.Contains(prompt, "Do not create or queue replacement work for a batch_range already listed in the completed-task snapshot above") {
		t.Fatalf("prompt = %q, want completed batch replacement suppression guidance", prompt)
	}
	if !strings.Contains(prompt, "proof_state=approved means that implementation-complete task also has a recorded review approval") {
		t.Fatalf("prompt = %q, want explicit proof-state guidance", prompt)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerIncludesCompletedCloseoutGuidance(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(75, "Verify content/technonymous-index.json delivered - close parent OC-44", 1, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:       "Active project id: 123",
		DraftTaskLine:     "Actionable draft tasks already in the tree: task 44 (Produce content/technonymous-index.json by crawling technonymous.org) id=aaa title=\"Produce content/technonymous-index.json by crawling technonymous.org\" work_status=draft deliverable_path=content/technonymous-index.json malformed_child_tasks=3 completed_closeout_child_tasks=1",
		FocusTaskLine:     "Start from this existing actionable draft before broad rediscovery if it is still the next bounded step: task 44 (Produce content/technonymous-index.json by crawling technonymous.org) id=aaa title=\"Produce content/technonymous-index.json by crawling technonymous.org\" work_status=draft deliverable_path=content/technonymous-index.json malformed_child_tasks=3 completed_closeout_child_tasks=1",
		CompletedTaskLine: "Recently implementation-complete bounded tasks already in the tree: task 75 (Verify content/technonymous-index.json delivered - close parent OC-44) id=bbb work_status=done deliverable_path=content/technonymous-index.json proof_state=approved",
	})

	if !strings.Contains(prompt, "use that completed child proof to advance or close the parent instead of creating another replacement child") {
		t.Fatalf("prompt = %q, want completed-closeout draft guidance", prompt)
	}
	if !strings.Contains(prompt, "Because that focus parent already has completed closeout child proof, advance or close the parent directly instead of creating another replacement child.") {
		t.Fatalf("prompt = %q, want completed-closeout focus guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not relist `content/posts`, reread sibling batch outputs, or re-verify the same deliverable on disk") {
		t.Fatalf("prompt = %q, want no re-verification guidance", prompt)
	}
	if strings.Contains(prompt, "Create the smallest fresh replacement child task under it now instead.") {
		t.Fatalf("prompt = %q, should not fall back to malformed-child replacement guidance once closeout proof exists", prompt)
	}
}

func TestProjectContinuationChildTaskActivityForWorkerIgnoresStaleVerificationCloseoutChildrenAfterRecordedProof(t *testing.T) {
	parentID := uuid.New()
	parentDescription := "Write planning/sambot-example-conversations.md with paired SamBot example conversations."
	staleChildDescription := "Verify planning/sambot-example-conversations.md exists and close out the parent if it is complete."
	provedChildDescription := "Verify planning/sambot-example-conversations.md exists and close out parent OC-12048 once the file is confirmed complete."

	provedChildMetadata, err := json.Marshal(map[string]any{
		"decomposition_parent_task_id": parentID.String(),
		"parent_orchestration": map[string]any{
			"integration_check": map[string]any{
				"status":  "passed",
				"summary": "planning/sambot-example-conversations.md exists and matches the verified deliverable.",
			},
			"outcome_assessment": map[string]any{
				"satisfied": true,
				"summary":   "Parent deliverable is already satisfied and can close.",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal proved child metadata: %v", err)
	}
	staleChildMetadata, err := json.Marshal(map[string]any{
		"decomposition_parent_task_id": parentID.String(),
	})
	if err != nil {
		t.Fatalf("marshal stale child metadata: %v", err)
	}

	parentTask := repo.ProjectTask{
		ID:          parentID,
		TaskNumber:  12048,
		Title:       "Write paired SamBot example conversations (casual + expert) — replacement for blocked OC-6654",
		Description: &parentDescription,
		WorkStatus:  "draft",
	}
	staleChild := repo.ProjectTask{
		ID:          uuid.New(),
		TaskNumber:  12049,
		Title:       "Verify planning/sambot-example-conversations.md exists and close out parent",
		Description: &staleChildDescription,
		WorkStatus:  "draft",
		Metadata:    staleChildMetadata,
	}
	provedChild := repo.ProjectTask{
		ID:          uuid.New(),
		TaskNumber:  12050,
		Title:       "Verify planning/sambot-example-conversations.md exists and close out parent OC-12048",
		Description: &provedChildDescription,
		WorkStatus:  "blocked",
		Metadata:    provedChildMetadata,
	}

	hints := map[uuid.UUID]projectContinuationTaskHintsForWorker{
		parentTask.ID: {DeliverablePath: "planning/sambot-example-conversations.md"},
		staleChild.ID: {DeliverablePath: "planning/sambot-example-conversations.md"},
		provedChild.ID: {
			DeliverablePath: "planning/sambot-example-conversations.md",
			ResumePolicy:    "resume_review_decision",
		},
	}

	activity := projectContinuationChildTaskActivityForWorker([]repo.ProjectTask{parentTask, staleChild, provedChild}, hints)[parentID]
	if activity.completedCloseoutChildTaskCount != 1 {
		t.Fatalf("completedCloseoutChildTaskCount = %d, want 1", activity.completedCloseoutChildTaskCount)
	}
	if activity.childTaskCount != 0 {
		t.Fatalf("childTaskCount = %d, want 0 once stale verification children are retired", activity.childTaskCount)
	}
	if activity.blockedChildTaskCount != 0 {
		t.Fatalf("blockedChildTaskCount = %d, want 0 once self-proved closeout child is retired", activity.blockedChildTaskCount)
	}
}
