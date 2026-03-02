package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
	tuiapp "github.com/samhotchkiss/otter-camp/internal/tui"
)

func runTUICommand(args []string) int {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	help := flags.Bool("help", false, "show help for tui command")
	nonInteractive := flags.Bool("non-interactive", false, "validate startup path and exit without opening UI")
	serverURLFlag := flags.String("server-url", "", "server URL (or OTTERCAMP_SERVER_URL)")
	apiKeyFlag := flags.String("api-key", "", "API key (or OTTERCAMP_API_KEY)")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printTUIUsage(os.Stdout)
			return 0
		}
		fmt.Fprintf(os.Stderr, "tui argument error: %v\n", err)
		printTUIUsage(os.Stderr)
		return 1
	}
	if *help {
		printTUIUsage(os.Stdout)
		return 0
	}
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "tui does not accept positional arguments: %s\n", flags.Args()[0])
		printTUIUsage(os.Stderr)
		return 1
	}

	statePath, err := tuiapp.ResolveStatePath(os.Getenv, os.UserHomeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui state path error: %v\n", err)
		return 1
	}
	state, err := tuiapp.LoadState(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui state load error: %v\n", err)
		return 1
	}
	stateExists, err := tuiapp.StateFileExists(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui state stat error: %v\n", err)
		return 1
	}

	if *nonInteractive {
		if err := tuiapp.SaveState(statePath, state); err != nil {
			fmt.Fprintf(os.Stderr, "tui state save error: %v\n", err)
			return 1
		}
		return 0
	}

	serverURL, apiKey, err := resolveTUIRealtimeCredentials(*serverURLFlag, *apiKeyFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui realtime setup error: %v\n", err)
		return 1
	}

	runtimeHints := tuiapp.DetectRuntimeHints(os.Getenv)
	runtimeHints.FirstRun = !stateExists

	// Use a pointer so the SendChatMessage closure can reference program after it's created.
	var program *tea.Program

	if serverURL != "" && apiKey != "" {
		apiClient, err := newCLIAPIClient(serverURL, apiKey)
		if err == nil {
			runtimeHints.LoadOrgSession = func(ctx context.Context) (string, error) {
				sessions, err := apiClient.ListChatSessions(ctx, chatListSessionsFilter{
					Status: "active",
					Limit:  5,
				})
				if err != nil {
					return "", err
				}
				for _, s := range sessions.Data {
					if strings.EqualFold(s.ScopeType, "organization") {
						return s.ID.String(), nil
					}
				}
				return "", nil
			}
			runtimeHints.LoadInboxCount = func(ctx context.Context) (int, error) {
				var resp struct {
					Meta struct {
						Pagination struct {
							Total int `json:"total"`
						} `json:"pagination"`
					} `json:"meta"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/inbox?is_acted=false&limit=1", nil, &resp); err != nil {
					return 0, err
				}
				return resp.Meta.Pagination.Total, nil
			}
			runtimeHints.LoadInboxItems = func(ctx context.Context) ([]tuiapp.InboxSummaryItem, error) {
				var resp struct {
					Data []struct {
						ID         string `json:"id"`
						Title      string `json:"title"`
						SourceTask struct {
							TaskID string `json:"task_id"`
						} `json:"source_task"`
					} `json:"data"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/inbox?is_acted=false&limit=50", nil, &resp); err != nil {
					return nil, err
				}
				out := make([]tuiapp.InboxSummaryItem, 0, len(resp.Data))
				for _, item := range resp.Data {
					out = append(out, tuiapp.InboxSummaryItem{
						ID:      item.ID,
						TaskID:  item.SourceTask.TaskID,
						Summary: strings.TrimSpace(item.Title),
					})
				}
				return out, nil
			}
			// EX-160: wire ActOnInboxItem so approve/reject/defer reach the server.
			runtimeHints.ActOnInboxItem = func(ctx context.Context, itemID, action string) error {
				body := map[string]string{"action": action}
				var resp struct{}
				return apiClient.request(ctx, "POST", "/v1/inbox/"+url.PathEscape(itemID)+"/act", body, &resp)
			}
			runtimeHints.LoadRecentChats = func(ctx context.Context) ([]tuiapp.SidebarChatItem, error) {
				sessions, err := apiClient.ListChatSessions(ctx, chatListSessionsFilter{
					Status: "active",
					Limit:  10,
				})
				if err != nil {
					return nil, err
				}
				out := make([]tuiapp.SidebarChatItem, 0, 4)
				for _, s := range sessions.Data {
					if strings.EqualFold(s.ScopeType, "organization") {
						continue // Frank/General is always shown separately
					}
					name := ""
					if s.Title != nil {
						name = strings.TrimSpace(*s.Title)
					}
					var taskWorkStatus string
					// For project_task sessions, always fetch task to get OC-N prefix and work status
					if strings.EqualFold(s.ScopeType, "project_task") && s.ScopeID != uuid.Nil {
						var taskResp struct {
							Data struct {
								Title      string `json:"title"`
								TaskNumber int    `json:"task_number"`
								WorkStatus string `json:"work_status"`
							} `json:"data"`
						}
						if apiClient.request(ctx, "GET", "/v1/tasks/"+s.ScopeID.String(), nil, &taskResp) == nil {
							taskWorkStatus = taskResp.Data.WorkStatus
							// Prefer session title (user-named); fall back to task title
							displayTitle := name
							if displayTitle == "" {
								displayTitle = strings.TrimSpace(taskResp.Data.Title)
							}
							if taskResp.Data.TaskNumber > 0 && displayTitle != "" {
								name = fmt.Sprintf("OC-%d: %s", taskResp.Data.TaskNumber, displayTitle)
							} else if taskResp.Data.TaskNumber > 0 {
								name = fmt.Sprintf("OC-%d", taskResp.Data.TaskNumber)
							} else {
								name = displayTitle
							}
						}
					}
					// Resolve a descriptive name from the scope object (for other scope types)
					if name == "" && s.ScopeID != uuid.Nil {
						switch strings.ToLower(s.ScopeType) {
						case "project":
							var projResp struct {
								Data struct {
									DisplayName string `json:"display_name"`
								} `json:"data"`
							}
							if apiClient.request(ctx, "GET", "/v1/projects/"+s.ScopeID.String(), nil, &projResp) == nil {
								name = strings.TrimSpace(projResp.Data.DisplayName)
							}
						}
					}
					if name == "" {
						name = s.ScopeType + " session"
					}
					updatedAt := s.CreatedAt
					if s.LastMessageAt != nil {
						updatedAt = *s.LastMessageAt
					}
					scopeID := ""
					if s.ScopeID != uuid.Nil {
						scopeID = s.ScopeID.String()
					}
					out = append(out, tuiapp.SidebarChatItem{
						SessionID:   s.ID.String(),
						DisplayName: name,
						UpdatedAt:   updatedAt,
						ScopeType:   strings.ToLower(s.ScopeType),
						ScopeID:     scopeID,
						WorkStatus:  taskWorkStatus,
					})
					if len(out) >= 4 {
						break
					}
				}
				return out, nil
			}
			runtimeHints.LoadProjects = func(ctx context.Context) ([]tuiapp.SidebarProjectItem, error) {
				var resp struct {
					Data []struct {
						ID          string    `json:"id"`
						DisplayName string    `json:"display_name"`
						UpdatedAt   time.Time `json:"updated_at"`
					} `json:"data"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/projects?limit=50", nil, &resp); err != nil {
					return nil, err
				}
				out := make([]tuiapp.SidebarProjectItem, 0, len(resp.Data))
				for _, p := range resp.Data {
					out = append(out, tuiapp.SidebarProjectItem{
						ID:          p.ID,
						DisplayName: p.DisplayName,
						UpdatedAt:   p.UpdatedAt,
					})
				}
				return out, nil
			}
			runtimeHints.LoadProjectTasks = func(ctx context.Context, projectID string) ([]tuiapp.SidebarTaskItem, error) {
				var resp struct {
					Data []struct {
						ID         string `json:"id"`
						Title      string `json:"title"`
						WorkStatus string `json:"work_status"`
						TaskNumber int    `json:"task_number"`
					} `json:"data"`
				}
				path := "/v1/projects/" + url.PathEscape(projectID) + "/tasks?limit=20"
				if err := apiClient.request(ctx, "GET", path, nil, &resp); err != nil {
					return nil, err
				}
				out := make([]tuiapp.SidebarTaskItem, 0, len(resp.Data))
				for _, t := range resp.Data {
					out = append(out, tuiapp.SidebarTaskItem{
						ID:         t.ID,
						Title:      t.Title,
						WorkStatus: t.WorkStatus,
						TaskNumber: t.TaskNumber,
					})
				}
				return out, nil
			}
			runtimeHints.LoadProjectDetail = func(ctx context.Context, projectID string) (*tuiapp.ProjectDetail, error) {
				var projResp struct {
					Data struct {
						ID           string `json:"id"`
						DisplayName  string `json:"display_name"`
						Description  string `json:"description"`
						DeliveryMode string `json:"delivery_mode"`
					} `json:"data"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/projects/"+url.PathEscape(projectID), nil, &projResp); err != nil {
					return nil, err
				}
				proj := projResp.Data
				var tasksResp struct {
					Data []struct {
						ID         string `json:"id"`
						Title      string `json:"title"`
						WorkStatus string `json:"work_status"`
						TaskNumber int    `json:"task_number"`
					} `json:"data"`
				}
				path := "/v1/projects/" + url.PathEscape(projectID) + "/tasks?limit=20"
				_ = apiClient.request(ctx, "GET", path, nil, &tasksResp)
				tasks := make([]tuiapp.SidebarTaskItem, 0, len(tasksResp.Data))
				doneTasks := make([]tuiapp.SidebarTaskItem, 0)
				for _, t := range tasksResp.Data {
					item := tuiapp.SidebarTaskItem{
						ID:         t.ID,
						Title:      t.Title,
						WorkStatus: t.WorkStatus,
						TaskNumber: t.TaskNumber,
					}
					if t.WorkStatus == "done" || t.WorkStatus == "approved" || t.WorkStatus == "cancelled" {
						doneTasks = append(doneTasks, item)
					} else {
						tasks = append(tasks, item)
					}
				}
				return &tuiapp.ProjectDetail{
					ID:           proj.ID,
					DisplayName:  proj.DisplayName,
					Description:  proj.Description,
					DeliveryMode: proj.DeliveryMode,
					Tasks:        tasks,
					DoneTasks:    doneTasks,
					DoneCount:    len(doneTasks),
				}, nil
			}
			runtimeHints.LoadAgents = func(ctx context.Context) ([]string, error) {
				var resp struct {
					Data []struct {
						DisplayName     string `json:"display_name"`
						LifecycleStatus string `json:"lifecycle_status"`
					} `json:"data"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/agents?limit=20", nil, &resp); err != nil {
					return nil, err
				}
				out := make([]string, 0, len(resp.Data))
				for _, a := range resp.Data {
					status := a.LifecycleStatus
					if status == "" {
						status = "unknown"
					}
					out = append(out, a.DisplayName+"="+status)
				}
				return out, nil
			}
			runtimeHints.LoadTaskDetail = func(ctx context.Context, taskID string) (*tuiapp.TaskDetailItem, error) {
				var resp struct {
					Data struct {
						ID                  string  `json:"id"`
						TaskNumber          int     `json:"task_number"`
						Title               string  `json:"title"`
						Description         *string `json:"description"`
						WorkStatus          string  `json:"work_status"`
						AssignedAgentID     string  `json:"assigned_agent_id"`
						RequiresHumanReview bool    `json:"requires_human_review"`
						BranchName          *string `json:"branch_name"`
						CurrentFlowNode     *struct {
							ID          string `json:"id"`
							DisplayName string `json:"display_name"`
						} `json:"current_flow_node"`
					} `json:"data"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/tasks/"+url.PathEscape(taskID), nil, &resp); err != nil {
					return nil, err
				}
				d := resp.Data
				desc := ""
				if d.Description != nil {
					desc = *d.Description
				}
				item := &tuiapp.TaskDetailItem{
					ID:                  d.ID,
					TaskNumber:          d.TaskNumber,
					Title:               d.Title,
					Description:         desc,
					WorkStatus:          d.WorkStatus,
					RequiresHumanReview: d.RequiresHumanReview,
				}
				if d.BranchName != nil {
					item.BranchName = *d.BranchName
				}
				if d.CurrentFlowNode != nil {
					item.FlowNodeName = d.CurrentFlowNode.DisplayName
				}
				// Fetch assigned agent display name if present
				if d.AssignedAgentID != "" {
					var agentResp struct {
						Data struct {
							DisplayName string `json:"display_name"`
						} `json:"data"`
					}
					if apiClient.request(ctx, "GET", "/v1/agents/"+url.PathEscape(d.AssignedAgentID), nil, &agentResp) == nil {
						item.AgentName = agentResp.Data.DisplayName
					}
				}
				// Fetch flow pipeline data
				var flowResp struct {
					Data struct {
						CurrentNode *struct {
							ID          string `json:"id"`
							DisplayName string `json:"display_name"`
							NodeType    string `json:"node_type"`
						} `json:"current_node"`
						Executions []struct {
							FlowNodeID string `json:"flow_node_id"`
							Status     string `json:"status"`
						} `json:"executions"`
						Subtasks []struct {
							Title  string `json:"title"`
							Status string `json:"status"`
						} `json:"subtasks"`
					} `json:"data"`
				}
				flowPath := "/v1/tasks/" + url.PathEscape(taskID) + "/flow"
				if apiClient.request(ctx, "GET", flowPath, nil, &flowResp) == nil {
					f := flowResp.Data
					// Build flow steps from executions
					if f.CurrentNode != nil {
						// Map execution statuses by node ID
						execMap := map[string]string{}
						for _, ex := range f.Executions {
							execMap[ex.FlowNodeID] = ex.Status
						}
						step := tuiapp.FlowStep{
							Name:     f.CurrentNode.DisplayName,
							NodeType: f.CurrentNode.NodeType,
						}
						if status, ok := execMap[f.CurrentNode.ID]; ok {
							step.Status = status
						} else {
							step.Status = "pending"
						}
						item.FlowSteps = append(item.FlowSteps, step)
					}
					// Subtasks
					for _, st := range f.Subtasks {
						item.SubtaskItems = append(item.SubtaskItems, tuiapp.SubtaskItem{
							Title:  st.Title,
							Status: st.Status,
						})
					}
				}
				// Find the task's async chat session (scope_type=project_task)
				var sessResp struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				sessPath := "/v1/chat-sessions?scope_type=project_task&scope_id=" + url.QueryEscape(taskID)
				if apiClient.request(ctx, "GET", sessPath, nil, &sessResp) == nil && len(sessResp.Data) > 0 {
					item.SessionID = sessResp.Data[0].ID
				}
				return item, nil
			}
			runtimeHints.SendChatMessage = func(ctx context.Context, sessionID, content string) error {
				resolvedID, resolveErr := resolveTUIChatSessionID(ctx, apiClient, sessionID)
				if resolveErr != nil {
					return resolveErr
				}
				// Notify the model of the resolved session UUID so it can filter
				// cross-session SSE events (prevents leakage from other sessions).
				if program != nil {
					program.Send(tuiapp.SessionResolvedMsg{SessionID: resolvedID.String()})
				}
				_, sendErr := apiClient.SendChatMessage(ctx, resolvedID, content)
				return sendErr
			}
			runtimeHints.CancelChatTurn = func(ctx context.Context, sessionID string) error {
				resolvedID, resolveErr := resolveTUIChatSessionID(ctx, apiClient, sessionID)
				if resolveErr != nil {
					return resolveErr
				}
				return apiClient.CancelChatTurn(ctx, resolvedID)
			}
			runtimeHints.LoadChatHistory = func(ctx context.Context, sessionID string) ([]tuiapp.ChatMessage, error) {
				resolvedID, resolveErr := resolveTUIChatSessionID(ctx, apiClient, sessionID)
				if resolveErr != nil {
					return nil, resolveErr
				}
				result, err := apiClient.ListChatMessages(ctx, resolvedID, chatListMessagesFilter{Limit: 200})
				if err != nil {
					return nil, err
				}
				out := make([]tuiapp.ChatMessage, 0, len(result.Data))
				for _, m := range result.Data {
					role := strings.ToLower(strings.TrimSpace(m.Role))
					if role == "tool_result" || role == "tool_calls" {
						continue
					}
					content := m.Content
					if m.IsRedacted {
						content = "[redacted]"
					}
					if strings.TrimSpace(content) == "" {
						continue
					}
					out = append(out, tuiapp.ChatMessage{
						ID:        m.ID.String(),
						Role:      m.Role,
						Content:   content,
						Finalized: true,
						Timestamp: m.CreatedAt,
					})
				}
				return out, nil
			}
		}
	}

	program = tea.NewProgram(tuiapp.NewModelWithRuntime(state, runtimeHints), tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Wire up SSE realtime connection using stored credentials.
	if serverURL != "" && apiKey != "" {
		ctx, cancel := context.WithCancel(context.Background())
		reducer := tuiapp.NewEventReducer(nil)
		client := &tuiapp.RealtimeClient{
			Connector: tuiapp.HTTPSSEConnector{
				URL:    strings.TrimRight(serverURL, "/") + "/v1/events/stream",
				APIKey: apiKey,
				Scopes: "org",
			},
			Reducer: reducer,
			OnStateChange: func(state tuiapp.ConnectionState, degraded bool) {
				program.Send(tuiapp.ConnectionStateMsg{State: state, Degraded: degraded})
			},
			OnReplaySynced: func() {
				program.Send(tuiapp.ReplaySyncedMsg{})
			},
			OnEvent: func(event tuiapp.EventEnvelope, applied bool) {
				if !applied {
					return
				}
				// EX-161: chat.session.* events have workspace handlers (reload sidebar,
				// add activity entry) in applyWorkspaceCommand — route them there.
				// All other chat.* events (message, turn, tool_call) go to applyChatEnvelope.
				if strings.HasPrefix(event.EventType, "chat.") && !strings.HasPrefix(event.EventType, "chat.session.") {
					program.Send(tuiapp.ChatEnvelopeMsg{Envelope: event})
				} else {
					program.Send(tuiapp.WorkspaceEnvelopeMsg{Envelope: event})
				}
			},
		}
		go func() {
			defer cancel()
			_ = client.Run(ctx)
		}()
		defer cancel()
	}

	finalModel, err := program.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui runtime error: %v\n", err)
		return 1
	}

	nextState := state
	switch typed := finalModel.(type) {
	case tuiapp.Model:
		nextState = typed.State()
	case *tuiapp.Model:
		nextState = typed.State()
	}
	if err := tuiapp.SaveState(statePath, nextState); err != nil {
		fmt.Fprintf(os.Stderr, "tui state save error: %v\n", err)
		return 1
	}

	return 0
}

func resolveTUIRealtimeCredentials(serverURLFlag, apiKeyFlag string) (string, string, error) {
	creds, err := credentialStore.Load()
	if err != nil {
		return "", "", err
	}

	serverURL := strings.TrimSpace(serverURLFlag)
	if serverURL == "" {
		serverURL = strings.TrimSpace(globalServerURL)
	}
	serverURL = clitools.ResolveServerURL(serverURL, creds)

	apiKey := strings.TrimSpace(apiKeyFlag)
	if apiKey == "" {
		apiKey = strings.TrimSpace(globalAPIKey)
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(creds.APIKey)
	}
	if apiKey == "" {
		return "", "", fmt.Errorf("api key required via --api-key, OTTERCAMP_API_KEY, or ~/.ottercamp/credentials")
	}

	return serverURL, apiKey, nil
}

func printTUIUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp tui [--server-url URL] [--api-key KEY] [--non-interactive]")
}

func resolveTUIChatSessionID(ctx context.Context, client *cliAPIClient, sessionID string) (uuid.UUID, error) {
	trimmed := strings.TrimSpace(sessionID)
	if id, err := uuid.Parse(trimmed); err == nil {
		return id, nil
	}

	scopeType := ""
	switch {
	case strings.HasPrefix(trimmed, "session-org-"), trimmed == "session-general",
		trimmed == "org-session":
		scopeType = "organization"
	case strings.HasPrefix(trimmed, "session-task-"), trimmed == "task-session":
		scopeType = "project_task"
	case strings.HasPrefix(trimmed, "session-project-"), trimmed == "project-session":
		scopeType = "project"
	default:
		return uuid.Nil, fmt.Errorf("unknown session %q: expected UUID or known TUI alias", trimmed)
	}

	sessions, err := client.ListChatSessions(ctx, chatListSessionsFilter{
		Status:    "active",
		ScopeType: scopeType,
		Limit:     20,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve %s session: %w", scopeType, err)
	}
	if len(sessions.Data) == 0 {
		return uuid.Nil, fmt.Errorf("no active %s chat session found", scopeType)
	}
	for _, session := range sessions.Data {
		if strings.EqualFold(session.Mode, "sync") {
			return session.ID, nil
		}
	}
	return sessions.Data[0].ID, nil
}
