package testutil

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	skillsvc "github.com/samhotchkiss/otter-camp/internal/skill"
)

type AgentOptions struct {
	DisplayName      string
	AgentClass       string
	LifecycleStatus  string
	SystemPrompt     string
	OperatorNotes    string
	AgentType        string
	TempProjectID    *uuid.UUID
	TempTTLSeconds   *int32
	TempExpiresAt    *time.Time
	BudgetCapTokens  *int64
	BudgetPeriod     *string
	DefaultModelID   *string
	CreatedByType    string
	CreatedByID      uuid.UUID
	MemoryReadScopes []string
	ToolAllowList    []string
	ToolDenyList     []string
}

func MakeAgent(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, opts AgentOptions) *agentsvc.Agent {
	t.Helper()

	displayName := strings.TrimSpace(opts.DisplayName)
	if displayName == "" {
		displayName = "Test Agent " + uuid.NewString()[:8]
	}
	agentClass := strings.TrimSpace(opts.AgentClass)
	if agentClass == "" {
		agentClass = "staff"
	}
	lifecycleStatus := strings.TrimSpace(opts.LifecycleStatus)
	if lifecycleStatus == "" {
		if agentClass == "temp" {
			lifecycleStatus = "active"
		} else {
			lifecycleStatus = "draft"
		}
	}
	agentType := strings.TrimSpace(opts.AgentType)
	if agentType == "" {
		agentType = "worker"
	}
	createdByType := strings.TrimSpace(opts.CreatedByType)
	if createdByType == "" {
		createdByType = "system"
	}
	createdByID := opts.CreatedByID
	if createdByID == uuid.Nil {
		createdByID = uuid.Nil
	}

	memoryReadScopes := opts.MemoryReadScopes
	if len(memoryReadScopes) == 0 {
		memoryReadScopes = []string{"org", "project", "agent"}
	}

	created, err := repo.NewAgentRepo(db).Create(context.Background(), repo.Agent{
		OrganizationID:        orgID,
		DisplayName:           displayName,
		AgentClass:            agentClass,
		LifecycleStatus:       lifecycleStatus,
		SystemPrompt:          strings.TrimSpace(opts.SystemPrompt),
		OperatorInstructions:  strings.TrimSpace(opts.OperatorNotes),
		AgentType:             agentType,
		PrivateMemory:         false,
		MemoryReadScopes:      memoryReadScopes,
		ToolAllowList:         opts.ToolAllowList,
		ToolDenyList:          opts.ToolDenyList,
		DefaultModelProfileID: opts.DefaultModelID,
		BudgetCapTokens:       opts.BudgetCapTokens,
		BudgetPeriod:          opts.BudgetPeriod,
		TempProjectID:         opts.TempProjectID,
		TempTTLSeconds:        opts.TempTTLSeconds,
		TempExpiresAt:         opts.TempExpiresAt,
		CreatedByType:         createdByType,
		CreatedByID:           createdByID,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	out := agentsvc.Agent(created)
	return &out
}

func MakeSkill(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID) *skillsvc.Skill {
	t.Helper()

	slug := "skill-" + strings.ToLower(uuid.NewString()[:8])
	created, err := repo.NewSkillRepo(db).Create(context.Background(), repo.Skill{
		OrganizationID: orgID,
		Slug:           slug,
		DisplayName:    "Skill " + slug,
		Description:    "Test skill",
		FilePath:       "skills/" + slug + ".md",
		Version:        1,
		IsActive:       true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	out := skillsvc.Skill(created)
	return &out
}
