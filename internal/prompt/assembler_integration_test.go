//go:build integration

package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestPromptAssemblerIntegrationFullAssembly(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := createPromptOrg(t, ctx, pool)
	project := createPromptProject(t, ctx, pool, org.ID)
	agent := createPromptAgent(t, ctx, pool, org.ID, "You are integration test agent")
	session := createPromptSession(t, ctx, pool, org.ID, "project", project.ID)

	seedPromptMessages(t, ctx, pool, session.ID, 10)
	if _, err := repo.NewChatSummaryRepo(pool).Create(ctx, repo.ChatSummary{SessionID: session.ID, FromSequence: 1, ToSequence: 3, SummaryText: "summary for first three"}); err != nil {
		t.Fatalf("create summary: %v", err)
	}

	createPromptMemory(t, ctx, pool, org.ID, "Memory one for integration")
	createPromptMemory(t, ctx, pool, org.ID, "Memory two for integration")

	skillsRoot := t.TempDir()
	t.Setenv("OTTERCAMP_SKILLS_DIR", skillsRoot)
	skillPath := filepath.Join(skillsRoot, "skills", "test-skill.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("Skill content for integration"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	skill := createPromptSkill(t, ctx, pool, org.ID, "skills/test-skill.md")
	if _, err := repo.NewAgentSkillAttachmentRepo(pool).Attach(ctx, repo.AgentSkillAttachment{AgentID: agent.ID, SkillID: skill.ID, Priority: 1, AttachedByType: "system"}); err != nil {
		t.Fatalf("attach skill: %v", err)
	}

	assembler, err := NewPromptAssembler(AssemblerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}

	assembled, assembleErr := assembler.Assemble(ctx, AssemblyInput{SessionID: session.ID, AgentID: agent.ID})
	if assembleErr != nil {
		t.Fatalf("Assemble: %v", assembleErr)
	}

	if !strings.Contains(assembled.SystemPrompt, agent.SystemPrompt) {
		t.Fatalf("missing layer1 system prompt")
	}
	if !strings.Contains(assembled.SystemPrompt, "Skill content for integration") {
		t.Fatalf("missing layer4 skill content")
	}
	if !strings.Contains(assembled.SystemPrompt, "Memory one for integration") || !strings.Contains(assembled.SystemPrompt, "Memory two for integration") {
		t.Fatalf("missing layer5 memory content in system prompt")
	}
	if assembled.TotalTokens <= 0 {
		t.Fatalf("total tokens = %d, want > 0", assembled.TotalTokens)
	}

	history := assembled.Messages[1:]
	summaryCount := 0
	rawCount := 0
	for _, item := range history {
		if strings.HasPrefix(item.Content, "[Summary of earlier conversation]:") {
			summaryCount++
			continue
		}
		rawCount++
	}
	if summaryCount != 1 {
		t.Fatalf("summary count = %d, want 1", summaryCount)
	}
	if rawCount != 7 {
		t.Fatalf("raw count = %d, want 7", rawCount)
	}
	if len(assembled.MemoryManifest.InjectedMemoryIDs) < 2 {
		t.Fatalf("injected memories = %d, want at least 2", len(assembled.MemoryManifest.InjectedMemoryIDs))
	}
}

func TestPromptAssemblerIntegrationLayer4SkillFileRead(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := createPromptOrg(t, ctx, pool)
	agent := createPromptAgent(t, ctx, pool, org.ID, "agent")
	session := createPromptSession(t, ctx, pool, org.ID, "organization", org.ID)

	skillsRoot := t.TempDir()
	t.Setenv("OTTERCAMP_SKILLS_DIR", skillsRoot)
	skillPath := filepath.Join(skillsRoot, "skills", "test-skill.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("Layer4 file skill content"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
	skill := createPromptSkill(t, ctx, pool, org.ID, "skills/test-skill.md")
	if _, err := repo.NewAgentSkillAttachmentRepo(pool).Attach(ctx, repo.AgentSkillAttachment{AgentID: agent.ID, SkillID: skill.ID, Priority: 1, AttachedByType: "system"}); err != nil {
		t.Fatalf("attach skill: %v", err)
	}

	assembler, err := NewPromptAssembler(AssemblerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	assembled, assembleErr := assembler.Assemble(ctx, AssemblyInput{SessionID: session.ID, AgentID: agent.ID})
	if assembleErr != nil {
		t.Fatalf("Assemble: %v", assembleErr)
	}
	if !strings.Contains(assembled.SystemPrompt, "Layer4 file skill content") {
		t.Fatalf("skill file content missing from layer4")
	}
}

func TestPromptAssemblerIntegrationMemoryCooldown(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := createPromptOrg(t, ctx, pool)
	agent := createPromptAgent(t, ctx, pool, org.ID, "agent")
	session := createPromptSession(t, ctx, pool, org.ID, "organization", org.ID)
	seedPromptMessages(t, ctx, pool, session.ID, 1)

	createPromptMemory(t, ctx, pool, org.ID, strings.Repeat("A", 40))
	createPromptMemory(t, ctx, pool, org.ID, strings.Repeat("B", 40))
	createPromptMemory(t, ctx, pool, org.ID, strings.Repeat("C", 40))

	assembler, err := NewPromptAssembler(AssemblerOptions{Pool: pool, DefaultMemoryBudget: 10})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}

	first, err := assembler.Assemble(ctx, AssemblyInput{SessionID: session.ID, AgentID: agent.ID})
	if err != nil {
		t.Fatalf("first Assemble: %v", err)
	}
	if len(first.MemoryManifest.InjectedMemoryIDs) != 1 {
		t.Fatalf("first injected count = %d, want 1", len(first.MemoryManifest.InjectedMemoryIDs))
	}

	second, err := assembler.Assemble(ctx, AssemblyInput{SessionID: session.ID, AgentID: agent.ID, PreviousManifest: &first.MemoryManifest})
	if err != nil {
		t.Fatalf("second Assemble: %v", err)
	}
	if len(second.MemoryManifest.InjectedMemoryIDs) == 0 {
		t.Fatalf("second injected count = 0, want at least 1")
	}
	if second.MemoryManifest.InjectedMemoryIDs[0] == first.MemoryManifest.InjectedMemoryIDs[0] {
		t.Fatalf("cooldown failed: first and second injected same memory id %s", second.MemoryManifest.InjectedMemoryIDs[0])
	}
}

func TestPromptAssemblerIntegrationTaskScopeIncludesTaskContext(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := createPromptOrg(t, ctx, pool)
	project := createPromptProject(t, ctx, pool, org.ID)
	agent := createPromptAgent(t, ctx, pool, org.ID, "task agent")

	template := createPromptFlowTemplate(t, ctx, pool, org.ID, project.ID)
	planNode := createPromptFlowNode(t, ctx, pool, template.ID, "Plan", 1)
	implementNode := createPromptFlowNode(t, ctx, pool, template.ID, "Implement", 2)
	reviewNode := createPromptFlowNode(t, ctx, pool, template.ID, "Review", 3)

	taskRecord := createPromptTask(t, ctx, pool, org.ID, project.ID, template.ID, implementNode.ID)
	dependencyTask := createPromptDependentTask(t, ctx, pool, org.ID, project.ID)

	flowExecutions := repo.NewFlowNodeExecutionRepo(pool)
	completedExec, err := flowExecutions.Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  planNode.ID,
		VisitNumber: 1,
		Status:      "completed",
	})
	if err != nil {
		t.Fatalf("create completed flow execution: %v", err)
	}
	if completedExec.ID == uuid.Nil {
		t.Fatalf("completed flow execution id is nil")
	}
	activeExec, err := flowExecutions.Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  implementNode.ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("create active flow execution: %v", err)
	}

	subtasks := repo.NewProjectSubtaskRepo(pool)
	if _, err := subtasks.Create(ctx, repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: activeExec.ID,
		Title:               "Set up Next.js project",
		WorkStatus:          "done",
		SequenceNumber:      1,
		CreatedByType:       "system",
		CreatedByID:         ptrUUID(uuid.Nil),
	}); err != nil {
		t.Fatalf("create done subtask: %v", err)
	}
	if _, err := subtasks.Create(ctx, repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: activeExec.ID,
		Title:               "Build hero section",
		WorkStatus:          "in_progress",
		SequenceNumber:      2,
		CreatedByType:       "system",
		CreatedByID:         ptrUUID(uuid.Nil),
	}); err != nil {
		t.Fatalf("create active subtask: %v", err)
	}
	if _, err := subtasks.Create(ctx, repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: activeExec.ID,
		Title:               "Add pricing table",
		WorkStatus:          "pending",
		SequenceNumber:      3,
		CreatedByType:       "system",
		CreatedByID:         ptrUUID(uuid.Nil),
	}); err != nil {
		t.Fatalf("create pending subtask: %v", err)
	}

	if _, err := repo.NewProjectTaskDependencyRepo(pool).Add(ctx, repo.ProjectTaskDependency{
		SourceType:    "project_task",
		SourceID:      taskRecord.ID,
		DependsOnType: "project_task",
		DependsOnID:   dependencyTask.ID,
		CreatedByType: "system",
		CreatedByID:   ptrUUID(uuid.Nil),
	}); err != nil {
		t.Fatalf("create dependency: %v", err)
	}

	session := createPromptSession(t, ctx, pool, org.ID, "project_task", taskRecord.ID)
	seedPromptMessages(t, ctx, pool, session.ID, 1)

	assembler, err := NewPromptAssembler(AssemblerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	assembled, err := assembler.Assemble(ctx, AssemblyInput{SessionID: session.ID, AgentID: agent.ID})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if !strings.Contains(assembled.SystemPrompt, "[Task Context]") {
		t.Fatalf("missing [Task Context] block")
	}
	if !strings.Contains(assembled.SystemPrompt, "Task: OC-") || !strings.Contains(assembled.SystemPrompt, "Build OtterCamp sales landing page") {
		t.Fatalf("missing task title line")
	}
	if !strings.Contains(assembled.SystemPrompt, "Status: in_progress") {
		t.Fatalf("missing task status line")
	}
	if !strings.Contains(assembled.SystemPrompt, "Current Flow Step: Implement (active)") {
		t.Fatalf("missing current flow step line")
	}
	if !strings.Contains(assembled.SystemPrompt, "Subtasks:\n- [done] Set up Next.js project\n- [active] Build hero section\n- [pending] Add pricing table") {
		t.Fatalf("missing expected subtasks block")
	}
	if !strings.Contains(assembled.SystemPrompt, "Dependencies:\n- OC-") || !strings.Contains(assembled.SystemPrompt, "Finalize copy deck") {
		t.Fatalf("missing expected dependencies block")
	}
	if !strings.Contains(assembled.SystemPrompt, "Pending Flow Steps:\n- Review") {
		t.Fatalf("missing expected pending flow steps")
	}
	_ = reviewNode
}

func TestPromptAssemblerIntegrationTaskContextUsesDecomposedPrimaryDeliverable(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := createPromptOrg(t, ctx, pool)
	project := createPromptProject(t, ctx, pool, org.ID)
	agent := createPromptAgent(t, ctx, pool, org.ID, "task agent")

	template := createPromptFlowTemplate(t, ctx, pool, org.ID, project.ID)
	planNode := createPromptFlowNode(t, ctx, pool, template.ID, "Plan", 1)
	implementNode := createPromptFlowNode(t, ctx, pool, template.ID, "Implement", 2)
	createPromptFlowNode(t, ctx, pool, template.ID, "Review", 3)

	taskRecord := createPromptTask(t, ctx, pool, org.ID, project.ID, template.ID, implementNode.ID)
	fullDescription := strings.Join([]string{
		"Coordinate full migration planning for blog content and media.",
		"Rebuild taxonomy alignment for tags and categories.",
		"Validate analytics parity after URL remapping.",
	}, " ")
	taskRecord.Description = &fullDescription
	taskRecord.Metadata = json.RawMessage(`{
		"decomposition": {
			"applied": true,
			"primary_deliverable": "Coordinate full migration planning for blog content and media.",
			"source_description": "Coordinate full migration planning for blog content and media. Rebuild taxonomy alignment for tags and categories. Validate analytics parity after URL remapping."
		}
	}`)
	if _, err := repo.NewProjectTaskRepo(pool).Update(ctx, taskRecord); err != nil {
		t.Fatalf("update task metadata for decomposition: %v", err)
	}

	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  planNode.ID,
		VisitNumber: 1,
		Status:      "completed",
	}); err != nil {
		t.Fatalf("create completed flow execution: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  implementNode.ID,
		VisitNumber: 1,
		Status:      "active",
	}); err != nil {
		t.Fatalf("create active flow execution: %v", err)
	}

	session := createPromptSession(t, ctx, pool, org.ID, "project_task", taskRecord.ID)
	seedPromptMessages(t, ctx, pool, session.ID, 1)

	assembler, err := NewPromptAssembler(AssemblerOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	assembled, err := assembler.Assemble(ctx, AssemblyInput{SessionID: session.ID, AgentID: agent.ID})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if !strings.Contains(assembled.SystemPrompt, "Description: Coordinate full migration planning for blog content and media.") {
		t.Fatalf("expected focused decomposed description, prompt=%s", assembled.SystemPrompt)
	}
	if strings.Contains(assembled.SystemPrompt, "Rebuild taxonomy alignment for tags and categories.") {
		t.Fatalf("prompt includes unrelated decomposed workstream context: %s", assembled.SystemPrompt)
	}
}

func createPromptOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.Organization {
	t.Helper()
	item, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: "prompt-org-" + uuid.NewString()[:8], DisplayName: "Prompt Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return item
}

func createPromptProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Project {
	t.Helper()
	item, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           "prompt-project-" + uuid.NewString()[:8],
		DisplayName:    "Prompt Project",
		Description:    "project description",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return item
}

func createPromptFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) repo.FlowTemplate {
	t.Helper()
	project := projectID
	item, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &project,
		Slug:           "prompt-flow-" + uuid.NewString()[:8],
		DisplayName:    "Prompt Flow",
		Description:    "prompt flow",
		IsCurrent:      true,
		Version:        1,
		IsSystem:       false,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	return item
}

func createPromptFlowNode(t *testing.T, ctx context.Context, pool *pgxpool.Pool, templateID uuid.UUID, name string, position int) repo.FlowNode {
	t.Helper()
	item, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: templateID,
		DisplayName:    name,
		NodeType:       "work",
		Position:       position,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	return item
}

func createPromptTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID, flowTemplateID, flowNodeID uuid.UUID) repo.ProjectTask {
	t.Helper()
	currentFlowNodeID := flowNodeID
	templateID := flowTemplateID
	item, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:    orgID,
		ProjectID:         projectID,
		Title:             "Build OtterCamp sales landing page",
		Description:       ptrString("Create a responsive landing page for OtterCamp."),
		WorkStatus:        "in_progress",
		CurrentFlowNodeID: &currentFlowNodeID,
		FlowTemplateID:    &templateID,
		CreatedByType:     "system",
		CreatedByID:       ptrUUID(uuid.Nil),
		Metadata:          json.RawMessage(`{"acceptance_criteria":["Mobile responsive design","Page load < 2s"]}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if item.TaskNumber != 1 {
		t.Fatalf("task number = %d, want 1", item.TaskNumber)
	}
	return item
}

func createPromptDependentTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) repo.ProjectTask {
	t.Helper()
	item, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          "Finalize copy deck",
		WorkStatus:     "draft",
		CreatedByType:  "system",
		CreatedByID:    ptrUUID(uuid.Nil),
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create dependent task: %v", err)
	}
	if item.TaskNumber != 2 {
		t.Fatalf("dependent task number = %d, want 2", item.TaskNumber)
	}
	return item
}

func createPromptAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, systemPrompt string) repo.Agent {
	t.Helper()
	item, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Prompt Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         systemPrompt,
		OperatorInstructions: "",
		AgentType:            "worker",
		MemoryReadScopes:     []string{"org"},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return item
}

func createPromptSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, scopeType string, scopeID uuid.UUID) repo.ChatSession {
	t.Helper()
	item, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: orgID,
		ScopeType:      scopeType,
		ScopeID:        scopeID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return item
}

func seedPromptMessages(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID, count int) {
	t.Helper()
	messages := repo.NewChatMessageRepo(pool)
	for i := 0; i < count; i++ {
		_, err := messages.Create(ctx, repo.ChatMessage{
			SessionID:     sessionID,
			Role:          "user",
			Content:       "message-" + uuid.NewString()[:8],
			ContentFormat: "text",
			Status:        "final",
			Metadata:      json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatalf("create message %d: %v", i+1, err)
		}
	}
}

func createPromptMemory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, content string) repo.Memory {
	t.Helper()
	hash := sha256.Sum256([]byte(content))
	item, err := repo.NewMemoryRepo(pool).Create(ctx, repo.Memory{
		OrganizationID: orgID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        content,
		ContentHash:    hex.EncodeToString(hash[:]),
		Confidence:     0.8,
		UtilityScore:   0.9,
		Status:         "active",
		Sensitivity:    "normal",
		TrustTier:      0.8,
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	return item
}

func createPromptSkill(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, filePath string) repo.Skill {
	t.Helper()
	item, err := repo.NewSkillRepo(pool).Create(ctx, repo.Skill{
		OrganizationID: orgID,
		Slug:           "prompt-skill-" + uuid.NewString()[:8],
		DisplayName:    "Prompt Skill",
		Description:    "integration skill",
		FilePath:       filePath,
		Version:        1,
		IsActive:       true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	return item
}

func ptrString(value string) *string {
	copyValue := value
	return &copyValue
}

func ptrUUID(value uuid.UUID) *uuid.UUID {
	copyValue := value
	return &copyValue
}
