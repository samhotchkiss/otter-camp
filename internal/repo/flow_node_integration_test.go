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

func TestFlowNodeRepoCRUDAndGetByTemplateOrdered(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	_, flowTemplateID := seedFlowTemplate(t, ctx, pool)
	flowTemplateRepo := NewFlowTemplateRepo(pool)
	flowNodeRepo := NewFlowNodeRepo(pool)

	nodeB, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: flowTemplateID,
		DisplayName:    "Review Node",
		NodeType:       "review",
		Position:       20,
		ToolDomains:    []string{"browser"},
	})
	if err != nil {
		t.Fatalf("create nodeB: %v", err)
	}

	nodeA, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: flowTemplateID,
		DisplayName:    "Work Node",
		NodeType:       "work",
		Position:       10,
		NextNodeID:     &nodeB.ID,
		MCPTools: []FlowNodeMCPTool{
			{
				ConnectionID: uuid.New(),
				ToolName:     "cli.execute",
			},
		},
		ToolDomains:         []string{"cli"},
		RequiresHumanReview: true,
		MaxVisits:           12,
	})
	if err != nil {
		t.Fatalf("create nodeA: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE flow_template
		SET start_node_id = $2
		WHERE id = $1
	`, flowTemplateID, nodeA.ID); err != nil {
		t.Fatalf("set flow_template.start_node_id: %v", err)
	}

	template, err := flowTemplateRepo.GetByID(ctx, flowTemplateID)
	if err != nil {
		t.Fatalf("GetByID flow_template: %v", err)
	}
	if template.StartNodeID == nil || *template.StartNodeID != nodeA.ID {
		t.Fatalf("flow_template.start_node_id = %v, want %s", template.StartNodeID, nodeA.ID)
	}

	gotA, err := flowNodeRepo.GetByID(ctx, nodeA.ID)
	if err != nil {
		t.Fatalf("GetByID nodeA: %v", err)
	}
	if gotA.NextNodeID == nil || *gotA.NextNodeID != nodeB.ID {
		t.Fatalf("nodeA.next_node_id = %v, want %s", gotA.NextNodeID, nodeB.ID)
	}
	if len(gotA.MCPTools) != 1 || gotA.MCPTools[0].ToolName != "cli.execute" {
		t.Fatalf("nodeA.mcp_tools = %+v, want cli.execute", gotA.MCPTools)
	}
	if len(gotA.ToolDomains) != 1 || gotA.ToolDomains[0] != "cli" {
		t.Fatalf("nodeA.tool_domains = %+v, want [cli]", gotA.ToolDomains)
	}

	gotA.DisplayName = "Work Node Updated"
	gotA.Position = 30
	updatedA, err := flowNodeRepo.Update(ctx, gotA)
	if err != nil {
		t.Fatalf("Update nodeA: %v", err)
	}
	if updatedA.DisplayName != "Work Node Updated" || updatedA.Position != 30 {
		t.Fatalf("updated nodeA = %+v", updatedA)
	}

	ordered, err := flowNodeRepo.GetByTemplateOrdered(ctx, flowTemplateID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(ordered) != 2 {
		t.Fatalf("ordered len = %d, want 2", len(ordered))
	}
	if ordered[0].ID != nodeB.ID || ordered[1].ID != nodeA.ID {
		t.Fatalf("ordered IDs = [%s %s], want [%s %s]", ordered[0].ID, ordered[1].ID, nodeB.ID, nodeA.ID)
	}
}

func TestFlowNodeDeleteSetsReferencingNextNodeIDNull(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	_, flowTemplateID := seedFlowTemplate(t, ctx, pool)
	flowNodeRepo := NewFlowNodeRepo(pool)

	nodeB, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: flowTemplateID,
		DisplayName:    "Node B",
		NodeType:       "review",
		Position:       20,
	})
	if err != nil {
		t.Fatalf("create nodeB: %v", err)
	}
	nodeA, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: flowTemplateID,
		DisplayName:    "Node A",
		NodeType:       "work",
		Position:       10,
		NextNodeID:     &nodeB.ID,
	})
	if err != nil {
		t.Fatalf("create nodeA: %v", err)
	}

	if err := flowNodeRepo.Delete(ctx, nodeB.ID); err != nil {
		t.Fatalf("Delete nodeB: %v", err)
	}

	gotA, err := flowNodeRepo.GetByID(ctx, nodeA.ID)
	if err != nil {
		t.Fatalf("GetByID nodeA after delete: %v", err)
	}
	if gotA.NextNodeID != nil {
		t.Fatalf("nodeA.next_node_id after deleting nodeB = %v, want nil", gotA.NextNodeID)
	}
}

func TestFlowNodeSkillRepoAttachDuplicateAndReorder(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgID, flowTemplateID := seedFlowTemplate(t, ctx, pool)
	flowNodeRepo := NewFlowNodeRepo(pool)
	flowNodeSkillRepo := NewFlowNodeSkillRepo(pool)
	skillRepo := NewSkillRepo(pool)

	node, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: flowTemplateID,
		DisplayName:    "Skill Node",
		NodeType:       "work",
		Position:       10,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	skillA, err := skillRepo.Create(ctx, Skill{
		OrganizationID: orgID,
		Slug:           "skill-a",
		DisplayName:    "Skill A",
		Description:    "A",
		FilePath:       "skills/skill-a.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skillA: %v", err)
	}
	skillB, err := skillRepo.Create(ctx, Skill{
		OrganizationID: orgID,
		Slug:           "skill-b",
		DisplayName:    "Skill B",
		Description:    "B",
		FilePath:       "skills/skill-b.md",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create skillB: %v", err)
	}

	attachA, err := flowNodeSkillRepo.Attach(ctx, node.ID, skillA.ID, 20)
	if err != nil {
		t.Fatalf("attach skillA: %v", err)
	}
	attachB, err := flowNodeSkillRepo.Attach(ctx, node.ID, skillB.ID, 10)
	if err != nil {
		t.Fatalf("attach skillB: %v", err)
	}

	if _, err := flowNodeSkillRepo.Attach(ctx, node.ID, skillA.ID, 30); !errors.Is(err, ErrAlreadyAttached) {
		t.Fatalf("duplicate attach error = %v, want ErrAlreadyAttached", err)
	}

	ordered, err := flowNodeSkillRepo.ListByNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("ListByNode: %v", err)
	}
	if len(ordered) != 2 {
		t.Fatalf("ListByNode len = %d, want 2", len(ordered))
	}
	if ordered[0].ID != attachB.ID || ordered[1].ID != attachA.ID {
		t.Fatalf("initial order = [%s %s], want [%s %s]", ordered[0].ID, ordered[1].ID, attachB.ID, attachA.ID)
	}

	if _, err := flowNodeSkillRepo.SetPosition(ctx, attachA.ID, 0); err != nil {
		t.Fatalf("SetPosition attachA: %v", err)
	}

	reordered, err := flowNodeSkillRepo.ListByNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("ListByNode reordered: %v", err)
	}
	if reordered[0].ID != attachA.ID {
		t.Fatalf("reordered first id = %s, want %s", reordered[0].ID, attachA.ID)
	}

	if err := flowNodeSkillRepo.Detach(ctx, attachB.ID); err != nil {
		t.Fatalf("Detach attachB: %v", err)
	}
	remaining, err := flowNodeSkillRepo.ListByNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("ListByNode after detach: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != attachA.ID {
		t.Fatalf("remaining attachments = %+v, want only attachA", remaining)
	}
}

func seedFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()

	orgRepo := NewOrgRepo(pool)
	flowTemplateRepo := NewFlowTemplateRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "flow-node-org-" + uuid.NewString(),
		DisplayName: "Flow Node Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	template, err := flowTemplateRepo.Create(ctx, FlowTemplate{
		OrganizationID: &org.ID,
		Slug:           "flow-template-" + uuid.NewString()[:8],
		DisplayName:    "Flow Template",
		Description:    "Flow node integration tests",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	return org.ID, template.ID
}
