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
			runtimeHints.LoadInboxCount = func(ctx context.Context) (int, error) {
				var resp struct {
					Meta struct {
						Total int `json:"total"`
					} `json:"meta"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/inbox?is_acted=false&limit=1", nil, &resp); err != nil {
					return 0, err
				}
				return resp.Meta.Total, nil
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
					if name == "" {
						name = s.ScopeType + " session"
					}
					updatedAt := s.CreatedAt
					if s.LastMessageAt != nil {
						updatedAt = *s.LastMessageAt
					}
					out = append(out, tuiapp.SidebarChatItem{
						SessionID:   s.ID.String(),
						DisplayName: name,
						UpdatedAt:   updatedAt,
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
					} `json:"data"`
				}
				path := "/v1/projects/" + url.PathEscape(projectID) + "/tasks?limit=10"
				if err := apiClient.request(ctx, "GET", path, nil, &resp); err != nil {
					return nil, err
				}
				out := make([]tuiapp.SidebarTaskItem, 0, len(resp.Data))
				for _, t := range resp.Data {
					if t.WorkStatus == "done" || t.WorkStatus == "approved" || t.WorkStatus == "cancelled" {
						continue
					}
					out = append(out, tuiapp.SidebarTaskItem{
						ID:         t.ID,
						Title:      t.Title,
						WorkStatus: t.WorkStatus,
					})
				}
				return out, nil
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

	program = tea.NewProgram(tuiapp.NewModelWithRuntime(state, runtimeHints), tea.WithAltScreen())

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
				if strings.HasPrefix(event.EventType, "chat.") {
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
