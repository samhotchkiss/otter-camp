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
