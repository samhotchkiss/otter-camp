package server

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type taskFlowSubtaskSummaryResponse struct {
	Total      int `json:"total"`
	Done       int `json:"done"`
	InProgress int `json:"in_progress"`
	Pending    int `json:"pending"`
	Blocked    int `json:"blocked"`
	Cancelled  int `json:"cancelled"`
}

type taskFlowEdgeResponse struct {
	FromNodeID uuid.UUID `json:"from_node_id"`
	ToNodeID   uuid.UUID `json:"to_node_id"`
	Kind       string    `json:"kind"`
	IsBackEdge bool      `json:"is_back_edge"`
}

type taskFlowNodeExecutionViewResponse struct {
	ID            uuid.UUID                      `json:"id"`
	TaskID        uuid.UUID                      `json:"task_id"`
	FlowNodeID    uuid.UUID                      `json:"flow_node_id"`
	VisitNumber   int                            `json:"visit_number"`
	VisitCount    int                            `json:"visit_count"`
	Status        string                         `json:"status"`
	State         string                         `json:"state"`
	SessionID     *uuid.UUID                     `json:"session_id"`
	CommitSHA     *string                        `json:"commit_sha"`
	StartedAt     time.Time                      `json:"started_at"`
	CompletedAt   *time.Time                     `json:"completed_at"`
	Metadata      json.RawMessage                `json:"metadata"`
	SubtaskCounts taskFlowSubtaskSummaryResponse `json:"subtask_counts"`
	Subtasks      []subtaskResponse              `json:"subtasks"`
}

type taskFlowNodeViewResponse struct {
	ID                uuid.UUID                           `json:"id"`
	DisplayName       string                              `json:"display_name"`
	NodeType          string                              `json:"node_type"`
	Position          int                                 `json:"position"`
	ActorType         *string                             `json:"actor_type"`
	ActorLabel        string                              `json:"actor_label"`
	NextNodeID        *uuid.UUID                          `json:"next_node_id"`
	RejectNodeID      *uuid.UUID                          `json:"reject_node_id"`
	State             string                              `json:"state"`
	IsCurrent         bool                                `json:"is_current"`
	VisitCount        int                                 `json:"visit_count"`
	CompletedVisits   int                                 `json:"completed_visits"`
	RejectedVisits    int                                 `json:"rejected_visits"`
	LatestExecutionID *uuid.UUID                          `json:"latest_execution_id,omitempty"`
	LatestSessionID   *uuid.UUID                          `json:"latest_session_id,omitempty"`
	SubtaskCounts     taskFlowSubtaskSummaryResponse      `json:"subtask_counts"`
	Executions        []taskFlowNodeExecutionViewResponse `json:"executions"`
}

type taskFlowResponse struct {
	TaskID           uuid.UUID                           `json:"task_id"`
	FlowTemplateID   *uuid.UUID                          `json:"flow_template_id"`
	CurrentNode      *flowNodeResponse                   `json:"current_node,omitempty"`
	CurrentExecution *taskFlowNodeExecutionViewResponse  `json:"current_execution,omitempty"`
	Executions       []taskFlowNodeExecutionViewResponse `json:"executions"`
	Subtasks         []subtaskResponse                   `json:"subtasks"`
	Nodes            []taskFlowNodeViewResponse          `json:"nodes"`
	Edges            []taskFlowEdgeResponse              `json:"edges"`
}

func (h taskHandlers) buildTaskFlowResponse(ctx context.Context, taskRecord repo.ProjectTask) (taskFlowResponse, error) {
	response := taskFlowResponse{
		TaskID:         taskRecord.ID,
		FlowTemplateID: taskRecord.FlowTemplateID,
		Executions:     []taskFlowNodeExecutionViewResponse{},
		Subtasks:       []subtaskResponse{},
		Nodes:          []taskFlowNodeViewResponse{},
		Edges:          []taskFlowEdgeResponse{},
	}

	if h.executions == nil || h.subtasks == nil {
		return response, repo.ErrNotFound
	}

	executions, err := h.executions.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		return response, err
	}
	sort.Slice(executions, func(i, j int) bool {
		if executions[i].StartedAt.Equal(executions[j].StartedAt) {
			return executions[i].VisitNumber < executions[j].VisitNumber
		}
		return executions[i].StartedAt.Before(executions[j].StartedAt)
	})

	subtasks, err := h.listAllTaskSubtasks(ctx, taskRecord.ID)
	if err != nil {
		return response, err
	}
	subtasksByExecution := make(map[uuid.UUID][]repo.ProjectSubtask, len(subtasks))
	for _, item := range subtasks {
		subtasksByExecution[item.FlowNodeExecutionID] = append(subtasksByExecution[item.FlowNodeExecutionID], item)
		response.Subtasks = append(response.Subtasks, toSubtaskResponse(item))
	}

	executionViews := make([]taskFlowNodeExecutionViewResponse, 0, len(executions))
	executionsByNode := make(map[uuid.UUID][]taskFlowNodeExecutionViewResponse, len(executions))
	for _, execution := range executions {
		view := toTaskFlowExecutionViewResponse(execution, subtasksByExecution[execution.ID])
		executionViews = append(executionViews, view)
		executionsByNode[execution.FlowNodeID] = append(executionsByNode[execution.FlowNodeID], view)
	}
	response.Executions = executionViews

	if taskRecord.CurrentFlowNodeID != nil && h.flowNodes != nil {
		if node, nodeErr := h.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID); nodeErr == nil {
			nodeResponse := toFlowNodeResponse(&node)
			response.CurrentNode = &nodeResponse
		}
		if current := latestActiveExecution(executionsByNode[*taskRecord.CurrentFlowNodeID]); current != nil {
			copy := *current
			response.CurrentExecution = &copy
		}
	}

	if taskRecord.FlowTemplateID == nil || h.flowNodes == nil {
		return response, nil
	}

	nodes, err := h.flowNodes.ListByTemplate(ctx, *taskRecord.FlowTemplateID)
	if err != nil {
		return response, err
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Position == nodes[j].Position {
			return nodes[i].DisplayName < nodes[j].DisplayName
		}
		return nodes[i].Position < nodes[j].Position
	})

	positionByID := make(map[uuid.UUID]int, len(nodes))
	for _, node := range nodes {
		positionByID[node.ID] = node.Position
	}

	for _, node := range nodes {
		nodeExecutions := executionsByNode[node.ID]
		state := deriveTaskFlowNodeState(taskRecord, node, nodeExecutions)
		latestExecution := latestExecutionView(nodeExecutions)
		nodeView := taskFlowNodeViewResponse{
			ID:              node.ID,
			DisplayName:     node.DisplayName,
			NodeType:        node.NodeType,
			Position:        node.Position,
			ActorType:       node.ActorType,
			ActorLabel:      h.resolveFlowNodeActorLabel(ctx, node),
			NextNodeID:      node.NextNodeID,
			RejectNodeID:    node.RejectNodeID,
			State:           state,
			IsCurrent:       taskRecord.CurrentFlowNodeID != nil && node.ID == *taskRecord.CurrentFlowNodeID,
			VisitCount:      len(nodeExecutions),
			CompletedVisits: countExecutionStatus(nodeExecutions, "completed"),
			RejectedVisits:  countExecutionStatus(nodeExecutions, "rejected"),
			SubtaskCounts:   summarizeNodeSubtasks(nodeExecutions),
			Executions:      append([]taskFlowNodeExecutionViewResponse(nil), nodeExecutions...),
		}
		if latestExecution != nil {
			nodeView.LatestExecutionID = &latestExecution.ID
			nodeView.LatestSessionID = latestExecution.SessionID
			nodeView.SubtaskCounts = latestExecution.SubtaskCounts
		}
		response.Nodes = append(response.Nodes, nodeView)

		if node.NextNodeID != nil {
			response.Edges = append(response.Edges, taskFlowEdgeResponse{
				FromNodeID: node.ID,
				ToNodeID:   *node.NextNodeID,
				Kind:       "next",
				IsBackEdge: positionByID[*node.NextNodeID] <= node.Position,
			})
		}
		if node.RejectNodeID != nil {
			response.Edges = append(response.Edges, taskFlowEdgeResponse{
				FromNodeID: node.ID,
				ToNodeID:   *node.RejectNodeID,
				Kind:       "reject",
				IsBackEdge: positionByID[*node.RejectNodeID] <= node.Position,
			})
		}
	}

	return response, nil
}

func toTaskFlowExecutionViewResponse(model repo.FlowNodeExecution, subtasks []repo.ProjectSubtask) taskFlowNodeExecutionViewResponse {
	subtaskResponse := make([]subtaskResponse, 0, len(subtasks))
	for _, item := range subtasks {
		subtaskResponse = append(subtaskResponse, toSubtaskResponse(item))
	}
	return taskFlowNodeExecutionViewResponse{
		ID:            model.ID,
		TaskID:        model.TaskID,
		FlowNodeID:    model.FlowNodeID,
		VisitNumber:   model.VisitNumber,
		VisitCount:    model.VisitNumber,
		Status:        model.Status,
		State:         executionState(model.Status),
		SessionID:     model.SessionID,
		CommitSHA:     model.CommitSHA,
		StartedAt:     model.StartedAt,
		CompletedAt:   model.CompletedAt,
		Metadata:      model.Metadata,
		SubtaskCounts: summarizeSubtasks(subtasks),
		Subtasks:      subtaskResponse,
	}
}

func executionState(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return "completed"
	case "rejected":
		return "rejected"
	case "abandoned":
		return "blocked"
	case "active":
		return "active"
	default:
		return "pending"
	}
}

func deriveTaskFlowNodeState(taskRecord repo.ProjectTask, node repo.FlowNode, executions []taskFlowNodeExecutionViewResponse) string {
	if taskRecord.CurrentFlowNodeID != nil && node.ID == *taskRecord.CurrentFlowNodeID {
		if strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
			return "blocked"
		}
		return "active"
	}
	if len(executions) == 0 {
		return "pending"
	}
	return executions[len(executions)-1].State
}

func latestExecutionView(executions []taskFlowNodeExecutionViewResponse) *taskFlowNodeExecutionViewResponse {
	if len(executions) == 0 {
		return nil
	}
	copy := executions[len(executions)-1]
	return &copy
}

func latestActiveExecution(executions []taskFlowNodeExecutionViewResponse) *taskFlowNodeExecutionViewResponse {
	for i := len(executions) - 1; i >= 0; i-- {
		if executions[i].Status == "active" {
			copy := executions[i]
			return &copy
		}
	}
	return latestExecutionView(executions)
}

func countExecutionStatus(executions []taskFlowNodeExecutionViewResponse, status string) int {
	count := 0
	for _, execution := range executions {
		if execution.Status == status {
			count++
		}
	}
	return count
}

func summarizeNodeSubtasks(executions []taskFlowNodeExecutionViewResponse) taskFlowSubtaskSummaryResponse {
	if latest := latestExecutionView(executions); latest != nil {
		return latest.SubtaskCounts
	}
	return taskFlowSubtaskSummaryResponse{}
}

func summarizeSubtasks(subtasks []repo.ProjectSubtask) taskFlowSubtaskSummaryResponse {
	summary := taskFlowSubtaskSummaryResponse{Total: len(subtasks)}
	for _, item := range subtasks {
		switch strings.ToLower(strings.TrimSpace(item.WorkStatus)) {
		case "done", "approved":
			summary.Done++
		case "in_progress":
			summary.InProgress++
		case "blocked":
			summary.Blocked++
		case "cancelled":
			summary.Cancelled++
		default:
			summary.Pending++
		}
	}
	return summary
}

func (h taskHandlers) resolveFlowNodeActorLabel(ctx context.Context, node repo.FlowNode) string {
	actorType := strings.ToLower(strings.TrimSpace(derefString(node.ActorType)))
	switch actorType {
	case "agent":
		if node.ActorID != nil && h.agents != nil {
			if agent, err := h.agents.GetByID(ctx, *node.ActorID); err == nil && strings.TrimSpace(agent.DisplayName) != "" {
				return agent.DisplayName
			}
		}
		return "Agent"
	case "human":
		if node.ActorID != nil && h.users != nil {
			if user, err := h.users.GetByID(ctx, *node.ActorID); err == nil && strings.TrimSpace(user.DisplayName) != "" {
				return user.DisplayName
			}
		}
		return "Human"
	case "role":
		if label := flowRoleLabel(node.Metadata); label != "" {
			return label
		}
		return "Role"
	default:
		return ""
	}
}

func flowRoleLabel(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return ""
	}
	for _, key := range []string{"actor_role", "role", "project_role"} {
		if value, ok := payload[key].(string); ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return humanizeFlowLabel(value)
			}
		}
	}
	return ""
}

func humanizeFlowLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(value))
	for i, part := range parts {
		switch strings.ToLower(part) {
		case "pm":
			parts[i] = "PM"
		default:
			parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
		}
	}
	return strings.Join(parts, " ")
}
