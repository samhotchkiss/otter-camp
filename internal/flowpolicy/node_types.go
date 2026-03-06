package flowpolicy

import (
	"fmt"
	"strings"
)

const (
	NodeTypeWork     = "work"
	NodeTypeReview   = "review"
	NodeTypeDecision = "decision"
	NodeTypeParallel = "parallel"
	NodeTypeMerge    = "merge"
)

var validNodeTypes = []string{
	NodeTypeWork,
	NodeTypeReview,
	NodeTypeDecision,
	NodeTypeParallel,
	NodeTypeMerge,
}

type InvalidNodeTypeError struct {
	NodeType string
}

func (e *InvalidNodeTypeError) Error() string {
	return fmt.Sprintf("invalid flow node type %q", e.NodeType)
}

type NormalizedNodeSpec struct {
	NodeType            string
	RequiresHumanReview bool
}

func ValidNodeTypes() []string {
	out := make([]string, len(validNodeTypes))
	copy(out, validNodeTypes)
	return out
}

func CanonicalNodeType(nodeType string) string {
	normalized := strings.ToLower(strings.TrimSpace(nodeType))
	switch normalized {
	case "agent_work", "execute":
		return NodeTypeWork
	case "human_review":
		return NodeTypeReview
	case "success", "completion", "complete", "completed", "done":
		return NodeTypeMerge
	default:
		return normalized
	}
}

func NormalizeNodeSpec(nodeType string, requiresHumanReview bool) (NormalizedNodeSpec, error) {
	raw := strings.TrimSpace(nodeType)
	canonical := CanonicalNodeType(raw)
	switch canonical {
	case NodeTypeWork, NodeTypeReview, NodeTypeDecision, NodeTypeParallel, NodeTypeMerge:
		return NormalizedNodeSpec{
			NodeType:            canonical,
			RequiresHumanReview: requiresHumanReview || strings.EqualFold(raw, "human_review"),
		}, nil
	default:
		return NormalizedNodeSpec{}, &InvalidNodeTypeError{NodeType: raw}
	}
}
