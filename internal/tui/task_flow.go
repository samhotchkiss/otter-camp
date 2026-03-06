package tui

import "strings"

func (task *taskRecord) flowNodeIndexByID(nodeID string) int {
	nodeID = strings.TrimSpace(nodeID)
	if task == nil || nodeID == "" {
		return -1
	}
	for i := range task.FlowNodes {
		if strings.EqualFold(strings.TrimSpace(task.FlowNodes[i].ID), nodeID) {
			return i
		}
	}
	return -1
}

func (task *taskRecord) flowNodeByID(nodeID string) *TaskFlowNode {
	index := task.flowNodeIndexByID(nodeID)
	if index < 0 {
		return nil
	}
	return &task.FlowNodes[index]
}

func (task *taskRecord) ensureSelectedFlowNode() {
	if task == nil || len(task.FlowNodes) == 0 {
		return
	}
	if task.flowNodeIndexByID(task.SelectedFlowNodeID) >= 0 {
		return
	}
	switch {
	case task.flowNodeIndexByID(task.FlowCurrentNodeID) >= 0:
		task.SelectedFlowNodeID = task.FlowCurrentNodeID
	default:
		task.SelectedFlowNodeID = task.FlowNodes[0].ID
	}
}

func (task *taskRecord) selectedFlowNode() *TaskFlowNode {
	if task == nil {
		return nil
	}
	task.ensureSelectedFlowNode()
	return task.flowNodeByID(task.SelectedFlowNodeID)
}

func (task *taskRecord) selectedFlowNodeSessionID() string {
	node := task.selectedFlowNode()
	if node == nil {
		return ""
	}
	if strings.TrimSpace(node.SessionID) != "" {
		return strings.TrimSpace(node.SessionID)
	}
	for i := len(node.Executions) - 1; i >= 0; i-- {
		if strings.TrimSpace(node.Executions[i].SessionID) != "" {
			return strings.TrimSpace(node.Executions[i].SessionID)
		}
	}
	return ""
}

func (task *taskRecord) flowNodeName(nodeID string) string {
	if node := task.flowNodeByID(nodeID); node != nil {
		return strings.TrimSpace(node.Name)
	}
	return ""
}
