package flowpolicy

import (
	"errors"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	ErrExecutableFlowPath       = errors.New("flow template must define a work -> review -> completion path")
	ErrFlowNodeTemplateMismatch = errors.New("flow node does not belong to flow template")
)

type validationState struct {
	NodeID            uuid.UUID
	HasWork           bool
	HasReviewedWork   bool
	PendingReviewWork bool
}

func ValidateExecutableFlowNodes(nodes []repo.FlowNode) error {
	return ValidateExecutableFlowTemplate(nil, nodes)
}

func ValidateExecutableFlowTemplate(startNodeID *uuid.UUID, nodes []repo.FlowNode) error {
	if len(nodes) == 0 {
		return ErrExecutableFlowPath
	}

	nodeByID := make(map[uuid.UUID]repo.FlowNode, len(nodes))
	incoming := make(map[uuid.UUID]int, len(nodes))
	for i := range nodes {
		nodeByID[nodes[i].ID] = nodes[i]
	}

	if startNodeID != nil && *startNodeID != uuid.Nil {
		if _, ok := nodeByID[*startNodeID]; !ok {
			return ErrFlowNodeTemplateMismatch
		}
	}

	for i := range nodes {
		node := nodes[i]
		if node.NextNodeID != nil && *node.NextNodeID != uuid.Nil {
			if _, ok := nodeByID[*node.NextNodeID]; ok {
				incoming[*node.NextNodeID]++
			}
		}
		if node.RejectNodeID != nil && *node.RejectNodeID != uuid.Nil {
			if _, ok := nodeByID[*node.RejectNodeID]; ok {
				if node.NextNodeID == nil || *node.NextNodeID != *node.RejectNodeID {
					incoming[*node.RejectNodeID]++
				}
			}
		}
	}

	entryNodeIDs := make([]uuid.UUID, 0, len(nodes)+1)
	seenEntries := map[uuid.UUID]struct{}{}
	if startNodeID != nil && *startNodeID != uuid.Nil {
		seenEntries[*startNodeID] = struct{}{}
		entryNodeIDs = append(entryNodeIDs, *startNodeID)
	}
	for i := range nodes {
		node := nodes[i]
		if incoming[node.ID] != 0 {
			continue
		}
		if _, seen := seenEntries[node.ID]; seen {
			continue
		}
		seenEntries[node.ID] = struct{}{}
		entryNodeIDs = append(entryNodeIDs, node.ID)
	}
	if len(entryNodeIDs) == 0 {
		for i := range nodes {
			entryNodeIDs = append(entryNodeIDs, nodes[i].ID)
		}
	}

	for _, entryNodeID := range entryNodeIDs {
		if err := validateExecutableFlowPath(entryNodeID, nodes); err != nil {
			return err
		}
	}
	return nil
}

func validateExecutableFlowPath(startNodeID uuid.UUID, nodes []repo.FlowNode) error {
	nodeByID := make(map[uuid.UUID]repo.FlowNode, len(nodes))
	for i := range nodes {
		nodeByID[nodes[i].ID] = nodes[i]
	}

	startNode, ok := nodeByID[startNodeID]
	if !ok {
		return ErrFlowNodeTemplateMismatch
	}

	visited := make(map[validationState]struct{})
	reachedTerminal := false
	var walk func(node repo.FlowNode, hasWork bool, hasReviewedWork bool, pendingReviewWork bool) error
	walk = func(node repo.FlowNode, hasWork bool, hasReviewedWork bool, pendingReviewWork bool) error {
		switch CanonicalNodeType(node.NodeType) {
		case NodeTypeWork:
			hasWork = true
			pendingReviewWork = true
		case NodeTypeReview:
			if pendingReviewWork {
				pendingReviewWork = false
				hasReviewedWork = true
			}
		}

		state := validationState{
			NodeID:            node.ID,
			HasWork:           hasWork,
			HasReviewedWork:   hasReviewedWork,
			PendingReviewWork: pendingReviewWork,
		}
		if _, seen := visited[state]; seen {
			return nil
		}
		visited[state] = struct{}{}

		if node.NextNodeID == nil || *node.NextNodeID == uuid.Nil {
			reachedTerminal = true
			if CanonicalNodeType(node.NodeType) != NodeTypeMerge || !hasWork || !hasReviewedWork || pendingReviewWork {
				return ErrExecutableFlowPath
			}
		}

		nextNodeIDs := make([]uuid.UUID, 0, 2)
		appendEdge := func(id *uuid.UUID) error {
			if id == nil || *id == uuid.Nil {
				return nil
			}
			if _, ok := nodeByID[*id]; !ok {
				return ErrFlowNodeTemplateMismatch
			}
			for _, existing := range nextNodeIDs {
				if existing == *id {
					return nil
				}
			}
			nextNodeIDs = append(nextNodeIDs, *id)
			return nil
		}

		if err := appendEdge(node.NextNodeID); err != nil {
			return err
		}
		if err := appendEdge(node.RejectNodeID); err != nil {
			return err
		}
		for _, nextID := range nextNodeIDs {
			nextNode := nodeByID[nextID]
			if err := walk(nextNode, hasWork, hasReviewedWork, pendingReviewWork); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(startNode, false, false, false); err != nil {
		return err
	}
	if !reachedTerminal {
		return ErrExecutableFlowPath
	}
	return nil
}
