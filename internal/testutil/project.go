package testutil

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type MakeTaskOptions struct {
	Title               string
	Description         *string
	FlowTemplateID      *uuid.UUID
	ScheduleID          *uuid.UUID
	AssignedAgentID     *uuid.UUID
	RequiresHumanReview *bool
	WorkStatus          string
	CreatedByType       string
	CreatedByID         *uuid.UUID
	Metadata            json.RawMessage
}

func MakeProject(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID) *project.Project {
	t.Helper()

	created, err := repo.NewProjectRepo(db).Create(context.Background(), repo.Project{
		OrganizationID: orgID,
		Slug:           "project-" + strings.ToLower(uuid.NewString()[:8]),
		DisplayName:    "Project " + uuid.NewString()[:8],
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	out := project.Project(created)
	return &out
}

func MakeFlowTemplate(t testing.TB, db *pgxpool.Pool, projectID uuid.UUID, nodeCount int) *project.FlowTemplate {
	t.Helper()

	ctx := context.Background()
	projectRecord, err := repo.NewProjectRepo(db).GetByID(ctx, projectID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	if nodeCount <= 0 {
		nodeCount = 1
	}

	templateRecord, err := repo.NewFlowTemplateRepo(db).Create(ctx, repo.FlowTemplate{
		OrganizationID: &projectRecord.OrganizationID,
		ProjectID:      &projectID,
		Slug:           "flow-" + strings.ToLower(uuid.NewString()[:8]),
		DisplayName:    "Flow " + uuid.NewString()[:8],
		Description:    "test flow template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	nodeRepo := repo.NewFlowNodeRepo(db)
	nodes := make([]repo.FlowNode, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		node, createErr := nodeRepo.Create(ctx, repo.FlowNode{
			FlowTemplateID: templateRecord.ID,
			DisplayName:    "Node " + uuid.NewString()[:8],
			NodeType:       "work",
			Position:       i + 1,
			MCPTools:       []repo.FlowNodeMCPTool{},
			ToolDomains:    []string{},
			MaxVisits:      10,
		})
		if createErr != nil {
			t.Fatalf("create flow node %d: %v", i+1, createErr)
		}
		nodes = append(nodes, node)
	}

	for i := 0; i < len(nodes)-1; i++ {
		nodes[i].NextNodeID = &nodes[i+1].ID
		if _, err := nodeRepo.Update(ctx, nodes[i]); err != nil {
			t.Fatalf("update flow node %d next pointer: %v", i+1, err)
		}
	}

	templateRecord.StartNodeID = &nodes[0].ID
	templateRecord, err = repo.NewFlowTemplateRepo(db).Update(ctx, templateRecord)
	if err != nil {
		t.Fatalf("set flow template start node: %v", err)
	}

	out := project.FlowTemplate(templateRecord)
	return &out
}

func MakeTask(t testing.TB, db *pgxpool.Pool, projectID uuid.UUID, opts MakeTaskOptions) *project.Task {
	t.Helper()

	ctx := context.Background()
	projectRecord, err := repo.NewProjectRepo(db).GetByID(ctx, projectID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "Task " + uuid.NewString()[:8]
	}
	workStatus := strings.TrimSpace(opts.WorkStatus)
	if workStatus == "" {
		workStatus = "draft"
	}
	createdByType := strings.TrimSpace(opts.CreatedByType)
	if createdByType == "" {
		createdByType = "system"
	}

	var createdByID *uuid.UUID
	if opts.CreatedByID != nil {
		createdByID = opts.CreatedByID
	}

	taskRecord, err := repo.NewProjectTaskRepo(db).Create(ctx, repo.ProjectTask{
		OrganizationID:      projectRecord.OrganizationID,
		ProjectID:           projectID,
		Title:               title,
		Description:         opts.Description,
		WorkStatus:          workStatus,
		FlowTemplateID:      opts.FlowTemplateID,
		ScheduleID:          opts.ScheduleID,
		AssignedAgentID:     opts.AssignedAgentID,
		RequiresHumanReview: opts.RequiresHumanReview != nil && *opts.RequiresHumanReview,
		CreatedByType:       createdByType,
		CreatedByID:         createdByID,
		Metadata:            opts.Metadata,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	out := project.Task(taskRecord)
	return &out
}
