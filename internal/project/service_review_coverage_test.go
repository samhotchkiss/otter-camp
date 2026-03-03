package project

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestCreateFlowTemplateValidatesReviewCoverageWhenStartNodeProvided(t *testing.T) {
	sourceTemplateID := uuid.New()
	workID := uuid.New()
	doneID := uuid.New()
	startNodeID := workID

	templates := &reviewCoverageTemplateRepoStub{
		templates: make(map[uuid.UUID]repo.FlowTemplate),
	}
	nodes := &reviewCoverageNodeRepoStub{
		nodes: map[uuid.UUID]repo.FlowNode{
			workID: {
				ID:             workID,
				FlowTemplateID: sourceTemplateID,
				NodeType:       "work",
				Position:       1,
				NextNodeID:     &doneID,
			},
			doneID: {
				ID:             doneID,
				FlowTemplateID: sourceTemplateID,
				NodeType:       "done",
				Position:       2,
			},
		},
	}
	svc := &service{
		templates: templates,
		nodes:     nodes,
	}

	_, err := svc.CreateFlowTemplate(context.Background(), CreateFlowTemplateRequest{
		Slug:          "review-coverage-check",
		DisplayName:   "Review Coverage Check",
		StartNodeID:   &startNodeID,
		CreatedByType: actorSystem,
	})
	if !errors.Is(err, ErrFlowTemplateReviewPath) {
		t.Fatalf("CreateFlowTemplate err = %v, want ErrFlowTemplateReviewPath", err)
	}
	if templates.createCalls != 0 {
		t.Fatalf("Create calls = %d, want 0", templates.createCalls)
	}
}

func TestAddFlowNodeRevalidatesCoverageAndRollsBack(t *testing.T) {
	templateID := uuid.New()
	workID := uuid.New()
	reviewID := uuid.New()
	doneID := uuid.New()

	templates := &reviewCoverageTemplateRepoStub{
		templates: map[uuid.UUID]repo.FlowTemplate{
			templateID: {
				ID:          templateID,
				StartNodeID: &workID,
			},
		},
	}
	nodes := &reviewCoverageNodeRepoStub{
		nodes: map[uuid.UUID]repo.FlowNode{
			workID: {
				ID:             workID,
				FlowTemplateID: templateID,
				NodeType:       "work",
				Position:       1,
				NextNodeID:     &reviewID,
			},
			reviewID: {
				ID:             reviewID,
				FlowTemplateID: templateID,
				NodeType:       "review",
				Position:       2,
				NextNodeID:     &doneID,
				RejectNodeID:   &doneID,
			},
			doneID: {
				ID:             doneID,
				FlowTemplateID: templateID,
				NodeType:       "done",
				Position:       3,
			},
		},
	}
	svc := &service{
		templates: templates,
		nodes:     nodes,
	}

	_, err := svc.AddFlowNode(context.Background(), templateID, AddFlowNodeRequest{
		DisplayName: "Bypass Review",
		NodeType:    "work",
		Position:    4,
		NextNodeID:  &doneID,
	})
	if !errors.Is(err, ErrFlowTemplateReviewPath) {
		t.Fatalf("AddFlowNode err = %v, want ErrFlowTemplateReviewPath", err)
	}
	if nodes.deleteCalls != 1 {
		t.Fatalf("Delete calls = %d, want 1 rollback delete", nodes.deleteCalls)
	}
	if len(nodes.nodes) != 3 {
		t.Fatalf("nodes after rollback = %d, want 3", len(nodes.nodes))
	}
}

func TestRemoveFlowNodeRejectsDeleteThatBreaksReviewCoverage(t *testing.T) {
	templateID := uuid.New()
	workID := uuid.New()
	reviewID := uuid.New()
	doneID := uuid.New()

	templates := &reviewCoverageTemplateRepoStub{
		templates: map[uuid.UUID]repo.FlowTemplate{
			templateID: {
				ID:          templateID,
				StartNodeID: &workID,
			},
		},
	}
	nodes := &reviewCoverageNodeRepoStub{
		nodes: map[uuid.UUID]repo.FlowNode{
			workID: {
				ID:             workID,
				FlowTemplateID: templateID,
				NodeType:       "work",
				Position:       1,
				NextNodeID:     &reviewID,
			},
			reviewID: {
				ID:             reviewID,
				FlowTemplateID: templateID,
				NodeType:       "review",
				Position:       2,
				NextNodeID:     &doneID,
				RejectNodeID:   &doneID,
			},
			doneID: {
				ID:             doneID,
				FlowTemplateID: templateID,
				NodeType:       "done",
				Position:       3,
			},
		},
	}
	svc := &service{
		templates: templates,
		nodes:     nodes,
	}

	err := svc.RemoveFlowNode(context.Background(), reviewID)
	if !errors.Is(err, ErrFlowTemplateReviewPath) {
		t.Fatalf("RemoveFlowNode err = %v, want ErrFlowTemplateReviewPath", err)
	}
	if nodes.deleteCalls != 0 {
		t.Fatalf("Delete calls = %d, want 0", nodes.deleteCalls)
	}
	if _, getErr := nodes.GetByID(context.Background(), reviewID); getErr != nil {
		t.Fatalf("review node should remain after failed delete, got err: %v", getErr)
	}
}

type reviewCoverageTemplateRepoStub struct {
	templates   map[uuid.UUID]repo.FlowTemplate
	createCalls int
}

func (r *reviewCoverageTemplateRepoStub) Create(_ context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error) {
	r.createCalls++
	if template.ID == uuid.Nil {
		template.ID = uuid.New()
	}
	if r.templates == nil {
		r.templates = make(map[uuid.UUID]repo.FlowTemplate)
	}
	r.templates[template.ID] = template
	return template, nil
}

func (r *reviewCoverageTemplateRepoStub) GetByID(_ context.Context, id uuid.UUID) (repo.FlowTemplate, error) {
	template, ok := r.templates[id]
	if !ok {
		return repo.FlowTemplate{}, repo.ErrNotFound
	}
	return template, nil
}

func (r *reviewCoverageTemplateRepoStub) ListCurrent(context.Context, *uuid.UUID, *uuid.UUID) ([]repo.FlowTemplate, error) {
	return nil, nil
}

func (r *reviewCoverageTemplateRepoStub) Update(_ context.Context, template repo.FlowTemplate) (repo.FlowTemplate, error) {
	if r.templates == nil {
		r.templates = make(map[uuid.UUID]repo.FlowTemplate)
	}
	r.templates[template.ID] = template
	return template, nil
}

func (r *reviewCoverageTemplateRepoStub) Deprecate(_ context.Context, _ uuid.UUID, next repo.FlowTemplate) (repo.FlowTemplate, error) {
	return next, nil
}

type reviewCoverageNodeRepoStub struct {
	nodes       map[uuid.UUID]repo.FlowNode
	createCalls int
	deleteCalls int
}

func (r *reviewCoverageNodeRepoStub) Create(_ context.Context, node repo.FlowNode) (repo.FlowNode, error) {
	r.createCalls++
	if node.ID == uuid.Nil {
		node.ID = uuid.New()
	}
	if r.nodes == nil {
		r.nodes = make(map[uuid.UUID]repo.FlowNode)
	}
	r.nodes[node.ID] = node
	return node, nil
}

func (r *reviewCoverageNodeRepoStub) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	node, ok := r.nodes[id]
	if !ok {
		return repo.FlowNode{}, repo.ErrNotFound
	}
	return node, nil
}

func (r *reviewCoverageNodeRepoStub) Update(_ context.Context, node repo.FlowNode) (repo.FlowNode, error) {
	if r.nodes == nil {
		r.nodes = make(map[uuid.UUID]repo.FlowNode)
	}
	r.nodes[node.ID] = node
	return node, nil
}

func (r *reviewCoverageNodeRepoStub) Delete(_ context.Context, id uuid.UUID) error {
	r.deleteCalls++
	delete(r.nodes, id)
	return nil
}

func (r *reviewCoverageNodeRepoStub) GetByTemplateOrdered(_ context.Context, flowTemplateID uuid.UUID) ([]repo.FlowNode, error) {
	nodes := make([]repo.FlowNode, 0)
	for _, node := range r.nodes {
		if node.FlowTemplateID == flowTemplateID {
			nodes = append(nodes, node)
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Position != nodes[j].Position {
			return nodes[i].Position < nodes[j].Position
		}
		return nodes[i].ID.String() < nodes[j].ID.String()
	})
	return nodes, nil
}
