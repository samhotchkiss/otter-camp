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

func TestBuildRecoveredTaskQueueKickoffMessagePrefersSourceDescriptionForReplacementShellChild(t *testing.T) {
	description := "Write technical SamBot test conversations — fresh replacement 4-6"
	taskRecord := repo.ProjectTask{
		Title:       "Write technical SamBot test conversations — fresh replacement 4-6",
		Description: &description,
		Metadata: taskdecomp.ApplyMetadata(nil, taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "planning/sambot-prompts/test-conversations-technical.md",
		}, "## Deliverable\nWrite `planning/sambot-prompts/test-conversations-technical.md` — six complete multi-turn test conversations demonstrating SamBot handling technical topics.\n\n## Requirements\nCover the six required scenarios and keep the full file consistent.\n\n## File Instructions\nWrite the complete file to `planning/sambot-prompts/test-conversations-technical.md`.", nil),
	}

	message := buildRecoveredTaskQueueKickoffMessage(taskRecord)
	if !strings.Contains(message, "## Deliverable") {
		t.Fatalf("kickoff message = %q, want structured source_description contract", message)
	}
	if strings.Contains(message, "Task description:\nWrite technical SamBot test conversations — fresh replacement 4-6") {
		t.Fatalf("kickoff message = %q, did not want shallow replacement-shell description", message)
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

func TestBuildRecoveredTaskQueueKickoffMessageForCloseoutReadyOrchestrationParent(t *testing.T) {
	description := "Write planning/sambot-example-conversations.md containing paired example conversations."
	metadata, err := json.Marshal(map[string]any{
		"decomposition": map[string]any{
			"orchestration_only": true,
		},
		"parent_orchestration": map[string]any{
			"integration_check": map[string]any{
				"status": "passed",
			},
			"outcome_assessment": map[string]any{
				"satisfied": true,
			},
			"child_verifications": []map[string]any{
				{
					"task_id": uuid.NewString(),
					"summary": "verified",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	taskRecord := repo.ProjectTask{
		Title:       "Write paired SamBot example conversations (casual + expert)",
		Description: &description,
		Metadata:    metadata,
	}

	message := buildRecoveredTaskQueueKickoffMessage(taskRecord)
	if !strings.Contains(message, "already has recorded parent_orchestration closeout evidence") {
		t.Fatalf("kickoff message = %q, want closeout-ready parent guidance", message)
	}
	if !strings.Contains(message, "Your next assistant action should be flow.advance") {
		t.Fatalf("kickoff message = %q, want flow.advance guidance", message)
	}
	if strings.Contains(message, "create or repair bounded executable child tasks beneath this parent") {
		t.Fatalf("kickoff message = %q, did not want child-creation guidance once closeout proof exists", message)
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

func TestBuildProjectExecutionContinuationPromptForWorkerSkipsMissingPrerequisitesForReplacementParent(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(246, "Write planning/sambot-personality-spec.md", 1, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:          "Active project id: 123",
		ActiveTaskLine:       "Already-active non-terminal tasks in the tree: task 250 (Include example exchanges) id=aaa title=\"Include example exchanges\" work_status=blocked deliverable_path=planning/sambot-personality-spec.md assigned_agent_id=worker-1 flow_template_id=ft-1 blocker=\"shared deliverable\"",
		ReplacementDraftLine: "Draft parent tasks need fresh replacement child work: task 246 (Write planning/sambot-personality-spec.md) id=bbb title=\"Write planning/sambot-personality-spec.md\" work_status=draft deliverable_path=planning/sambot-personality-spec.md assigned_agent_id=missing flow_template_id=missing child_tasks=3 replaceable_blocked_child_tasks=3 malformed_child_tasks=1",
		FocusTaskLine:        "Start from this existing actionable draft before broad rediscovery if it is still the next bounded step: task 246 (Write planning/sambot-personality-spec.md) id=bbb title=\"Write planning/sambot-personality-spec.md\" work_status=draft deliverable_path=planning/sambot-personality-spec.md assigned_agent_id=missing flow_template_id=missing child_tasks=3 replaceable_blocked_child_tasks=3 malformed_child_tasks=1",
	})

	if strings.Contains(prompt, "Because that focus parent still has explicit prerequisite fields missing") {
		t.Fatalf("prompt = %q, did not want missing-prerequisite priority guidance for a replacement parent", prompt)
	}
	if strings.Contains(prompt, "Repair the named assigned_agent_id / flow_template_id gaps on that exact task with one narrow task.update") {
		t.Fatalf("prompt = %q, did not want direct task.update prerequisite guidance for a replacement parent", prompt)
	}
	if !strings.Contains(prompt, "Your next assistant action must create the smallest fresh replacement child task beneath task 246") {
		t.Fatalf("prompt = %q, want replacement-child guidance to remain dominant", prompt)
	}
	if strings.Contains(prompt, "Reuse one of the already-named project assignee ids") {
		t.Fatalf("prompt = %q, did not want assignee reuse guidance for a replacement parent", prompt)
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

func TestBuildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorkerPrefersReplacementChildPromptWhenChildDraftsExist(t *testing.T) {
	t.Parallel()

	parentID := uuid.NewString()
	childAID := uuid.NewString()
	childBID := uuid.NewString()

	prompt := buildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorker(
		12525,
		"Write 6 technical test conversations for SamBot",
		4,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:          "Active project id: 123",
			FocusTaskLine:        `Current focus parent: task 12526 (Write 6 technical test conversations for SamBot) id=` + parentID + ` title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
			ChildActiveDraftLine: `Draft parent tasks already have child work: task 12527 (Write technical test conversations 1-3) id=` + childAID + ` title="Write technical test conversations 1-3" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md; task 12528 (Write technical test conversations 4-6) id=` + childBID + ` title="Write technical test conversations 4-6" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
		},
		`task 12526 (Write 6 technical test conversations for SamBot) id=`+parentID+` title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
		[]string{"Write technical test conversations 1-3", "Write technical test conversations 4-6"},
	)

	if !strings.Contains(prompt, "focused draft parent already has direct child work to advance") {
		t.Fatalf("prompt = %q, want replacement-child retry guidance", prompt)
	}
	if strings.Contains(prompt, "still too broad to queue as-is") {
		t.Fatalf("prompt = %q, did not want generic bounded-size retry guidance once child drafts exist", prompt)
	}
	if !strings.Contains(prompt, "Those direct child lanes are not individually named yet in this prompt.") {
		t.Fatalf("prompt = %q, want unnamed direct-child guidance", prompt)
	}
	if !strings.Contains(prompt, "task.list(parent_task_id="+parentID+")") {
		t.Fatalf("prompt = %q, want narrow direct-child inspection guidance", prompt)
	}
	if strings.Contains(prompt, "one narrow task.update on one already-named direct child task above") {
		t.Fatalf("prompt = %q, did not want direct child task.update before child ids are named", prompt)
	}
}

func TestBuildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorkerPrefersRepairDraftPromptWhenRepairDraftExists(t *testing.T) {
	t.Parallel()

	parentID := uuid.NewString()
	repairID := uuid.NewString()

	prompt := buildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorker(
		12525,
		"Write 6 technical test conversations for SamBot",
		4,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:     "Active project id: 123",
			FocusTaskLine:   `Current focus parent: task 12526 (Write 6 technical test conversations for SamBot) id=` + parentID + ` title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md malformed_child_tasks=3`,
			RepairDraftLine: `Preferred existing same-deliverable malformed child draft to repair before any new replacement work: task 12527 (Write technical test conversations 1-3) id=` + repairID + ` title="Write technical test conversations 1-3" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
		},
		`task 12526 (Write 6 technical test conversations for SamBot) id=`+parentID+` title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
		[]string{"Conversation 1", "Conversation 2"},
	)

	if !strings.Contains(prompt, "Current focus parent: task 12527 (Write technical test conversations 1-3)") {
		t.Fatalf("prompt = %q, want repair child promoted to focus", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with task.list(parent_task_id=...), task.get, or phrases like 'Let me inspect it first' or 'Let me check the current children first'.") {
		t.Fatalf("prompt = %q, want explicit no-reread guidance for repair child retry", prompt)
	}
	if !strings.Contains(prompt, "Your last continuation turn proved that the named repair child is still too broad to queue as-is.") {
		t.Fatalf("prompt = %q, want repair-child bounded-size lead-in", prompt)
	}
	if !strings.Contains(prompt, "Your next assistant action must split task 12527 (Write technical test conversations 1-3)") {
		t.Fatalf("prompt = %q, want repair-child bounded split guidance", prompt)
	}
	if !strings.Contains(prompt, "The last bounded-size tool result already suggested a concrete split:") {
		t.Fatalf("prompt = %q, want bounded-size split hint retained", prompt)
	}
}

func TestBuildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorkerKeepsChildBoundedSizeFocus(t *testing.T) {
	t.Parallel()

	parentID := uuid.NewString()
	childAID := uuid.NewString()
	childBID := uuid.NewString()

	prompt := buildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorker(
		12525,
		"Write 6 technical test conversations for SamBot",
		4,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:          "Active project id: 123",
			DraftTaskLine:        `Actionable draft tasks already in the tree: task 12527 (Write technical test conversations 1-3) id=` + childAID + ` title="Write technical test conversations 1-3" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md; task 12528 (Write technical test conversations 4-6) id=` + childBID + ` title="Write technical test conversations 4-6" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
			ChildActiveDraftLine: `Draft parent tasks already have child work: task 12526 (Write 6 technical test conversations for SamBot) id=` + parentID + ` title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md child_tasks=3`,
		},
		"task 12527 (Write technical test conversations 1-3)",
		[]string{"Conversation 1", "Conversation 2"},
	)

	if !strings.Contains(prompt, "still too broad to queue as-is") {
		t.Fatalf("prompt = %q, want bounded-size child focus guidance", prompt)
	}
	if strings.Contains(prompt, "focused draft parent already has direct child work to advance") {
		t.Fatalf("prompt = %q, did not want parent-level replacement-child guidance for specific child bounded retry", prompt)
	}
	if !strings.Contains(prompt, "Current focus parent: task 12527 (Write technical test conversations 1-3)") {
		t.Fatalf("prompt = %q, want specific child focus label", prompt)
	}
}

func TestBuildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorkerKeepsUnresolvedChildLabelFocus(t *testing.T) {
	t.Parallel()

	parentID := uuid.NewString()

	prompt := buildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorker(
		12525,
		"Write 6 technical test conversations for SamBot",
		4,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:          "Active project id: 123",
			ChildActiveDraftLine: `Draft parent tasks already have child work: task 12526 (Write 6 technical test conversations for SamBot) id=` + parentID + ` title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md child_tasks=3`,
		},
		"task 12527",
		[]string{"Conversation 1", "Conversation 2"},
	)

	if !strings.Contains(prompt, "still too broad to queue as-is") {
		t.Fatalf("prompt = %q, want bounded-size child focus guidance", prompt)
	}
	if strings.Contains(prompt, "focused draft parent already has direct child work to advance") {
		t.Fatalf("prompt = %q, did not want parent-level replacement-child guidance for unresolved child label", prompt)
	}
	if !strings.Contains(prompt, "Current focus parent: task 12527.") {
		t.Fatalf("prompt = %q, want raw child label preserved", prompt)
	}
}

func TestBuildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorkerCanonicalizesMalformedOverrideWithTaskID(t *testing.T) {
	t.Parallel()

	parentID := uuid.NewString()

	prompt := buildProjectExecutionContinuationBoundedRetryPromptFromSnapshotForWorker(
		12651,
		"Write planning/sambot-prompts/test-conversations-technical.md — 6 SamBot technical test conversations (final replacement)",
		1,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:   "Active project id: a6dbd331-7205-42d9-b0df-10105d5b5330",
			FocusTaskLine: `Current focus parent: task 12532 (Write SamBot test conversation: Startup CTO asks about AI agent orchestration) id=` + parentID + ` title="Write SamBot test conversation: Startup CTO asks about AI agent orchestration" work_status=draft deliverable_path=content/sambot/test-conversations/01-startup-cto-ai-orchestration.md malformed_child_tasks=2`,
		},
		`Startup CTO asks about AI agent orchestration) id=`+parentID+` title="Write SamBot test conversation: Startup CTO asks about AI agent orchestration" work_status=draft deliverable_path=content/sambot/test-conversations/01-startup-cto-ai-orchestration.md assigned_agent_id=missing flow_template_id=9a60dfee-1fc7-4e3a-bd05-f9da1bb97552 malformed_child_tasks=2`,
		nil,
	)

	if !strings.Contains(prompt, "Current focus parent: task 12532 (Write SamBot test conversation: Startup CTO asks about AI agent orchestration)") {
		t.Fatalf("prompt = %q, want canonical snapshot focus ref", prompt)
	}
	if strings.Contains(prompt, "Current focus parent: Startup CTO asks about AI agent orchestration)") {
		t.Fatalf("prompt = %q, did not want malformed raw override focus", prompt)
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

func TestBuildProjectExecutionContinuationRediscoveryRetryPromptForWorkerPrefersBoundedRepairChildWhenLatestBoundedContextExists(t *testing.T) {
	t.Parallel()

	parentID := uuid.NewString()
	repairID := uuid.NewString()

	prompt := buildProjectExecutionContinuationRediscoveryRetryPromptForWorker(
		12525,
		"Write 6 technical test conversations for SamBot",
		4,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:     "Active project id: 123",
			FocusTaskLine:   `Current focus parent: task 12526 (Write 6 technical test conversations for SamBot) id=` + parentID + ` title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md malformed_child_tasks=1 workspace_deliverable_present=true`,
			RepairDraftLine: `Preferred existing same-deliverable malformed child draft to repair before any new replacement work: task 12527 (Write technical test conversations 1-3) id=` + repairID + ` title="Write technical test conversations 1-3" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
		},
		`task 12527 (Write technical test conversations 1-3) id=`+repairID+` title="Write technical test conversations 1-3" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
		[]string{"Conversation 1", "Conversation 2"},
	)

	if !strings.Contains(prompt, "Your last continuation turn proved that the named repair child is still too broad to queue as-is.") {
		t.Fatalf("prompt = %q, want bounded repair-child rediscovery retry guidance", prompt)
	}
	if !strings.Contains(prompt, "Current focus parent: task 12527 (Write technical test conversations 1-3)") {
		t.Fatalf("prompt = %q, want repair child as rediscovery retry focus", prompt)
	}
	if strings.Contains(prompt, "create the smallest closeout/verification child task beneath task 12526") {
		t.Fatalf("prompt = %q, did not want parent closeout retry guidance once bounded repair child context exists", prompt)
	}
}

func TestProjectContinuationMalformedSameDeliverableDraftChildrenForWorkerIncludesInheritedSharedFileTopicChild(t *testing.T) {
	t.Parallel()

	parentID := uuid.New()
	childID := uuid.New()

	parentDescription := "Write planning/sambot-prompts/test-conversations-technical.md with six technical conversation scenarios."
	childDescription := "Technical conversation scenarios 1-3 for planning/sambot-prompts/test-conversations-technical.md."
	childMetadata, err := json.Marshal(map[string]any{
		"decomposition_parent_task_id": parentID.String(),
	})
	if err != nil {
		t.Fatalf("marshal child metadata: %v", err)
	}

	parent := repo.ProjectTask{
		ID:          parentID,
		TaskNumber:  12526,
		Title:       "Write 6 technical test conversations for SamBot",
		Description: &parentDescription,
		WorkStatus:  "draft",
	}
	child := repo.ProjectTask{
		ID:          childID,
		TaskNumber:  12527,
		Title:       "Technical test conversations 1-3",
		Description: &childDescription,
		WorkStatus:  "draft",
		Metadata:    childMetadata,
	}

	malformed := projectContinuationMalformedSameDeliverableDraftChildrenForWorker(
		[]repo.ProjectTask{parent, child},
		parent,
		map[uuid.UUID]struct{}{childID: {}},
	)
	if len(malformed) != 1 || malformed[0].ID != childID {
		t.Fatalf("malformed same-deliverable children = %+v, want child %s included", malformed, childID)
	}
}

func TestProjectContinuationMalformedSameDeliverableDraftChildrenForWorkerSkipsConflictingDuplicateSharedFileChild(t *testing.T) {
	t.Parallel()

	parentTaskID := uuid.New()
	childTaskID := uuid.New()
	parentDescription := "Create the SamBot technical test conversations at planning/sambot-prompts/test-conversations-technical.md."
	childDescription := "## Deliverable\n\n**File:** `planning/sambot-prompts/test-conversations-technical.md`\n\nWrite exactly 6 multi-turn test conversations in planning/sambot-prompts/test-conversations-technical.md."
	childMetadata, err := json.Marshal(map[string]any{
		"decomposition_parent_task_id": parentTaskID.String(),
		"decomposition": map[string]any{
			"source_description":  "Write a single SamBot test conversation file at sambot/test-conversations/tech-01-ai-orchestration.md.",
			"primary_deliverable": "Write a single SamBot test conversation file at sambot/test-conversations/tech-01-ai-orchestration.md.",
		},
	})
	if err != nil {
		t.Fatalf("marshal child metadata: %v", err)
	}

	tasks := []repo.ProjectTask{
		{
			ID:          parentTaskID,
			TaskNumber:  12526,
			Title:       "Write planning/sambot-prompts/test-conversations-technical.md",
			Description: &parentDescription,
			WorkStatus:  "draft",
		},
		{
			ID:          childTaskID,
			TaskNumber:  12527,
			Title:       "Write technical test conversations 1-3",
			Description: &childDescription,
			WorkStatus:  "draft",
			Metadata:    childMetadata,
		},
	}
	malformedChildTaskIDs := projectContinuationMalformedChildTaskIDsForWorker(tasks)
	if _, ok := malformedChildTaskIDs[childTaskID]; !ok {
		t.Fatalf("malformedChildTaskIDs missing conflicting duplicate child")
	}
	malformed := projectContinuationMalformedSameDeliverableDraftChildrenForWorker(tasks, tasks[0], malformedChildTaskIDs)
	if len(malformed) != 0 {
		t.Fatalf("malformed same-deliverable children = %v, want none for conflicting duplicate child", malformed)
	}
}

func TestProjectContinuationOwnDeliverableHintsForWorkerPreferDecompositionSourcePath(t *testing.T) {
	t.Parallel()

	childMetadata, err := json.Marshal(map[string]any{
		"decomposition": map[string]any{
			"source_description":  "Write a single SamBot test conversation file at sambot/test-conversations/tech-01-ai-orchestration.md.",
			"primary_deliverable": "Write a single SamBot test conversation file at sambot/test-conversations/tech-01-ai-orchestration.md.",
		},
	})
	if err != nil {
		t.Fatalf("marshal child metadata: %v", err)
	}
	task := repo.ProjectTask{
		Title:      "Test Conversations 2-3: Ethics researcher + Engineering manager hiring signal",
		WorkStatus: "draft",
		Metadata:   childMetadata,
	}

	explicit, root := projectContinuationOwnDeliverableHintsForWorker(task)
	if explicit != "sambot/test-conversations/tech-01-ai-orchestration.md" {
		t.Fatalf("explicit = %q, want decomposition-source deliverable path", explicit)
	}
	if root != "" {
		t.Fatalf("root = %q, want empty root when explicit path is available", root)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerPrefersNamedChildDraftActionOverRelisting(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(12525, "Write 6 technical test conversations for SamBot", 3, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:          "Active project id: 123",
		DraftTaskLine:        `Actionable draft tasks already in the tree: task 12527 (Write technical test conversations 1-3) id=aaa title="Write technical test conversations 1-3" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md; task 12528 (Write technical test conversations 4-6) id=bbb title="Write technical test conversations 4-6" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
		ChildActiveDraftLine: `Draft parent tasks already have child work: task 12526 (Write 6 technical test conversations for SamBot) id=parent title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md child_tasks=3`,
	})

	if !strings.Contains(prompt, "Your last continuation turn was blocked after broad rediscovery even though the next bounded work was already named.") {
		t.Fatalf("prompt = %q, want strict handoff guidance", prompt)
	}
	if strings.Contains(prompt, "Recently completed work may have unlocked the next wave of bounded tasks.") {
		t.Fatalf("prompt = %q, did not want generic continuation summary", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.list without parent_task_id, task.get, file.list, or file.read before acting.") {
		t.Fatalf("prompt = %q, want no-broad-rediscovery guard", prompt)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerDefaultsToReplacementChildRetryWhenChildDraftsExist(t *testing.T) {
	prompt := buildProjectExecutionContinuationPromptForWorker(12525, "Write 6 technical test conversations for SamBot", 3, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:          "Active project id: 123",
		ChildActiveDraftLine: `Draft parent tasks already have child work: task 12526 (Write 6 technical test conversations for SamBot) id=parent title="Write 6 technical test conversations for SamBot" work_status=draft deliverable_path=planning/sambot-prompts/test-conversations-technical.md child_tasks=3`,
	})

	if !strings.Contains(prompt, "focused draft parent already has direct child work to advance") {
		t.Fatalf("prompt = %q, want replacement-child retry guidance", prompt)
	}
	if strings.Contains(prompt, "Recently completed work may have unlocked the next wave of bounded tasks.") {
		t.Fatalf("prompt = %q, did not want generic continuation summary", prompt)
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

func TestProjectContinuationResumePromptMatchesSnapshotForWorkerRejectsBroadBoundedPromptWhenChildDraftsNowExist(t *testing.T) {
	t.Parallel()

	stale := "Continue the active project execution now. Your last continuation turn proved that this focused draft is still too broad to queue as-is. Current focus parent: task 12532 (Write SamBot test conversation: Startup CTO asks about AI agent orchestration) id=focus."
	snapshot := projectExecutionContinuationSnapshotForWorker{
		ProjectLine:          "Active project id: 123",
		ChildActiveDraftLine: `Draft parent tasks already have child work: task 12639 (Write Turn 1 (User): Startup CTO introduces themselves and asks about multi-agent systems) id=child title="Write Turn 1 (User): Startup CTO introduces themselves and asks about multi-agent systems" work_status=draft`,
		FocusTaskLine:        `Start from this existing actionable draft before broad rediscovery if it is still the next bounded step: task 12532 (Write SamBot test conversation: Startup CTO asks about AI agent orchestration) id=focus title="Write SamBot test conversation: Startup CTO asks about AI agent orchestration" work_status=draft`,
	}

	if projectContinuationResumePromptMatchesSnapshotForWorker(stale, snapshot) {
		t.Fatal("stale broad bounded-size resume should not match once child draft work exists in the current worker snapshot")
	}
}

func TestProjectContinuationSnapshotWaitsOnActiveTaskWorkForWorker(t *testing.T) {
	t.Parallel()

	snapshot := projectExecutionContinuationSnapshotForWorker{
		ActiveTaskLine: strings.Join([]string{
			"Already-active non-terminal tasks in the tree:",
			`task 12672 (Write technical SamBot test conversations 4-6 — completing the remaining 3 of 6) id=aaa work_status=in_progress deliverable_path=planning/sambot-prompts/test-conversations-technical.md`,
			`; task 12668 (Write technical SamBot test conversations — fresh replacement 1-3) id=bbb work_status=blocked resume_policy=terminal_keep_blocked`,
		}, " "),
		LeafActiveTaskLine: "Active leaf tasks already have no child tasks to inspect: task 12672 leaf_task_id=aaa; task 12668 leaf_task_id=bbb",
	}

	if !projectContinuationSnapshotWaitsOnActiveTaskWorkForWorker(0, snapshot) {
		t.Fatal("expected PM to wait when only active task work plus blocked residue remains")
	}
	if projectContinuationSnapshotHasRemainingWorkForWorker(0, snapshot) {
		t.Fatal("expected no remaining PM work when only active task work plus blocked residue remains")
	}
}

func TestProjectContinuationSnapshotWaitsOnActiveTaskWorkForWorkerRequiresNoActionableDrafts(t *testing.T) {
	t.Parallel()

	snapshot := projectExecutionContinuationSnapshotForWorker{
		ActiveTaskLine: `Already-active non-terminal tasks in the tree: task 12672 id=aaa work_status=in_progress`,
		DraftTaskLine:  `Actionable draft tasks already in the tree: task 12673 id=bbb work_status=draft`,
	}

	if projectContinuationSnapshotWaitsOnActiveTaskWorkForWorker(0, snapshot) {
		t.Fatal("did not expect PM wait when actionable drafts still exist")
	}
	if !projectContinuationSnapshotHasRemainingWorkForWorker(1, snapshot) {
		t.Fatal("expected remaining PM work when actionable drafts still exist")
	}
}

func TestProjectContinuationBlockedTaskIsSupersededSameDeliverableResidueForWorker(t *testing.T) {
	t.Parallel()

	satisfied := map[string]struct{}{
		"planning/sambot-prompts/test-conversations-technical.md": {},
	}
	task := repo.ProjectTask{WorkStatus: "blocked"}
	hints := projectContinuationTaskHintsForWorker{
		DeliverablePath: "planning/sambot-prompts/test-conversations-technical.md",
	}

	if !projectContinuationBlockedTaskIsSupersededSameDeliverableResidueForWorker(task, hints, satisfied) {
		t.Fatal("expected blocked same-deliverable task to be treated as superseded residue once a satisfied lane exists")
	}
}

func TestProjectContinuationSatisfiedDeliverablePathsForWorkerUsesDoneAndOutcomeSatisfiedTasks(t *testing.T) {
	t.Parallel()

	metadata, err := json.Marshal(map[string]any{
		"parent_orchestration": map[string]any{
			"outcome_assessment": map[string]any{"satisfied": true},
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	doneTask := repo.ProjectTask{ID: uuid.New(), WorkStatus: "done"}
	satisfiedTask := repo.ProjectTask{
		ID:         uuid.New(),
		WorkStatus: "blocked",
		Metadata:   metadata,
	}
	hints := map[uuid.UUID]projectContinuationTaskHintsForWorker{
		doneTask.ID:      {DeliverablePath: "planning/sambot-prompts/test-conversations-technical.md"},
		satisfiedTask.ID: {DeliverablePath: "planning/sambot-personality-tone.md"},
	}

	paths := projectContinuationSatisfiedDeliverablePathsForWorker([]repo.ProjectTask{doneTask, satisfiedTask}, hints)
	if _, ok := paths["planning/sambot-prompts/test-conversations-technical.md"]; !ok {
		t.Fatal("expected done task deliverable path to count as satisfied")
	}
	if _, ok := paths["planning/sambot-personality-tone.md"]; !ok {
		t.Fatal("expected outcome-satisfied task deliverable path to count as satisfied")
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
	parentID := uuid.NewString()

	prompt := buildProjectExecutionContinuationReplacementChildRetryPromptForWorker(
		297,
		"Write results/verify-test-conversations-level3.md",
		0,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:          "Active project id: a6dbd331-7205-42d9-b0df-10105d5b5330",
			ActiveTaskLine:       `Already-active non-terminal tasks in the tree: task 6650 (Verify planning/sambot-prompts/test-conversations-level3.md contains exactly 3 deeply technical multi-turn SamBot test conversations) id=aaa title="Verify planning/sambot-prompts/test-conversations-level3.md contains exactly 3 deeply technical multi-turn SamBot test conversations" work_status=review deliverable_path=planning/sambot-prompts/test-conversations-level3.md`,
			ChildActiveDraftLine: `Draft parent tasks already have child work: task 814 (Write the file planning/sambot-prompts/test-conversations-level3.md containing exactly 3 deeply technical multi-turn test conversations that exercise SamBot's ability to speak as Sam Hotchkiss on hard technical subjects.) id=` + parentID + ` title="Write the file planning/sambot-prompts/test-conversations-level3.md containing exactly 3 deeply technical multi-turn test conversations that exercise SamBot's ability to speak as Sam Hotchkiss on hard technical subjects." work_status=draft`,
		},
	)

	if !strings.Contains(prompt, "focused draft parent already has direct child work to advance") {
		t.Fatalf("prompt = %q, want direct-child retry guidance", prompt)
	}
	if !strings.Contains(prompt, "Current focus parent: task 814") {
		t.Fatalf("prompt = %q, want focused child lane reference", prompt)
	}
	if !strings.Contains(prompt, "Those direct child lanes are not individually named yet in this prompt.") {
		t.Fatalf("prompt = %q, want unnamed-child guidance", prompt)
	}
	if !strings.Contains(prompt, "task.list(parent_task_id="+parentID+")") {
		t.Fatalf("prompt = %q, want narrow direct-child task.list guidance", prompt)
	}
	if strings.Contains(prompt, "one narrow task.update on one already-named direct child task above") {
		t.Fatalf("prompt = %q, did not want direct child task.update before naming child ids", prompt)
	}
}

func TestBuildProjectExecutionContinuationDirectChildActionRetryPromptForWorkerUsesNamedChildRefs(t *testing.T) {
	t.Parallel()

	prompt := buildProjectExecutionContinuationDirectChildActionRetryPromptForWorker(
		12644,
		"Write SamBot example conversation: Engineering manager hiring evaluation",
		1,
		projectExecutionContinuationSnapshotForWorker{
			ProjectLine:          "Active project id: 123",
			FocusTaskLine:        `Current focus parent: task 12643 (Write SamBot example conversation: Ethics researcher inquiry) id=fe14406e-c2ed-4471-923e-ec8225f7b2e0 title="Write SamBot example conversation: Ethics researcher inquiry" work_status=draft deliverable_path=planning/sambot-example-conversations-ethics-researcher.md`,
			ChildActiveDraftLine: `Draft parent tasks already have child work: task 12643 (Write SamBot example conversation: Ethics researcher inquiry) id=fe14406e-c2ed-4471-923e-ec8225f7b2e0 title="Write SamBot example conversation: Ethics researcher inquiry" work_status=draft deliverable_path=planning/sambot-example-conversations-ethics-researcher.md active_child_tasks=1 malformed_child_tasks=1`,
		},
		[]string{
			`task 12648 (Draft ethics researcher inquiry conversation (turns 1-5)) id=bd632279-c9f1-419e-bfd2-37acb3dc1e23 work_status=in_progress`,
			`task 12649 (Draft ethics researcher inquiry conversation (turns 6-10 + wrap-up)) id=3866c817-772a-46ad-a1f7-d1772ac23810 work_status=blocked`,
		},
	)

	if !strings.Contains(prompt, "already inspected the focused parent's direct child lanes") {
		t.Fatalf("prompt = %q, want prior-inspection lead-in", prompt)
	}
	if !strings.Contains(prompt, "Named direct child lanes from the last inspection:") {
		t.Fatalf("prompt = %q, want named child lane list", prompt)
	}
	if !strings.Contains(prompt, "task 12648 (Draft ethics researcher inquiry conversation (turns 1-5))") {
		t.Fatalf("prompt = %q, want first named child ref", prompt)
	}
	if !strings.Contains(prompt, "Do not inspect child lanes again for task_id=fe14406e-c2ed-4471-923e-ec8225f7b2e0 from the project lane.") {
		t.Fatalf("prompt = %q, want no-repeat child inspection guidance", prompt)
	}
	if !strings.Contains(prompt, "Those named direct child lanes are already active or otherwise non-draft") {
		t.Fatalf("prompt = %q, want active-child wait guidance", prompt)
	}
	if !strings.Contains(prompt, "no tool call is allowed in that turn") {
		t.Fatalf("prompt = %q, want no-tool blocker guidance", prompt)
	}
	if strings.Contains(prompt, "Your next assistant action must be one narrow task.update on one named direct child task above") {
		t.Fatalf("prompt = %q, did not want task.update guidance when every named child lane is non-draft", prompt)
	}
	if strings.Contains(prompt, "task 12609") {
		t.Fatalf("prompt = %q, did not want unrelated task refs", prompt)
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

	if !strings.Contains(prompt, "already closeout-ready, but the project lane cannot jump it directly from draft to done") {
		t.Fatalf("prompt = %q, want closeout-ready parent queue guidance", prompt)
	}
	if !strings.Contains(prompt, "If the parent_orchestration evidence is already recorded on task 44") {
		t.Fatalf("prompt = %q, want direct parent queue instruction", prompt)
	}
	if strings.Contains(prompt, "Create the smallest fresh replacement child task under it now instead.") {
		t.Fatalf("prompt = %q, should not fall back to malformed-child replacement guidance once closeout proof exists", prompt)
	}
}

func TestBuildProjectExecutionContinuationPromptForWorkerPrefersParentQueueRetryForBlockedCloseoutReadyFocus(t *testing.T) {
	parentID := uuid.NewString()

	prompt := buildProjectExecutionContinuationPromptForWorker(12532, "Write SamBot test conversation: Startup CTO asks about AI agent orchestration", 0, projectExecutionContinuationSnapshotForWorker{
		ProjectLine:    "Active project id: 123",
		ActiveTaskLine: `Already-active non-terminal tasks in the tree: task 12526 (Write 6 technical test conversations for SamBot) id=` + parentID + ` title="Write 6 technical test conversations for SamBot" work_status=blocked deliverable_path=planning/sambot-prompts/test-conversations-technical.md outcome_satisfied=true completed_closeout_child_tasks=2 blocker="duplicates older terminal lanes"`,
		FocusTaskLine:  `Start from this existing actionable draft before broad rediscovery if it is still the next bounded step: task 12526 (Write 6 technical test conversations for SamBot) id=` + parentID + ` title="Write 6 technical test conversations for SamBot" work_status=blocked deliverable_path=planning/sambot-prompts/test-conversations-technical.md outcome_satisfied=true completed_closeout_child_tasks=2 blocker="duplicates older terminal lanes"`,
	})

	if !strings.Contains(prompt, "Your last continuation turn confirmed this focused parent is already closeout-ready, but the project lane cannot jump it directly from draft to done.") {
		t.Fatalf("prompt = %q, want blocked closeout-ready parent queue guidance", prompt)
	}
	if !strings.Contains(prompt, "If the parent_orchestration evidence is already recorded on task 12526") {
		t.Fatalf("prompt = %q, want direct parent queue instruction", prompt)
	}
	if strings.Contains(prompt, "Let me check what's actually on disk") {
		t.Fatalf("prompt = %q, did not want generic rediscovery framing", prompt)
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

func TestBuildRecoveredTaskQueueKickoffMessageIncludesDirectWriteCheckpointInstruction(t *testing.T) {
	description := strings.Join([]string{
		"Create templates/layout-08-portfolio.html — a portfolio showcase layout template.",
		"",
		"IMPORTANT: If file_write fails or is intercepted, use cli_execute with python3:",
		"```",
		"python3 -c \"print('fallback')\"",
		"```",
	}, "\n")
	metadata, err := json.Marshal(map[string]any{
		"agent_turn_validation_guard": map[string]any{
			"failure_code": "recovery_target_focus_required",
		},
		"recovery_file_write_checkpoint": map[string]any{
			"version":               1,
			"target_path":           "templates/layout-08-portfolio.html",
			"failure_reason":        "content_required",
			"prior_failure_reasons": []string{"recovery_target_focus_required"},
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	message := buildRecoveredTaskQueueKickoffMessage(repo.ProjectTask{
		Title:       "Write templates/layout-08-portfolio.html — replacement for blocked OC-22",
		Description: &description,
		Metadata:    metadata,
	})
	if !strings.Contains(message, "Recovery already narrowed this task to `templates/layout-08-portfolio.html`.") {
		t.Fatalf("message = %q, want narrowed checkpoint instruction", message)
	}
	if !strings.Contains(message, "Do not begin with file.list, file.read, planning-artifact rereads, or design-consistency discovery.") {
		t.Fatalf("message = %q, want anti-discovery instruction", message)
	}
	if !strings.Contains(message, "must begin immediately with `<!DOCTYPE html>` or the opening `<html` tag") {
		t.Fatalf("message = %q, want body-start requirement", message)
	}
	if !strings.Contains(message, "do not emit file.write with only `path`") {
		t.Fatalf("message = %q, want path-only write ban", message)
	}
	if !strings.Contains(message, "file.write using both `path` and `content`") {
		t.Fatalf("message = %q, want direct file.write instruction", message)
	}
	if strings.Contains(message, "If file_write fails or is intercepted") || strings.Contains(message, "cli_execute with python3") {
		t.Fatalf("message = %q, want fallback scaffold stripped from recovered kickoff", message)
	}
}
