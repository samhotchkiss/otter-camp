package native

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func (e *NativeToolExecutor) handleMemoryQuery(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.memory == nil {
		return nil, errors.New("memory query service not configured")
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	query, ok := readString(input, "query")
	if !ok || query == "" {
		return map[string]any{"memories": []any{}, "total": 0}, nil
	}

	limit := clamp(readInt(input, "limit", 20), 1, 100)
	req := memory.RetrievalRequest{
		OrganizationID: scope.organizationID,
		Query:          query,
		Mode:           memory.RetrievalModeAgentQuery,
		MaxResults:     limit,
		ProjectID:      scope.projectID,
		TaskID:         scope.taskID,
		AgentID:        scope.agentID,
	}

	if scopeName, ok := readString(input, "scope"); ok {
		switch strings.ToLower(scopeName) {
		case "org":
			req.ProjectID = nil
			req.TaskID = nil
		case "project":
			req.TaskID = nil
		case "agent":
			req.ProjectID = nil
			req.TaskID = nil
		}
	}

	result, err := e.memory.Query(ctx, req)
	if err != nil {
		return nil, err
	}

	payload := make([]map[string]any, 0, len(result.Memories))
	for _, ranked := range result.Memories {
		payload = append(payload, map[string]any{
			"id":          ranked.Memory.ID.String(),
			"content":     ranked.Memory.Content,
			"confidence":  ranked.Memory.Confidence,
			"source_type": ranked.Memory.MemoryType,
			"created_at":  ranked.Memory.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}

	return map[string]any{
		"memories": payload,
		"total":    len(payload),
	}, nil
}

func (e *NativeToolExecutor) handleProjectList(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.projects == nil {
		return nil, errors.New("project repository not configured")
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	limit := clamp(readInt(input, "limit", 50), 1, 200)
	cursor := decodeCursor(readCursor(input))

	projects, err := e.projects.List(ctx, scope.organizationID)
	if err != nil {
		return nil, err
	}
	start := findCursorIndex(cursor, func(i int) string { return projects[i].ID.String() }, len(projects))
	end := start + limit
	if end > len(projects) {
		end = len(projects)
	}

	items := make([]map[string]any, 0, end-start)
	for _, project := range projects[start:end] {
		items = append(items, map[string]any{
			"id":            project.ID.String(),
			"slug":          project.Slug,
			"name":          project.DisplayName,
			"status":        "active",
			"delivery_mode": project.DeliveryMode,
		})
	}
	nextCursor := ""
	if end < len(projects) && len(items) > 0 {
		nextCursor = encodeCursor(projects[end-1].ID.String())
	}
	return map[string]any{
		"projects": items,
		"meta":     map[string]any{"cursor": nextCursor},
	}, nil
}

func (e *NativeToolExecutor) handleProjectGet(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.projects == nil {
		return nil, errors.New("project repository not configured")
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok {
		return map[string]any{"error": "not_found"}, nil
	}
	project, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	return map[string]any{
		"project": map[string]any{
			"id":              project.ID.String(),
			"organization_id": project.OrganizationID.String(),
			"slug":            project.Slug,
			"name":            project.DisplayName,
			"description":     project.Description,
			"delivery_mode":   project.DeliveryMode,
			"created_at":      project.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"updated_at":      project.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		},
	}, nil
}

func (e *NativeToolExecutor) handleTaskList(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.tasks == nil {
		return nil, errors.New("task repository not configured")
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	limit := clamp(readInt(input, "limit", 50), 1, 200)
	cursor := decodeCursor(readCursor(input))
	statusFilter, _ := readString(input, "status")

	taskRows := make([]repo.ProjectTask, 0)
	if projectID, ok := readUUID(input, "project_id"); ok {
		tasks, err := e.tasks.ListByProject(ctx, projectID)
		if err != nil {
			return nil, err
		}
		taskRows = append(taskRows, tasks...)
	} else if e.projects != nil {
		projects, err := e.projects.List(ctx, scope.organizationID)
		if err != nil {
			return nil, err
		}
		for _, project := range projects {
			tasks, listErr := e.tasks.ListByProject(ctx, project.ID)
			if listErr != nil {
				return nil, listErr
			}
			taskRows = append(taskRows, tasks...)
		}
	}

	filtered := make([]repo.ProjectTask, 0, len(taskRows))
	for _, task := range taskRows {
		if statusFilter != "" && !strings.EqualFold(task.WorkStatus, statusFilter) {
			continue
		}
		filtered = append(filtered, task)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID.String() > filtered[j].ID.String()
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	start := findCursorIndex(cursor, func(i int) string { return filtered[i].ID.String() }, len(filtered))
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}

	items := make([]map[string]any, 0, end-start)
	for _, task := range filtered[start:end] {
		var assignee any
		if task.AssignedAgentID != nil {
			assignee = task.AssignedAgentID.String()
		}
		items = append(items, map[string]any{
			"id":          task.ID.String(),
			"task_number": task.TaskNumber,
			"title":       task.Title,
			"work_status": task.WorkStatus,
			"assignee":    assignee,
		})
	}

	nextCursor := ""
	if end < len(filtered) && len(items) > 0 {
		nextCursor = encodeCursor(filtered[end-1].ID.String())
	}

	return map[string]any{
		"tasks": items,
		"meta":  map[string]any{"cursor": nextCursor},
	}, nil
}

func (e *NativeToolExecutor) handleTaskGet(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.tasks == nil {
		return nil, errors.New("task repository not configured")
	}
	taskID, ok := readUUID(input, "task_id")
	if !ok {
		return map[string]any{"error": "not_found"}, nil
	}
	task, err := e.tasks.GetByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	var assigned any
	if task.AssignedAgentID != nil {
		assigned = task.AssignedAgentID.String()
	}
	return map[string]any{
		"task": map[string]any{
			"id":                    task.ID.String(),
			"organization_id":       task.OrganizationID.String(),
			"project_id":            task.ProjectID.String(),
			"task_number":           task.TaskNumber,
			"title":                 task.Title,
			"description":           task.Description,
			"work_status":           task.WorkStatus,
			"current_flow_node_id":  uuidPointer(task.CurrentFlowNodeID),
			"flow_template_id":      uuidPointer(task.FlowTemplateID),
			"schedule_id":           uuidPointer(task.ScheduleID),
			"branch_name":           task.BranchName,
			"requires_human_review": task.RequiresHumanReview,
			"assigned_agent_id":     assigned,
			"created_at":            task.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"updated_at":            task.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"completed_at":          toRFC3339(task.CompletedAt),
		},
	}, nil
}

func (e *NativeToolExecutor) handleInboxList(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.inbox == nil {
		return nil, errors.New("inbox repository not configured")
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	status, _ := readString(input, "status")
	limit := clamp(readInt(input, "limit", 50), 1, 200)
	includeActed := strings.EqualFold(status, "all") || strings.EqualFold(status, "actioned")

	// Best-judgment fallback: inbox rows are user-targeted today, so agent-native view returns
	// organization broadcasts unless a user context is explicitly provided by later tasks.
	rows, err := e.inbox.ListBroadcast(ctx, scope.organizationID, repo.InboxListOptions{
		IncludeActed: includeActed,
		Limit:        limit,
	})
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, 0, len(rows))
	for _, item := range rows {
		if strings.EqualFold(status, "pending") && item.IsActed {
			continue
		}
		if strings.EqualFold(status, "actioned") && !item.IsActed {
			continue
		}
		items = append(items, map[string]any{
			"id":         item.ID.String(),
			"item_type":  item.ItemType,
			"urgency":    "normal",
			"created_at": item.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"summary":    item.Title,
		})
	}

	nextCursor := ""
	if len(items) > 0 && len(items) == limit {
		nextCursor = encodeCursor(rows[len(rows)-1].ID.String())
	}

	return map[string]any{
		"items": items,
		"meta":  map[string]any{"cursor": nextCursor},
	}, nil
}

func (e *NativeToolExecutor) handleSessionList(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.chatSessions == nil {
		return nil, errors.New("session repository not configured")
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := e.chatSessions.ListByOrg(ctx, scope.organizationID)
	if err != nil {
		return nil, err
	}

	scopeTypeFilter, _ := readString(input, "scope_type")
	scopeIDFilter, _ := readUUID(input, "scope_id")
	statusFilter, _ := readString(input, "status")
	limit := clamp(readInt(input, "limit", 50), 1, 200)
	cursor := decodeCursor(readCursor(input))

	filtered := make([]repo.ChatSession, 0, len(rows))
	for _, row := range rows {
		if scopeTypeFilter != "" && !strings.EqualFold(row.ScopeType, scopeTypeFilter) {
			continue
		}
		if scopeIDFilter != uuid.Nil && row.ScopeID != scopeIDFilter {
			continue
		}
		if statusFilter != "" && !strings.EqualFold(row.Status, statusFilter) {
			continue
		}
		filtered = append(filtered, row)
	}

	start := findCursorIndex(cursor, func(i int) string { return filtered[i].ID.String() }, len(filtered))
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	items := make([]map[string]any, 0, end-start)
	for _, row := range filtered[start:end] {
		items = append(items, map[string]any{
			"id":         row.ID.String(),
			"scope_type": row.ScopeType,
			"scope_id":   row.ScopeID.String(),
			"mode":       row.Mode,
			"status":     row.Status,
		})
	}
	nextCursor := ""
	if end < len(filtered) && len(items) > 0 {
		nextCursor = encodeCursor(filtered[end-1].ID.String())
	}

	return map[string]any{"sessions": items, "meta": map[string]any{"cursor": nextCursor}}, nil
}

func (e *NativeToolExecutor) handleSessionGet(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.chatSessions == nil {
		return nil, errors.New("session repository not configured")
	}
	sessionID, ok := readUUID(input, "session_id")
	if !ok {
		return map[string]any{"error": "not_found"}, nil
	}
	session, err := e.chatSessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	participantCount := 0
	if e.participants != nil {
		participants, listErr := e.participants.ListBySession(ctx, sessionID)
		if listErr == nil {
			participantCount = len(participants)
		}
	}
	return map[string]any{
		"session": map[string]any{
			"id":              session.ID.String(),
			"organization_id": session.OrganizationID.String(),
			"scope_type":      session.ScopeType,
			"scope_id":        session.ScopeID.String(),
			"mode":            session.Mode,
			"status":          session.Status,
			"title":           session.Title,
			"created_at":      session.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"updated_at":      session.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		},
		"participant_count": participantCount,
	}, nil
}

func (e *NativeToolExecutor) handleSessionHistory(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.messages == nil {
		return nil, errors.New("message repository not configured")
	}
	sessionID, ok := readUUID(input, "session_id")
	if !ok {
		return map[string]any{"error": "not_found"}, nil
	}
	messages, err := e.messages.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	limit := clamp(readInt(input, "limit", 100), 1, 500)
	beforeSequence := int64(readInt(input, "before_sequence", 0))

	filtered := make([]repo.ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if beforeSequence > 0 && msg.SequenceNumber >= beforeSequence {
			continue
		}
		filtered = append(filtered, msg)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].SequenceNumber > filtered[j].SequenceNumber
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	items := make([]map[string]any, 0, len(filtered))
	for _, msg := range filtered {
		items = append(items, map[string]any{
			"sequence":    msg.SequenceNumber,
			"author_type": msg.AuthorType,
			"author_id":   uuidPointer(msg.AuthorID),
			"role":        msg.Role,
			"content":     msg.Content,
			"created_at":  msg.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	var nextSequence any
	if len(filtered) > 0 {
		nextSequence = filtered[len(filtered)-1].SequenceNumber
	}
	return map[string]any{"messages": items, "meta": map[string]any{"next_sequence": nextSequence}}, nil
}

func (e *NativeToolExecutor) handleAgentList(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.agents == nil {
		return nil, errors.New("agent repository not configured")
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := e.agents.List(ctx, scope.organizationID)
	if err != nil {
		return nil, err
	}
	classFilter, _ := readString(input, "class")
	statusFilter, _ := readString(input, "status")
	limit := clamp(readInt(input, "limit", 50), 1, 200)
	cursor := decodeCursor(readCursor(input))

	filtered := make([]repo.Agent, 0, len(rows))
	for _, row := range rows {
		if classFilter != "" && !strings.EqualFold(row.AgentClass, classFilter) {
			continue
		}
		if statusFilter != "" && !strings.EqualFold(row.LifecycleStatus, statusFilter) {
			continue
		}
		filtered = append(filtered, row)
	}
	start := findCursorIndex(cursor, func(i int) string { return filtered[i].ID.String() }, len(filtered))
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	items := make([]map[string]any, 0, end-start)
	for _, row := range filtered[start:end] {
		items = append(items, map[string]any{
			"id":               row.ID.String(),
			"name":             row.DisplayName,
			"class":            row.AgentClass,
			"lifecycle_status": row.LifecycleStatus,
		})
	}
	nextCursor := ""
	if end < len(filtered) && len(items) > 0 {
		nextCursor = encodeCursor(filtered[end-1].ID.String())
	}
	return map[string]any{"agents": items, "meta": map[string]any{"cursor": nextCursor}}, nil
}

func (e *NativeToolExecutor) handleAgentGet(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.agents == nil {
		return nil, errors.New("agent repository not configured")
	}
	agentID, ok := readUUID(input, "agent_id")
	if !ok {
		return map[string]any{"error": "not_found"}, nil
	}
	agent, err := e.agents.GetByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	return map[string]any{"agent": map[string]any{
		"id":               agent.ID.String(),
		"organization_id":  agent.OrganizationID.String(),
		"name":             agent.DisplayName,
		"class":            agent.AgentClass,
		"lifecycle_status": agent.LifecycleStatus,
		"agent_type":       agent.AgentType,
		"created_at":       agent.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		"updated_at":       agent.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}}, nil
}

func (e *NativeToolExecutor) handleFlowGetTemplate(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.flowTemplates == nil {
		return nil, errors.New("flow template repository not configured")
	}
	id, ok := readUUID(input, "flow_template_id")
	if !ok {
		return map[string]any{"error": "not_found"}, nil
	}
	tpl, err := e.flowTemplates.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	nodes := make([]map[string]any, 0)
	if e.flowNodes != nil {
		rows, listErr := e.flowNodes.GetByTemplateOrdered(ctx, id)
		if listErr == nil {
			for _, node := range rows {
				nodes = append(nodes, map[string]any{
					"id":                    node.ID.String(),
					"flow_template_id":      node.FlowTemplateID.String(),
					"display_name":          node.DisplayName,
					"node_type":             node.NodeType,
					"position":              node.Position,
					"next_node_id":          uuidPointer(node.NextNodeID),
					"reject_node_id":        uuidPointer(node.RejectNodeID),
					"requires_human_review": node.RequiresHumanReview,
				})
			}
		}
	}
	return map[string]any{
		"template": map[string]any{
			"id":           tpl.ID.String(),
			"slug":         tpl.Slug,
			"display_name": tpl.DisplayName,
			"description":  tpl.Description,
			"version":      tpl.Version,
			"is_current":   tpl.IsCurrent,
		},
		"nodes": nodes,
	}, nil
}

func (e *NativeToolExecutor) handleFlowListTemplates(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.flowTemplates == nil {
		return nil, errors.New("flow template repository not configured")
	}
	scope, err := e.resolveScope(ctx)
	if err != nil {
		return nil, err
	}

	var projectID *uuid.UUID
	if requestedProjectID, ok := readUUID(input, "project_id"); ok && requestedProjectID != uuid.Nil {
		projectID = &requestedProjectID
	} else if scope.projectID != nil {
		projectID = scope.projectID
	}

	combined := make([]repo.FlowTemplate, 0)
	seen := make(map[uuid.UUID]struct{})
	addTemplates := func(items []repo.FlowTemplate) {
		for _, item := range items {
			if _, exists := seen[item.ID]; exists {
				continue
			}
			seen[item.ID] = struct{}{}
			combined = append(combined, item)
		}
	}

	globalTemplates, err := e.flowTemplates.ListCurrent(ctx, &scope.organizationID, nil)
	if err != nil {
		return nil, err
	}
	addTemplates(globalTemplates)

	if projectID != nil {
		projectTemplates, listErr := e.flowTemplates.ListCurrent(ctx, &scope.organizationID, projectID)
		if listErr != nil {
			return nil, listErr
		}
		addTemplates(projectTemplates)
	}

	sort.Slice(combined, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(combined[i].DisplayName))
		right := strings.ToLower(strings.TrimSpace(combined[j].DisplayName))
		if left == right {
			return combined[i].ID.String() < combined[j].ID.String()
		}
		return left < right
	})

	templatesPayload := make([]map[string]any, 0, len(combined))
	for _, template := range combined {
		nodesPayload := make([]map[string]any, 0)
		if e.flowNodes != nil {
			nodes, listErr := e.flowNodes.GetByTemplateOrdered(ctx, template.ID)
			if listErr != nil {
				return nil, listErr
			}
			nodesPayload = make([]map[string]any, 0, len(nodes))
			for _, node := range nodes {
				nodesPayload = append(nodesPayload, map[string]any{
					"display_name": node.DisplayName,
					"node_type":    node.NodeType,
					"position":     node.Position,
				})
			}
		}

		templatesPayload = append(templatesPayload, map[string]any{
			"id":           template.ID.String(),
			"display_name": template.DisplayName,
			"nodes":        nodesPayload,
		})
	}

	return map[string]any{"templates": templatesPayload}, nil
}

func (e *NativeToolExecutor) handleFlowGetExecution(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.flowExecs == nil {
		return nil, errors.New("flow execution repository not configured")
	}
	id, ok := readUUID(input, "flow_node_execution_id")
	if !ok {
		return map[string]any{"error": "not_found"}, nil
	}
	execRow, err := e.flowExecs.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]any{"error": "not_found"}, nil
		}
		return nil, err
	}
	subtaskPayload := make([]map[string]any, 0)
	if e.subtasks != nil {
		rows, listErr := e.subtasks.ListByExecution(ctx, id)
		if listErr == nil {
			for _, subtask := range rows {
				subtaskPayload = append(subtaskPayload, map[string]any{
					"id":          subtask.ID.String(),
					"title":       subtask.Title,
					"work_status": subtask.WorkStatus,
				})
			}
		}
	}
	return map[string]any{
		"execution": map[string]any{
			"id":           execRow.ID.String(),
			"task_id":      execRow.TaskID.String(),
			"current_node": execRow.FlowNodeID.String(),
			"visit_count":  execRow.VisitNumber,
			"subtasks":     subtaskPayload,
			"session_id":   uuidPointer(execRow.SessionID),
			"commit_sha":   execRow.CommitSHA,
			"status":       execRow.Status,
			"started_at":   execRow.StartedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"completed_at": toRFC3339(execRow.CompletedAt),
		},
	}, nil
}

func (e *NativeToolExecutor) handleScheduleList(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.schedules == nil {
		return nil, errors.New("schedule repository not configured")
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok {
		return map[string]any{"schedules": []any{}}, nil
	}
	rows, err := e.schedules.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	payload := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		payload = append(payload, map[string]any{
			"id":               row.ID.String(),
			"cron":             row.CronExpression,
			"flow_template_id": row.FlowTemplateID.String(),
			"overlap_policy":   row.OverlapPolicy,
			"last_fired_at":    toRFC3339(row.LastFiredAt),
			"next_fire_at":     toRFC3339(row.NextFireAt),
		})
	}
	return map[string]any{"schedules": payload}, nil
}

func (e *NativeToolExecutor) handleMergeQueueStatus(ctx context.Context, input map[string]any) (map[string]any, error) {
	if e.mergeQueue == nil {
		return nil, errors.New("merge queue repository not configured")
	}
	projectID, ok := readUUID(input, "project_id")
	if !ok {
		return map[string]any{"entries": []any{}, "length": 0}, nil
	}
	rows, err := e.mergeQueue.ListActive(ctx, projectID)
	if err != nil {
		return nil, err
	}
	entries := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, map[string]any{
			"id":          row.ID.String(),
			"task_id":     row.TaskID.String(),
			"position":    row.Position,
			"enqueued_at": row.EnqueuedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return map[string]any{"entries": entries, "length": len(entries)}, nil
}

func readCursor(input map[string]any) string {
	cursor, _ := readString(input, "cursor")
	return cursor
}

func findCursorIndex(cursor string, idAt func(int) string, length int) int {
	if cursor == "" || length == 0 {
		return 0
	}
	for i := 0; i < length; i++ {
		if idAt(i) == cursor {
			return i + 1
		}
	}
	return 0
}

func uuidPointer(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}
