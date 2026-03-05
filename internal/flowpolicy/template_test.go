package flowpolicy

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestValidateExecutableFlowNodesRejectsEmptyNodes(t *testing.T) {
	if err := ValidateExecutableFlowNodes(nil); !errors.Is(err, ErrExecutableFlowPath) {
		t.Fatalf("ValidateExecutableFlowNodes err = %v, want ErrExecutableFlowPath", err)
	}
}

func TestValidateExecutableFlowNodesRejectsWorkOnlyFlow(t *testing.T) {
	nodes := []repo.FlowNode{
		flowNode(uuid.New(), "work", nil, nil),
	}

	if err := ValidateExecutableFlowNodes(nodes); !errors.Is(err, ErrExecutableFlowPath) {
		t.Fatalf("ValidateExecutableFlowNodes err = %v, want ErrExecutableFlowPath", err)
	}
}

func TestValidateExecutableFlowNodesRejectsReviewOnlyFlow(t *testing.T) {
	nodes := []repo.FlowNode{
		flowNode(uuid.New(), "review", nil, nil),
	}

	if err := ValidateExecutableFlowNodes(nodes); !errors.Is(err, ErrExecutableFlowPath) {
		t.Fatalf("ValidateExecutableFlowNodes err = %v, want ErrExecutableFlowPath", err)
	}
}

func TestValidateExecutableFlowTemplateAcceptsWorkReviewDonePath(t *testing.T) {
	doneID := uuid.New()
	reviewID := uuid.New()
	workID := uuid.New()
	nodes := []repo.FlowNode{
		flowNode(workID, "work", &reviewID, nil),
		flowNode(reviewID, "review", &doneID, nil),
		flowNode(doneID, "done", nil, nil),
	}

	if err := ValidateExecutableFlowTemplate(&workID, nodes); err != nil {
		t.Fatalf("ValidateExecutableFlowTemplate err = %v, want nil", err)
	}
}

func TestValidateExecutableFlowNodesRejectsTerminalNodeWithPendingUnreviewedWork(t *testing.T) {
	reviewID := uuid.New()
	terminalWorkID := uuid.New()
	workID := uuid.New()
	nodes := []repo.FlowNode{
		flowNode(workID, "work", &reviewID, nil),
		flowNode(reviewID, "review", &terminalWorkID, nil),
		flowNode(terminalWorkID, "work", nil, nil),
	}

	if err := ValidateExecutableFlowNodes(nodes); !errors.Is(err, ErrExecutableFlowPath) {
		t.Fatalf("ValidateExecutableFlowNodes err = %v, want ErrExecutableFlowPath", err)
	}
}

func TestValidateExecutableFlowTemplateAcceptsRejectLoopThroughReReview(t *testing.T) {
	doneID := uuid.New()
	reviewID := uuid.New()
	workID := uuid.New()
	nodes := []repo.FlowNode{
		flowNode(workID, "work", &reviewID, nil),
		flowNode(reviewID, "review", &doneID, &workID),
		flowNode(doneID, "done", nil, nil),
	}

	if err := ValidateExecutableFlowTemplate(&workID, nodes); err != nil {
		t.Fatalf("ValidateExecutableFlowTemplate err = %v, want nil", err)
	}
}

func TestValidateExecutableFlowNodesRejectsDisconnectedEntryWithoutReview(t *testing.T) {
	doneID := uuid.New()
	reviewID := uuid.New()
	workID := uuid.New()
	disconnectedWorkID := uuid.New()
	nodes := []repo.FlowNode{
		flowNode(workID, "work", &reviewID, nil),
		flowNode(reviewID, "review", &doneID, nil),
		flowNode(doneID, "done", nil, nil),
		flowNode(disconnectedWorkID, "work", nil, nil),
	}

	if err := ValidateExecutableFlowNodes(nodes); !errors.Is(err, ErrExecutableFlowPath) {
		t.Fatalf("ValidateExecutableFlowNodes err = %v, want ErrExecutableFlowPath", err)
	}
}

func TestValidateExecutableFlowTemplateRejectsUnknownStartNode(t *testing.T) {
	workID := uuid.New()
	startNodeID := uuid.New()
	nodes := []repo.FlowNode{
		flowNode(workID, "work", nil, nil),
	}

	if err := ValidateExecutableFlowTemplate(&startNodeID, nodes); !errors.Is(err, ErrFlowNodeTemplateMismatch) {
		t.Fatalf("ValidateExecutableFlowTemplate err = %v, want ErrFlowNodeTemplateMismatch", err)
	}
}

func flowNode(id uuid.UUID, nodeType string, nextNodeID *uuid.UUID, rejectNodeID *uuid.UUID) repo.FlowNode {
	return repo.FlowNode{
		ID:           id,
		NodeType:     nodeType,
		NextNodeID:   nextNodeID,
		RejectNodeID: rejectNodeID,
	}
}
