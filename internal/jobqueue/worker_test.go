package jobqueue

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
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

func TestBuildProjectExecutionContinuationPromptForWorkerIncludesCompletedBatchSupersessionGuidance(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(
		67,
		"Fetch posts 25-35 from technonymous-index.json via web_fetch and save as markdown under content/posts/",
		2,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:          "Active project id: 123",
			CompletedTaskLine:    "Recently completed bounded tasks already in the tree: task 67 (Fetch posts 25-35) id=bbb work_status=done deliverable_root=content/posts depends_on_path=content/technonymous-index.json batch_range=25-35",
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
	if !strings.Contains(prompt, "Recently completed bounded tasks already in the tree: task 67") {
		t.Fatalf("prompt = %q, want completed batch coverage surfaced in prompt body", prompt)
	}
	if !strings.Contains(prompt, "Do not create or queue replacement work for a batch_range already listed in the completed-task snapshot above") {
		t.Fatalf("prompt = %q, want completed batch replacement suppression guidance", prompt)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerIncludesCompletedCloseoutGuidance(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(75, "Verify content/technonymous-index.json delivered - close parent OC-44", 1, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:       "Active project id: 123",
		DraftTaskLine:     "Actionable draft tasks already in the tree: task 44 (Produce content/technonymous-index.json by crawling technonymous.org) id=aaa title=\"Produce content/technonymous-index.json by crawling technonymous.org\" work_status=draft deliverable_path=content/technonymous-index.json malformed_child_tasks=3 completed_closeout_child_tasks=1",
		FocusTaskLine:     "Start from this existing actionable draft before broad rediscovery if it is still the next bounded step: task 44 (Produce content/technonymous-index.json by crawling technonymous.org) id=aaa title=\"Produce content/technonymous-index.json by crawling technonymous.org\" work_status=draft deliverable_path=content/technonymous-index.json malformed_child_tasks=3 completed_closeout_child_tasks=1",
		CompletedTaskLine: "Recently completed bounded tasks already in the tree: task 75 (Verify content/technonymous-index.json delivered - close parent OC-44) id=bbb work_status=done deliverable_path=content/technonymous-index.json",
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
