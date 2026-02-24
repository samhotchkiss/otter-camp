//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestFlowNodeRepoCRUDGetByTemplateOrderedAndStartNodeFK(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	flowTemplateID := seedFlowTemplateForFlowNodeTests(t, ctx, pool)
	flowNodeRepo := NewFlowNodeRepo(pool)

	created, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID:      flowTemplateID,
		DisplayName:         "Implement",
		NodeType:            "work",
		Position:            2,
		ActorID:             flowNodePtrUUID(uuid.New()),
		MCPTools:            []FlowNodeMCPTool{{ConnectionID: uuid.New(), ToolName: "mcp.execute"}},
		ToolDomains:         []string{"cli", "mcp"},
		RequiresHumanReview: false,
		MaxVisits:           10,
		Metadata:            []byte(`{"priority": "high"}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE flow_template SET start_node_id = $2 WHERE id = $1`, flowTemplateID, created.ID); err != nil {
		t.Fatalf("set flow_template.start_node_id: %v", err)
	}

	var startNodeID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT start_node_id FROM flow_template WHERE id = $1`, flowTemplateID).Scan(&startNodeID); err != nil {
		t.Fatalf("query flow_template.start_node_id: %v", err)
	}
	if startNodeID == nil || *startNodeID != created.ID {
		t.Fatalf("start_node_id = %v, want %s", startNodeID, created.ID)
	}

	loaded, err := flowNodeRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(loaded.MCPTools) != 1 || loaded.MCPTools[0].ToolName != "mcp.execute" {
		t.Fatalf("loaded mcp_tools = %+v, want one tool named mcp.execute", loaded.MCPTools)
	}
	if len(loaded.ToolDomains) != 2 || loaded.ToolDomains[0] != "cli" || loaded.ToolDomains[1] != "mcp" {
		t.Fatalf("loaded tool_domains = %#v, want [cli mcp]", loaded.ToolDomains)
	}

	updated, err := flowNodeRepo.Update(ctx, FlowNode{
		ID:                  created.ID,
		FlowTemplateID:      flowTemplateID,
		DisplayName:         "Implement Updated",
		NodeType:            "review",
		Position:            5,
		ActorType:           flowNodePtrString("human"),
		MCPTools:            []FlowNodeMCPTool{{ConnectionID: uuid.New(), ToolName: "mcp.review"}},
		ToolDomains:         []string{"file"},
		RequiresHumanReview: true,
		MaxVisits:           20,
		Metadata:            []byte(`{"priority": "critical"}`),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.DisplayName != "Implement Updated" || updated.Position != 5 || !updated.RequiresHumanReview || updated.MaxVisits != 20 {
		t.Fatalf("updated node = %+v", updated)
	}

	second, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: flowTemplateID,
		DisplayName:    "Plan",
		NodeType:       "work",
		Position:       1,
	})
	if err != nil {
		t.Fatalf("create second flow node: %v", err)
	}

	ordered, err := flowNodeRepo.GetByTemplateOrdered(ctx, flowTemplateID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(ordered) != 2 {
		t.Fatalf("ordered len = %d, want 2", len(ordered))
	}
	if ordered[0].ID != second.ID || ordered[1].ID != updated.ID {
		t.Fatalf("ordered IDs = [%s %s], want [%s %s]", ordered[0].ID, ordered[1].ID, second.ID, updated.ID)
	}
}

func TestFlowNodeRepoDeleteSetsNextNodeIDNullAndListByTemplate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	flowTemplateID := seedFlowTemplateForFlowNodeTests(t, ctx, pool)
	flowNodeRepo := NewFlowNodeRepo(pool)

	nodeB, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: flowTemplateID,
		DisplayName:    "B",
		NodeType:       "work",
		Position:       2,
	})
	if err != nil {
		t.Fatalf("create node B: %v", err)
	}

	nodeA, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: flowTemplateID,
		DisplayName:    "A",
		NodeType:       "work",
		Position:       1,
		NextNodeID:     &nodeB.ID,
	})
	if err != nil {
		t.Fatalf("create node A: %v", err)
	}

	list, err := flowNodeRepo.ListByTemplate(ctx, flowTemplateID)
	if err != nil {
		t.Fatalf("ListByTemplate: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListByTemplate len = %d, want 2", len(list))
	}

	if err := flowNodeRepo.Delete(ctx, nodeB.ID); err != nil {
		t.Fatalf("Delete node B: %v", err)
	}

	reloadedA, err := flowNodeRepo.GetByID(ctx, nodeA.ID)
	if err != nil {
		t.Fatalf("reload node A: %v", err)
	}
	if reloadedA.NextNodeID != nil {
		t.Fatalf("node A next_node_id = %v, want nil after deleting node B", *reloadedA.NextNodeID)
	}
}

func TestFlowNodeSkillRepoAttachDuplicateAndSetPosition(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	templateRepo := NewFlowTemplateRepo(pool)
	skillRepo := NewSkillRepo(pool)
	flowNodeRepo := NewFlowNodeRepo(pool)
	flowNodeSkillRepo := NewFlowNodeSkillRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "flow-skill-org", DisplayName: "Flow Skill Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "flow-skill-project",
		DisplayName:    "Flow Skill Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := templateRepo.Create(ctx, FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "flow-skill-template",
		DisplayName:    "Flow Skill Template",
		Description:    "template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	node, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Skill Node",
		NodeType:       "work",
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}

	skillA, err := skillRepo.Create(ctx, Skill{
		OrganizationID: org.ID,
		Slug:           "skill-a",
		DisplayName:    "Skill A",
		Description:    "A",
		FilePath:       "skills/a.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skill A: %v", err)
	}
	skillB, err := skillRepo.Create(ctx, Skill{
		OrganizationID: org.ID,
		Slug:           "skill-b",
		DisplayName:    "Skill B",
		Description:    "B",
		FilePath:       "skills/b.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skill B: %v", err)
	}

	if _, err := flowNodeSkillRepo.Attach(ctx, FlowNodeSkill{FlowNodeID: node.ID, SkillID: skillA.ID, Position: 10}); err != nil {
		t.Fatalf("attach skill A: %v", err)
	}
	if _, err := flowNodeSkillRepo.Attach(ctx, FlowNodeSkill{FlowNodeID: node.ID, SkillID: skillA.ID, Position: 11}); !errors.Is(err, ErrAlreadyAttached) {
		t.Fatalf("duplicate attach err = %v, want ErrAlreadyAttached", err)
	}
	if _, err := flowNodeSkillRepo.Attach(ctx, FlowNodeSkill{FlowNodeID: node.ID, SkillID: skillB.ID, Position: 20}); err != nil {
		t.Fatalf("attach skill B: %v", err)
	}

	if _, err := flowNodeSkillRepo.SetPosition(ctx, node.ID, skillB.ID, 0); err != nil {
		t.Fatalf("SetPosition skill B: %v", err)
	}

	attached, err := flowNodeSkillRepo.ListByNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("ListByNode: %v", err)
	}
	if len(attached) != 2 {
		t.Fatalf("ListByNode len = %d, want 2", len(attached))
	}
	if attached[0].SkillID != skillB.ID || attached[1].SkillID != skillA.ID {
		t.Fatalf("ListByNode order skill IDs = [%s %s], want [%s %s]", attached[0].SkillID, attached[1].SkillID, skillB.ID, skillA.ID)
	}
}

func seedFlowTemplateForFlowNodeTests(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	templateRepo := NewFlowTemplateRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "flow-node-org", DisplayName: "Flow Node Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "flow-node-project",
		DisplayName:    "Flow Node Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := templateRepo.Create(ctx, FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "flow-node-template",
		DisplayName:    "Flow Node Template",
		Description:    "template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	return template.ID
}

func flowNodePtrUUID(value uuid.UUID) *uuid.UUID {
	return &value
}

func flowNodePtrString(value string) *string {
	return &value
}
