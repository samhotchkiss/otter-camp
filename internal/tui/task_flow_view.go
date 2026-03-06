package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func renderTaskFlowSection(width int, task *taskRecord) []string {
	lines := []string{divider(width, "Flow")}
	selected := task.selectedFlowNode()

	for i := range task.FlowNodes {
		node := &task.FlowNodes[i]
		lines = append(lines, renderTaskFlowNode(width, task, selected, node)...)
	}

	if selected == nil {
		return lines
	}

	lines = append(lines, "")
	lines = append(lines, divider(width, "Selected Node"))

	icon, iconStyle := taskFlowStateGlyph(selected.State, selected.IsCurrent)
	header := fmt.Sprintf("  %s %s", icon, selected.Name)
	lines = append(lines, iconStyle.Render(truncate(header, maxInt(12, width-2))))

	metaParts := []string{}
	if strings.TrimSpace(selected.NodeType) != "" {
		metaParts = append(metaParts, strings.ToUpper(strings.TrimSpace(selected.NodeType)))
	}
	if actor := taskFlowActorLabel(selected); actor != "" {
		metaParts = append(metaParts, actor)
	}
	if selected.VisitCount > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d %s", selected.VisitCount, pluralize(selected.VisitCount, "run", "runs")))
	}
	if counts := taskFlowCountsLabel(selected.SubtaskCounts); counts != "" {
		metaParts = append(metaParts, counts)
	}
	if len(metaParts) > 0 {
		lines = append(lines, styleMuted.Render("  "+truncate(strings.Join(metaParts, "  ·  "), maxInt(12, width-2))))
	}

	if latest := taskFlowLatestExecution(selected); latest != nil {
		lines = append(lines, styleMuted.Render("  Runs"))
		for i := len(selected.Executions) - 1; i >= 0; i-- {
			execution := selected.Executions[i]
			runParts := []string{
				fmt.Sprintf("visit %d", execution.VisitNumber),
				execution.State,
			}
			if counts := taskFlowCountsLabel(execution.SubtaskCounts); counts != "" {
				runParts = append(runParts, counts)
			}
			if strings.TrimSpace(execution.SessionID) != "" {
				runParts = append(runParts, "journal ready")
			}
			lines = append(lines, styleMuted.Render("  · "+truncate(strings.Join(runParts, "  ·  "), maxInt(12, width-6))))
		}
	}

	nodeEvents := taskFlowEventsForNode(task, selected.ID)
	if len(nodeEvents) > 0 {
		lines = append(lines, styleMuted.Render("  History"))
		for _, event := range nodeEvents {
			lines = append(lines, styleMuted.Render("  · "+truncate(taskFlowEventSummary(task, event), maxInt(12, width-6))))
		}
	}

	if latest := taskFlowLatestExecution(selected); latest != nil && len(latest.Subtasks) > 0 {
		lines = append(lines, styleMuted.Render("  Related subtasks"))
		for _, subtask := range latest.Subtasks {
			lines = append(lines, styleText.Render("  "+taskFlowSubtaskGlyph(subtask.Status)+" "+truncate(subtask.Title, maxInt(12, width-6))))
		}
	}

	hintParts := []string{"h/l·inspect flow"}
	if strings.TrimSpace(task.selectedFlowNodeSessionID()) != "" {
		hintParts = append(hintParts, "Enter·open selected journal")
	}
	hintParts = append(hintParts, "H·toggle history")
	lines = append(lines, styleMuted.Render("  "+truncate(strings.Join(hintParts, "  ·  "), maxInt(12, width-2))))

	return lines
}

func renderTaskFlowNode(width int, task *taskRecord, selected *TaskFlowNode, node *TaskFlowNode) []string {
	lines := make([]string, 0, 4)
	icon, iconStyle := taskFlowStateGlyph(node.State, node.IsCurrent)
	prefix := "  "
	if selected != nil && strings.EqualFold(selected.ID, node.ID) {
		prefix = "▸ "
	}

	title := fmt.Sprintf("%s%s %s", prefix, icon, node.Name)
	lines = append(lines, iconStyle.Render(truncate(title, maxInt(12, width-2))))

	metaParts := []string{}
	if strings.TrimSpace(node.NodeType) != "" {
		metaParts = append(metaParts, strings.ToUpper(strings.TrimSpace(node.NodeType)))
	}
	if actor := taskFlowActorLabel(node); actor != "" {
		metaParts = append(metaParts, actor)
	}
	if node.VisitCount > 0 {
		metaParts = append(metaParts, fmt.Sprintf("%d %s", node.VisitCount, pluralize(node.VisitCount, "visit", "visits")))
	}
	if counts := taskFlowCountsLabel(node.SubtaskCounts); counts != "" {
		metaParts = append(metaParts, counts)
	}
	if node.IsCurrent {
		metaParts = append(metaParts, "current")
	}
	if len(metaParts) > 0 {
		lines = append(lines, styleMuted.Render("     "+truncate(strings.Join(metaParts, "  ·  "), maxInt(12, width-5))))
	}

	if nextName := task.flowNodeName(node.NextNodeID); nextName != "" {
		lines = append(lines, styleMuted.Render("     approve -> "+truncate(nextName, maxInt(12, width-17))))
	}
	if rejectName := task.flowNodeName(node.RejectNodeID); rejectName != "" {
		suffix := ""
		if task.flowNodeIndexByID(node.RejectNodeID) <= task.flowNodeIndexByID(node.ID) {
			suffix = " (loop)"
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(colWarning).Render("     reject  -> "+truncate(rejectName+suffix, maxInt(12, width-17))))
	}

	return lines
}

func taskFlowStateGlyph(state string, current bool) (string, lipgloss.Style) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "completed":
		return "[✓]", lipgloss.NewStyle().Foreground(colConnected)
	case "active":
		style := lipgloss.NewStyle().Foreground(colWarning).Bold(true)
		if current {
			style = style.Underline(true)
		}
		return "[*]", style
	case "blocked":
		return "[!]", lipgloss.NewStyle().Foreground(colError).Bold(true)
	case "rejected":
		return "[↺]", lipgloss.NewStyle().Foreground(colWarning).Bold(true)
	default:
		return "[ ]", lipgloss.NewStyle().Foreground(colMuted)
	}
}

func taskFlowActorLabel(node *TaskFlowNode) string {
	if node == nil || strings.TrimSpace(node.ActorLabel) == "" {
		return ""
	}
	actorType := strings.ToLower(strings.TrimSpace(node.ActorType))
	if actorType == "" {
		return node.ActorLabel
	}
	return actorType + ": " + node.ActorLabel
}

func taskFlowCountsLabel(counts TaskSubtaskCounts) string {
	if counts.Total == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d %s", counts.Total, pluralize(counts.Total, "subtask", "subtasks"))}
	if counts.Done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", counts.Done))
	}
	if counts.InProgress > 0 {
		parts = append(parts, fmt.Sprintf("%d active", counts.InProgress))
	}
	if counts.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", counts.Blocked))
	}
	if counts.Pending > 0 && counts.Done == 0 && counts.InProgress == 0 {
		parts = append(parts, fmt.Sprintf("%d pending", counts.Pending))
	}
	return strings.Join(parts, " · ")
}

func taskFlowLatestExecution(node *TaskFlowNode) *TaskFlowExecution {
	if node == nil || len(node.Executions) == 0 {
		return nil
	}
	return &node.Executions[len(node.Executions)-1]
}

func taskFlowEventsForNode(task *taskRecord, nodeID string) []TaskEvent {
	if task == nil || strings.TrimSpace(nodeID) == "" {
		return nil
	}
	filtered := make([]TaskEvent, 0, len(task.Events))
	for i := len(task.Events) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(task.Events[i].FlowNodeID), nodeID) {
			filtered = append(filtered, task.Events[i])
			if len(filtered) == 3 {
				break
			}
		}
	}
	return filtered
}

func taskFlowEventSummary(task *taskRecord, event TaskEvent) string {
	ts := event.CreatedAt.Local().Format("Jan 02 15:04")
	actor := event.ActorType
	if actor == "human_user" {
		actor = "human"
	}
	switch event.EventType {
	case "status.changed":
		from, _ := event.Payload["from_status"].(string)
		to, _ := event.Payload["to_status"].(string)
		return fmt.Sprintf("%s  %s → %s (%s)", ts, from, to, actor)
	case "flow.advanced":
		name := task.flowNodeName(event.FlowNodeID)
		if name == "" {
			name = "flow advanced"
		}
		return fmt.Sprintf("%s  advanced to %s (%s)", ts, name, actor)
	case "flow.rejected", "task.review_rejected":
		name := task.flowNodeName(event.FlowNodeID)
		if name == "" {
			name = "review loop"
		}
		return fmt.Sprintf("%s  rejected to %s (%s)", ts, name, actor)
	case "task.created":
		return fmt.Sprintf("%s  created (%s)", ts, actor)
	default:
		return fmt.Sprintf("%s  %s (%s)", ts, event.EventType, actor)
	}
}

func taskFlowSubtaskGlyph(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "approved":
		return "✓"
	case "in_progress":
		return "◌"
	case "blocked":
		return "!"
	default:
		return "○"
	}
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
