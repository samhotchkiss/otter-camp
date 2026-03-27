package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	tuiapp "github.com/samhotchkiss/otter-camp/internal/tui"
	versionpkg "github.com/samhotchkiss/otter-camp/internal/version"
)

var tuiRateLimitBackoffSchedule = []time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	1 * time.Second,
}

func isTUIRateLimitError(err error) bool {
	var apiErr *cliAPIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests
}

func sleepTUIBackoff(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func withTUIRateLimitRetryErr(ctx context.Context, fn func() error) error {
	_, err := withTUIRateLimitRetryValue(ctx, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

func withTUIRateLimitRetryValue[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		value, err := fn()
		if err == nil {
			return value, nil
		}
		if !isTUIRateLimitError(err) || attempt >= len(tuiRateLimitBackoffSchedule) {
			return zero, err
		}
		if waitErr := sleepTUIBackoff(ctx, tuiRateLimitBackoffSchedule[attempt]); waitErr != nil {
			return zero, waitErr
		}
	}
}

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
	freshness := versionpkg.CachedStartupFreshness()
	runtimeHints.BinaryStale = freshness.Stale
	runtimeHints.BinaryMetadataWarning = freshness.MetadataOnly()

	// Use a pointer so the SendChatMessage closure can reference program after it's created.
	var program *tea.Program

	if serverURL != "" && apiKey != "" {
		apiClient, err := newCLIAPIClient(serverURL, apiKey)
		if err == nil {
			runtimeHints.LoadOrgSession = func(ctx context.Context) (string, error) {
				sessions, err := apiClient.ListChatSessions(ctx, chatListSessionsFilter{
					Status:    "active",
					ScopeType: "organization",
					Limit:     5,
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
			runtimeHints.ConnectProjectRemote = func(ctx context.Context, projectID, repoURL string) error {
				body := map[string]interface{}{
					"name":       "github",
					"url":        repoURL,
					"transport":  "https",
					"is_default": true,
				}
				var resp struct{}
				return apiClient.request(ctx, "POST", "/v1/projects/"+url.PathEscape(projectID)+"/remotes", body, &resp)
			}
			runtimeHints.RelaunchProject = func(ctx context.Context, projectID string) (string, error) {
				var resp struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if err := apiClient.request(ctx, "POST", "/v1/projects/"+url.PathEscape(projectID)+"/relaunch", map[string]any{}, &resp); err != nil {
					return "", err
				}
				return strings.TrimSpace(resp.Data.ID), nil
			}
			runtimeHints.ArchiveProject = func(ctx context.Context, projectID string) error {
				return withTUIRateLimitRetryErr(ctx, func() error {
					var resp struct{}
					return apiClient.request(ctx, "POST", "/v1/projects/"+url.PathEscape(projectID)+"/archive", map[string]any{}, &resp)
				})
			}
			runtimeHints.LoadRecentChats = func(ctx context.Context) ([]tuiapp.SidebarChatItem, error) {
				return loadTUIRecentChats(ctx, apiClient)
			}
			runtimeHints.LoadProjects = func(ctx context.Context) ([]tuiapp.SidebarProjectItem, error) {
				return loadTUIProjects(ctx, apiClient)
			}
			runtimeHints.LoadProjectTasks = func(ctx context.Context, projectID string) ([]tuiapp.SidebarTaskItem, error) {
				var resp struct {
					Data []struct {
						ID         string `json:"id"`
						Title      string `json:"title"`
						WorkStatus string `json:"work_status"`
						TaskNumber int    `json:"task_number"`
						Priority   int    `json:"priority"`
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
						Priority:   t.Priority,
					})
				}
				return out, nil
			}
			runtimeHints.LoadProjectDetail = func(ctx context.Context, projectID string) (*tuiapp.ProjectDetail, error) {
				return loadTUIProjectDetail(ctx, apiClient, projectID)
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
			runtimeHints.LoadOperatorDashboard = func(ctx context.Context) (*tuiapp.OperatorDashboardData, error) {
				return loadTUIOperatorDashboard(ctx, apiClient)
			}
			runtimeHints.LoadTaskDetail = func(ctx context.Context, taskID string) (*tuiapp.TaskDetailItem, error) {
				return loadTUITaskDetail(ctx, apiClient, taskID)
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
			runtimeHints.ResetOrgSession = func(ctx context.Context, currentSessionID string) (string, error) {
				// Archive the current active sync session (not just whatever the TUI is displaying).
				sessions, err := withTUIRateLimitRetryValue(ctx, func() (chatSessionListEnvelope, error) {
					return apiClient.ListChatSessions(ctx, chatListSessionsFilter{Status: "active", ScopeType: "organization", Limit: 20})
				})
				if err != nil {
					return "", fmt.Errorf("list sessions: %w", err)
				}
				for _, s := range sessions.Data {
					if strings.EqualFold(s.Mode, "sync") {
						_ = withTUIRateLimitRetryErr(ctx, func() error {
							return apiClient.ArchiveChatSession(ctx, s.ID)
						})
					}
				}
				// Also archive the displayed session if it's different.
				if currentID, err := uuid.Parse(strings.TrimSpace(currentSessionID)); err == nil {
					_ = withTUIRateLimitRetryErr(ctx, func() error {
						return apiClient.ArchiveChatSession(ctx, currentID)
					})
				}
				// Resolve org ID and create a fresh sync session.
				var orgResp struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if err := withTUIRateLimitRetryErr(ctx, func() error {
					return apiClient.request(ctx, "GET", "/v1/orgs/current", nil, &orgResp)
				}); err != nil {
					return "", fmt.Errorf("resolve org: %w", err)
				}
				created, err := withTUIRateLimitRetryValue(ctx, func() (chatSessionEnvelope, error) {
					return apiClient.CreateChatSession(ctx, chatCreateSessionRequest{
						ScopeType: "organization",
						ScopeID:   orgResp.Data.ID,
						Mode:      "sync",
					})
				})
				if err != nil {
					return "", err
				}
				return created.Data.ID.String(), nil
			}
			runtimeHints.LoadSettings = func(ctx context.Context) (*tuiapp.SettingsData, error) {
				// 1. Load model profiles
				var profilesResp struct {
					Data []struct {
						LogicalProfileID string `json:"logical_profile_id"`
						ProviderID       string `json:"provider_id"`
						ModelName        string `json:"model_name"`
					} `json:"data"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/model/profiles", nil, &profilesResp); err != nil {
					return nil, fmt.Errorf("load profiles: %w", err)
				}

				// 2. Load providers
				var providersResp struct {
					Data []struct {
						ID          string `json:"id"`
						Slug        string `json:"slug"`
						DisplayName string `json:"display_name"`
						IsEnabled   bool   `json:"is_enabled"`
					} `json:"data"`
				}
				if err := apiClient.request(ctx, "GET", "/v1/model/providers", nil, &providersResp); err != nil {
					return nil, fmt.Errorf("load providers: %w", err)
				}

				// Build provider ID→name map
				providerNames := make(map[string]string)
				for _, p := range providersResp.Data {
					providerNames[p.ID] = p.DisplayName
				}

				// Build profiles
				profiles := make([]tuiapp.ModelProfileItem, 0, len(profilesResp.Data))
				for _, p := range profilesResp.Data {
					profiles = append(profiles, tuiapp.ModelProfileItem{
						LogicalID:    p.LogicalProfileID,
						ProviderID:   p.ProviderID,
						ProviderName: providerNames[p.ProviderID],
						ModelName:    p.ModelName,
					})
				}

				// 3. Load connections for each provider
				providers := make([]tuiapp.ProviderItem, 0, len(providersResp.Data))
				for _, prov := range providersResp.Data {
					var connsResp struct {
						Data []struct {
							ID          string `json:"id"`
							DisplayName string `json:"display_name"`
							AuthMode    string `json:"auth_mode"`
							IsEnabled   bool   `json:"is_enabled"`
							Health      string `json:"health"`
						} `json:"data"`
					}
					connPath := "/v1/model/providers/" + url.PathEscape(prov.ID) + "/connections"
					_ = apiClient.request(ctx, "GET", connPath, nil, &connsResp)

					conns := make([]tuiapp.ConnectionItem, 0, len(connsResp.Data))
					for _, c := range connsResp.Data {
						conns = append(conns, tuiapp.ConnectionItem{
							ID:          c.ID,
							ProviderID:  prov.ID,
							DisplayName: c.DisplayName,
							AuthMode:    c.AuthMode,
							IsEnabled:   c.IsEnabled,
							Health:      c.Health,
						})
					}
					providers = append(providers, tuiapp.ProviderItem{
						ID:          prov.ID,
						Slug:        prov.Slug,
						DisplayName: prov.DisplayName,
						IsEnabled:   prov.IsEnabled,
						Connections: conns,
					})
				}

				// 4. Load secrets
				var secretsResp struct {
					Data []struct {
						Slug        string `json:"slug"`
						DisplayName string `json:"display_name"`
					} `json:"data"`
				}
				_ = apiClient.request(ctx, "GET", "/v1/secrets", nil, &secretsResp)

				secrets := make([]tuiapp.SecretItem, 0, len(secretsResp.Data))
				for _, s := range secretsResp.Data {
					secrets = append(secrets, tuiapp.SecretItem{
						Slug:        s.Slug,
						DisplayName: s.DisplayName,
					})
				}

				return &tuiapp.SettingsData{
					Profiles:  profiles,
					Providers: providers,
					Secrets:   secrets,
				}, nil
			}
			runtimeHints.CreateSecret = func(ctx context.Context, slug, value string) error {
				body := map[string]string{"slug": slug, "value": value, "display_name": slug}
				var resp struct{}
				return apiClient.request(ctx, "POST", "/v1/secrets", body, &resp)
			}
			runtimeHints.DeleteSecret = func(ctx context.Context, slug string) error {
				return apiClient.request(ctx, "DELETE", "/v1/secrets/"+url.PathEscape(slug), nil, nil)
			}
			runtimeHints.UpdateModelProfile = func(ctx context.Context, profileID, providerID, modelName string) error {
				body := map[string]any{"provider_id": providerID, "model_name": modelName}
				var resp struct{}
				return apiClient.request(ctx, "PATCH", "/v1/model/profiles/"+url.PathEscape(profileID), body, &resp)
			}
			runtimeHints.UpdateConnectionAuth = func(ctx context.Context, providerID, connectionID, authMode, secretSlug string) error {
				body := map[string]any{"auth_mode": authMode}
				if authMode == "subscription" {
					body["subscription_access_token_secret_ref"] = "ref:" + secretSlug
				}
				path := "/v1/model/providers/" + url.PathEscape(providerID) + "/connections/" + url.PathEscape(connectionID)
				var resp struct{}
				return apiClient.request(ctx, "PATCH", path, body, &resp)
			}
			runtimeHints.CreateConnection = func(ctx context.Context, providerID, displayName, apiKeySecretSlug string, failoverPriority int) error {
				body := map[string]any{
					"display_name":       displayName,
					"api_key_secret_ref": "ref:" + apiKeySecretSlug,
					"failover_priority":  failoverPriority,
				}
				path := "/v1/model/providers/" + url.PathEscape(providerID) + "/connections"
				var resp struct{}
				return apiClient.request(ctx, "POST", path, body, &resp)
			}
			runtimeHints.Login = func(ctx context.Context, email, password string) error {
				base := strings.TrimRight(serverURL, "/")

				// Step 1: authenticate
				loginBody, _ := json.Marshal(map[string]string{"email": email, "password": password})
				loginResp, err := http.Post(base+"/v1/auth/login", "application/json", bytes.NewReader(loginBody))
				if err != nil {
					return fmt.Errorf("connect error: %w", err)
				}
				defer loginResp.Body.Close()
				respBody, _ := io.ReadAll(io.LimitReader(loginResp.Body, 64<<10))
				if loginResp.StatusCode != http.StatusOK {
					return fmt.Errorf("login returned %d: %s", loginResp.StatusCode, strings.TrimSpace(string(respBody)))
				}
				var loginResult struct {
					Data struct {
						SessionToken string `json:"session_token"`
					} `json:"data"`
				}
				if err := json.Unmarshal(respBody, &loginResult); err != nil || loginResult.Data.SessionToken == "" {
					return fmt.Errorf("failed to parse session token")
				}

				// Step 2: create a TUI-capable API key
				keyBody, _ := json.Marshal(map[string]any{
					"display_name": "tui-admin",
					"scopes":       interactiveClientAPIKeyScopes(),
				})
				keyReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/api-keys", bytes.NewReader(keyBody))
				keyReq.Header.Set("Content-Type", "application/json")
				keyReq.Header.Set("Authorization", "Bearer "+loginResult.Data.SessionToken)
				keyResp, err := http.DefaultClient.Do(keyReq)
				if err != nil {
					return fmt.Errorf("create API key error: %w", err)
				}
				defer keyResp.Body.Close()
				keyRespBody, _ := io.ReadAll(io.LimitReader(keyResp.Body, 64<<10))
				if keyResp.StatusCode != http.StatusOK && keyResp.StatusCode != http.StatusCreated {
					return fmt.Errorf("API key creation returned %d: %s", keyResp.StatusCode, strings.TrimSpace(string(keyRespBody)))
				}
				var keyResult struct {
					Data struct {
						Key string `json:"key"`
					} `json:"data"`
				}
				if err := json.Unmarshal(keyRespBody, &keyResult); err != nil || keyResult.Data.Key == "" {
					return fmt.Errorf("failed to parse API key")
				}

				// Step 3: save credentials and hot-swap
				store := clitools.NewCredentialStore()
				if err := store.Save(clitools.Credentials{ServerURL: base, APIKey: keyResult.Data.Key}); err != nil {
					return fmt.Errorf("save credentials error: %w", err)
				}
				apiClient.apiKey = keyResult.Data.Key
				return nil
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

	program = tea.NewProgram(tuiapp.NewModelWithRuntime(state, runtimeHints), tea.WithAltScreen(), tea.WithMouseAllMotion())

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

func loadTUIRecentChats(ctx context.Context, apiClient *cliAPIClient) ([]tuiapp.SidebarChatItem, error) {
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
		if strings.EqualFold(s.ScopeType, "project") {
			var projResp struct {
				Data struct {
					DisplayName string `json:"display_name"`
					Slug        string `json:"slug"`
				} `json:"data"`
			}
			if s.ScopeID != uuid.Nil && apiClient.request(ctx, "GET", "/v1/projects/"+s.ScopeID.String(), nil, &projResp) == nil {
				name = tuiapp.ResolveProjectLabel(projResp.Data.DisplayName, projResp.Data.Slug, name)
			} else {
				name = tuiapp.ResolveProjectLabel(name, "", "")
			}
		}
		if name == "" {
			if strings.EqualFold(s.ScopeType, "project") {
				name = tuiapp.ResolveProjectLabel("", "", "")
			} else {
				name = s.ScopeType + " session"
			}
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

func loadTUIProjects(ctx context.Context, apiClient *cliAPIClient) ([]tuiapp.SidebarProjectItem, error) {
	var resp struct {
		Data []struct {
			ID          string    `json:"id"`
			Slug        string    `json:"slug"`
			DisplayName string    `json:"display_name"`
			IsPaused    bool      `json:"is_paused"`
			PauseReason string    `json:"pause_reason"`
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
			Slug:        strings.TrimSpace(p.Slug),
			DisplayName: tuiapp.ResolveProjectLabel(p.DisplayName, p.Slug, ""),
			IsPaused:    p.IsPaused,
			PauseReason: strings.TrimSpace(p.PauseReason),
			UpdatedAt:   p.UpdatedAt,
		})
	}
	return out, nil
}

func loadTUIProjectDetail(ctx context.Context, apiClient *cliAPIClient, projectID string) (*tuiapp.ProjectDetail, error) {
	var projResp struct {
		Data struct {
			ID           string `json:"id"`
			Slug         string `json:"slug"`
			DisplayName  string `json:"display_name"`
			Description  string `json:"description"`
			DeliveryMode string `json:"delivery_mode"`
			IsPaused     bool   `json:"is_paused"`
			PauseReason  string `json:"pause_reason"`
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
			Priority   int    `json:"priority"`
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
			Priority:   t.Priority,
		}
		if t.WorkStatus == "done" || t.WorkStatus == "approved" || t.WorkStatus == "cancelled" {
			doneTasks = append(doneTasks, item)
		} else {
			tasks = append(tasks, item)
		}
	}
	var agentsResp struct {
		Data []struct {
			AgentDisplayName string `json:"agent_display_name"`
			Role             string `json:"role"`
			IsActive         bool   `json:"is_active"`
		} `json:"data"`
	}
	agentsPath := "/v1/projects/" + url.PathEscape(projectID) + "/agents"
	var agents []tuiapp.ProjectAgent
	if apiClient.request(ctx, "GET", agentsPath, nil, &agentsResp) == nil {
		for _, a := range agentsResp.Data {
			if a.IsActive {
				agents = append(agents, tuiapp.ProjectAgent{
					DisplayName: a.AgentDisplayName,
					Role:        a.Role,
				})
			}
		}
	}
	var remotesResp struct {
		Data []struct {
			URL       string `json:"url"`
			IsDefault bool   `json:"is_default"`
		} `json:"data"`
	}
	var repoURL string
	remotesPath := "/v1/projects/" + url.PathEscape(projectID) + "/remotes"
	if apiClient.request(ctx, "GET", remotesPath, nil, &remotesResp) == nil {
		for _, r := range remotesResp.Data {
			if r.IsDefault || repoURL == "" {
				repoURL = r.URL
			}
		}
	}
	var filesResp struct {
		Data struct {
			RepoURL  *string `json:"repo_url"`
			RepoPath *string `json:"repo_path"`
			Files    []struct {
				Path  string `json:"path"`
				IsDir bool   `json:"is_dir"`
				Depth int    `json:"depth"`
			} `json:"files"`
		} `json:"data"`
	}
	projectFiles := make([]tuiapp.ProjectFileEntry, 0)
	repoPath := ""
	filesPath := "/v1/projects/" + url.PathEscape(projectID) + "/files"
	if apiClient.request(ctx, "GET", filesPath, nil, &filesResp) == nil {
		if filesResp.Data.RepoURL != nil && strings.TrimSpace(*filesResp.Data.RepoURL) != "" {
			repoURL = strings.TrimSpace(*filesResp.Data.RepoURL)
		}
		if filesResp.Data.RepoPath != nil {
			repoPath = strings.TrimSpace(*filesResp.Data.RepoPath)
		}
		projectFiles = make([]tuiapp.ProjectFileEntry, 0, len(filesResp.Data.Files))
		for _, file := range filesResp.Data.Files {
			projectFiles = append(projectFiles, tuiapp.ProjectFileEntry{
				Path:  file.Path,
				IsDir: file.IsDir,
				Depth: file.Depth,
			})
		}
	}
	return &tuiapp.ProjectDetail{
		ID:           proj.ID,
		Slug:         strings.TrimSpace(proj.Slug),
		DisplayName:  tuiapp.ResolveProjectLabel(proj.DisplayName, proj.Slug, ""),
		Description:  proj.Description,
		DeliveryMode: proj.DeliveryMode,
		IsPaused:     proj.IsPaused,
		PauseReason:  strings.TrimSpace(proj.PauseReason),
		RepoURL:      repoURL,
		RepoPath:     repoPath,
		Files:        projectFiles,
		Tasks:        tasks,
		DoneTasks:    doneTasks,
		DoneCount:    len(doneTasks),
		Agents:       agents,
	}, nil
}

func loadTUIOperatorDashboard(ctx context.Context, apiClient *cliAPIClient) (*tuiapp.OperatorDashboardData, error) {
	var resp struct {
		Data struct {
			Summary struct {
				Health          string `json:"health"`
				QuietHealthy    bool   `json:"quiet_healthy"`
				ActiveProjects  int    `json:"active_projects"`
				ActiveTasks     int    `json:"active_tasks"`
				ActiveRuns      int    `json:"active_runs"`
				StaleTasks      int    `json:"stale_tasks"`
				StaleExecutions int    `json:"stale_executions"`
				BlockedItems    int    `json:"blocked_items"`
				RecentFailures  int    `json:"recent_failures"`
			} `json:"summary"`
			Active         tuiOperatorDashboardSectionPayload `json:"active"`
			Stale          tuiOperatorDashboardSectionPayload `json:"stale"`
			Blocked        tuiOperatorDashboardSectionPayload `json:"blocked"`
			RecentFailures tuiOperatorDashboardSectionPayload `json:"recent_failures"`
			RecentActivity tuiOperatorDashboardSectionPayload `json:"recent_activity"`
			Thresholds     struct {
				StaleExecutionSeconds int `json:"stale_execution_seconds"`
				StaleTaskSeconds      int `json:"stale_task_seconds"`
			} `json:"thresholds"`
			ServerTime time.Time `json:"server_time"`
		} `json:"data"`
	}
	if err := apiClient.request(ctx, "GET", "/v1/control/dashboard?limit=6", nil, &resp); err != nil {
		return nil, err
	}
	return &tuiapp.OperatorDashboardData{
		Summary: tuiapp.OperatorDashboardSummary{
			Health:          resp.Data.Summary.Health,
			QuietHealthy:    resp.Data.Summary.QuietHealthy,
			ActiveProjects:  resp.Data.Summary.ActiveProjects,
			ActiveTasks:     resp.Data.Summary.ActiveTasks,
			ActiveRuns:      resp.Data.Summary.ActiveRuns,
			StaleTasks:      resp.Data.Summary.StaleTasks,
			StaleExecutions: resp.Data.Summary.StaleExecutions,
			BlockedItems:    resp.Data.Summary.BlockedItems,
			RecentFailures:  resp.Data.Summary.RecentFailures,
		},
		Active:         convertTUIOperatorDashboardSection(resp.Data.Active),
		Stale:          convertTUIOperatorDashboardSection(resp.Data.Stale),
		Blocked:        convertTUIOperatorDashboardSection(resp.Data.Blocked),
		RecentFailures: convertTUIOperatorDashboardSection(resp.Data.RecentFailures),
		RecentActivity: convertTUIOperatorDashboardSection(resp.Data.RecentActivity),
		Thresholds: tuiapp.OperatorDashboardThresholds{
			StaleExecutionSeconds: resp.Data.Thresholds.StaleExecutionSeconds,
			StaleTaskSeconds:      resp.Data.Thresholds.StaleTaskSeconds,
		},
		ServerTime: resp.Data.ServerTime,
	}, nil
}

type tuiOperatorDashboardSectionPayload struct {
	Count      int                               `json:"count"`
	TotalCount int                               `json:"total_count"`
	Items      []tuiOperatorDashboardItemPayload `json:"items"`
}

type tuiOperatorDashboardRefPayload struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type tuiOperatorDashboardTaskRefPayload struct {
	ID         string `json:"id"`
	TaskNumber int    `json:"task_number"`
	Label      string `json:"label"`
}

type tuiOperatorDashboardItemPayload struct {
	Kind            string                              `json:"kind"`
	Title           string                              `json:"title"`
	Summary         string                              `json:"summary"`
	Status          string                              `json:"status"`
	Project         *tuiOperatorDashboardRefPayload     `json:"project"`
	Task            *tuiOperatorDashboardTaskRefPayload `json:"task"`
	Run             *tuiOperatorDashboardRefPayload     `json:"run"`
	UpdatedAt       time.Time                           `json:"updated_at"`
	AgeSeconds      int                                 `json:"age_seconds"`
	StaleForSeconds int                                 `json:"stale_for_seconds"`
	Links           struct {
		Project string `json:"project"`
		Task    string `json:"task"`
		Run     string `json:"run"`
	} `json:"links"`
}

func convertTUIOperatorDashboardSection(section tuiOperatorDashboardSectionPayload) tuiapp.OperatorDashboardSection {
	items := make([]tuiapp.OperatorDashboardItem, 0, len(section.Items))
	for _, item := range section.Items {
		items = append(items, tuiapp.OperatorDashboardItem{
			Kind:            item.Kind,
			Title:           item.Title,
			Summary:         item.Summary,
			Status:          item.Status,
			Project:         convertTUIOperatorDashboardRef(item.Project),
			Task:            convertTUIOperatorDashboardTaskRef(item.Task),
			Run:             convertTUIOperatorDashboardRef(item.Run),
			UpdatedAt:       item.UpdatedAt,
			AgeSeconds:      item.AgeSeconds,
			StaleForSeconds: item.StaleForSeconds,
			Links: tuiapp.OperatorDashboardLinks{
				Project: item.Links.Project,
				Task:    item.Links.Task,
				Run:     item.Links.Run,
			},
		})
	}
	return tuiapp.OperatorDashboardSection{
		Count:      section.Count,
		TotalCount: section.TotalCount,
		Items:      items,
	}
}

func convertTUIOperatorDashboardRef(ref *tuiOperatorDashboardRefPayload) *tuiapp.OperatorDashboardRef {
	if ref == nil {
		return nil
	}
	return &tuiapp.OperatorDashboardRef{ID: ref.ID, Label: ref.Label}
}

func convertTUIOperatorDashboardTaskRef(ref *tuiOperatorDashboardTaskRefPayload) *tuiapp.OperatorDashboardTaskRef {
	if ref == nil {
		return nil
	}
	return &tuiapp.OperatorDashboardTaskRef{
		ID:         ref.ID,
		TaskNumber: ref.TaskNumber,
		Label:      ref.Label,
	}
}

func loadTUITaskDetail(ctx context.Context, apiClient *cliAPIClient, taskID string) (*tuiapp.TaskDetailItem, error) {
	var resp struct {
		Data struct {
			ID                  string          `json:"id"`
			ProjectID           string          `json:"project_id"`
			TaskNumber          int             `json:"task_number"`
			Title               string          `json:"title"`
			Description         *string         `json:"description"`
			WorkStatus          string          `json:"work_status"`
			Priority            int             `json:"priority"`
			AssignedAgentID     string          `json:"assigned_agent_id"`
			RequiresHumanReview bool            `json:"requires_human_review"`
			BranchName          *string         `json:"branch_name"`
			Metadata            json.RawMessage `json:"metadata"`
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
		ProjectID:           d.ProjectID,
		TaskNumber:          d.TaskNumber,
		Title:               d.Title,
		Description:         desc,
		WorkStatus:          d.WorkStatus,
		Priority:            d.Priority,
		RequiresHumanReview: d.RequiresHumanReview,
	}
	stringValue := func(value *string) string {
		if value == nil {
			return ""
		}
		return strings.TrimSpace(*value)
	}
	if d.BranchName != nil {
		item.BranchName = *d.BranchName
	}
	if d.CurrentFlowNode != nil {
		item.FlowNodeName = d.CurrentFlowNode.DisplayName
	}
	if plan, ok := taskplan.Parse(d.Metadata); ok {
		report := taskplan.Evaluate(plan)
		item.PlanningPlaybook = plan.Playbook
		item.PlanningWorkType = plan.WorkType
		item.PlanningProjectStage = plan.ProjectStage
		item.PlanningEvidenceMaturity = plan.EvidenceMaturity
		item.PlanningRiskLevel = plan.RiskLevel
		for _, artifact := range plan.Artifacts {
			item.PlanningArtifacts = append(item.PlanningArtifacts, tuiapp.TaskPlanningArtifact{
				Slug:  artifact.Slug,
				Title: artifact.Title,
			})
		}
		item.PlanningFollowOns = append(item.PlanningFollowOns, plan.FollowOnSuggestions...)
		if report.Enforced {
			item.PlanningProcessStatus = report.ProcessStatus
			item.PlanningChecklist = append(item.PlanningChecklist, report.ReviewChecklist...)
			item.PlanningMissing = append(item.PlanningMissing, report.MissingRequirements()...)
		}
		if report.ProcessStatus == taskplan.ProcessStatusOverridden && plan.Override != nil {
			item.PlanningOverrideReason = strings.TrimSpace(plan.Override.Reason)
		}
	}
	if strings.EqualFold(strings.TrimSpace(d.WorkStatus), "blocked") {
		if guard, ok := tasksvc.ParseValidationGuard(d.Metadata); ok && guard.Blocked {
			item.BlockedReason = strings.TrimSpace(guard.FailureReason)
			if item.BlockedReason == "" {
				item.BlockedReason = strings.TrimSpace(guard.FailureCode)
			}
			if toolName := strings.TrimSpace(guard.ToolName); toolName != "" {
				switch {
				case item.BlockedReason != "":
					item.BlockedReason = toolName + " (" + item.BlockedReason + ")"
				default:
					item.BlockedReason = toolName
				}
			}
			if item.BlockedReason != "" {
				item.BlockedReason = "Deterministic validation loop blocked: " + item.BlockedReason
			} else {
				item.BlockedReason = "Deterministic validation loop blocked."
			}
			item.RecoveryHint = "Resume with: ottercamp task resume " + strings.TrimSpace(taskID)
		}
	}
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

	flowActiveExecutionID := ""
	flowRecentExecutionID := ""
	var flowResp struct {
		Data struct {
			CurrentNode *struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
				NodeType    string `json:"node_type"`
			} `json:"current_node"`
			CurrentExecution *struct {
				ID        string  `json:"id"`
				SessionID *string `json:"session_id"`
			} `json:"current_execution"`
			Nodes []struct {
				ID                string  `json:"id"`
				DisplayName       string  `json:"display_name"`
				NodeType          string  `json:"node_type"`
				Position          int     `json:"position"`
				ActorType         *string `json:"actor_type"`
				ActorLabel        string  `json:"actor_label"`
				State             string  `json:"state"`
				IsCurrent         bool    `json:"is_current"`
				NextNodeID        *string `json:"next_node_id"`
				RejectNodeID      *string `json:"reject_node_id"`
				VisitCount        int     `json:"visit_count"`
				CompletedVisits   int     `json:"completed_visits"`
				RejectedVisits    int     `json:"rejected_visits"`
				LatestExecutionID *string `json:"latest_execution_id"`
				LatestSessionID   *string `json:"latest_session_id"`
				SubtaskCounts     struct {
					Total      int `json:"total"`
					Done       int `json:"done"`
					InProgress int `json:"in_progress"`
					Pending    int `json:"pending"`
					Blocked    int `json:"blocked"`
					Cancelled  int `json:"cancelled"`
				} `json:"subtask_counts"`
				Executions []struct {
					ID            string     `json:"id"`
					FlowNodeID    string     `json:"flow_node_id"`
					VisitNumber   int        `json:"visit_number"`
					Status        string     `json:"status"`
					State         string     `json:"state"`
					SessionID     *string    `json:"session_id"`
					StartedAt     time.Time  `json:"started_at"`
					CompletedAt   *time.Time `json:"completed_at"`
					SubtaskCounts struct {
						Total      int `json:"total"`
						Done       int `json:"done"`
						InProgress int `json:"in_progress"`
						Pending    int `json:"pending"`
						Blocked    int `json:"blocked"`
						Cancelled  int `json:"cancelled"`
					} `json:"subtask_counts"`
					Subtasks []struct {
						Title      string `json:"title"`
						WorkStatus string `json:"work_status"`
					} `json:"subtasks"`
				} `json:"executions"`
			} `json:"nodes"`
			Edges []struct {
				FromNodeID string `json:"from_node_id"`
				ToNodeID   string `json:"to_node_id"`
				Kind       string `json:"kind"`
				IsBackEdge bool   `json:"is_back_edge"`
			} `json:"edges"`
			Executions []struct {
				ID          string     `json:"id"`
				FlowNodeID  string     `json:"flow_node_id"`
				Status      string     `json:"status"`
				SessionID   *string    `json:"session_id"`
				StartedAt   time.Time  `json:"started_at"`
				CompletedAt *time.Time `json:"completed_at"`
			} `json:"executions"`
			Subtasks []struct {
				Title      string `json:"title"`
				Status     string `json:"status"`
				WorkStatus string `json:"work_status"`
			} `json:"subtasks"`
		} `json:"data"`
	}
	flowPath := "/v1/tasks/" + url.PathEscape(taskID) + "/flow"
	if apiClient.request(ctx, "GET", flowPath, nil, &flowResp) == nil {
		f := flowResp.Data
		if f.CurrentNode != nil {
			item.FlowCurrentNodeID = f.CurrentNode.ID
			item.FlowNodeName = f.CurrentNode.DisplayName
		}
		if f.CurrentExecution != nil {
			flowActiveExecutionID = stringValue(f.CurrentExecution.SessionID)
		}
		for _, edge := range f.Edges {
			item.FlowEdges = append(item.FlowEdges, tuiapp.TaskFlowEdge{
				FromNodeID: edge.FromNodeID,
				ToNodeID:   edge.ToNodeID,
				Kind:       edge.Kind,
				IsBackEdge: edge.IsBackEdge,
			})
		}
		for _, node := range f.Nodes {
			flowNode := tuiapp.TaskFlowNode{
				ID:                node.ID,
				Name:              node.DisplayName,
				NodeType:          node.NodeType,
				Position:          node.Position,
				ActorType:         stringValue(node.ActorType),
				ActorLabel:        node.ActorLabel,
				State:             node.State,
				IsCurrent:         node.IsCurrent,
				NextNodeID:        stringValue(node.NextNodeID),
				RejectNodeID:      stringValue(node.RejectNodeID),
				VisitCount:        node.VisitCount,
				CompletedVisits:   node.CompletedVisits,
				RejectedVisits:    node.RejectedVisits,
				SessionID:         stringValue(node.LatestSessionID),
				LatestExecutionID: stringValue(node.LatestExecutionID),
				SubtaskCounts: tuiapp.TaskSubtaskCounts{
					Total:      node.SubtaskCounts.Total,
					Done:       node.SubtaskCounts.Done,
					InProgress: node.SubtaskCounts.InProgress,
					Pending:    node.SubtaskCounts.Pending,
					Blocked:    node.SubtaskCounts.Blocked,
					Cancelled:  node.SubtaskCounts.Cancelled,
				},
			}
			for _, execution := range node.Executions {
				flowExecution := tuiapp.TaskFlowExecution{
					ID:          execution.ID,
					FlowNodeID:  execution.FlowNodeID,
					VisitNumber: execution.VisitNumber,
					Status:      execution.Status,
					State:       execution.State,
					SessionID:   stringValue(execution.SessionID),
					StartedAt:   execution.StartedAt,
					CompletedAt: execution.CompletedAt,
					SubtaskCounts: tuiapp.TaskSubtaskCounts{
						Total:      execution.SubtaskCounts.Total,
						Done:       execution.SubtaskCounts.Done,
						InProgress: execution.SubtaskCounts.InProgress,
						Pending:    execution.SubtaskCounts.Pending,
						Blocked:    execution.SubtaskCounts.Blocked,
						Cancelled:  execution.SubtaskCounts.Cancelled,
					},
				}
				for _, subtask := range execution.Subtasks {
					flowExecution.Subtasks = append(flowExecution.Subtasks, tuiapp.SubtaskItem{
						Title:  subtask.Title,
						Status: subtask.WorkStatus,
					})
				}
				flowNode.Executions = append(flowNode.Executions, flowExecution)
			}
			if node.IsCurrent && len(flowNode.Executions) > 0 {
				item.SubtaskItems = append([]tuiapp.SubtaskItem(nil), flowNode.Executions[len(flowNode.Executions)-1].Subtasks...)
			}
			item.FlowNodes = append(item.FlowNodes, flowNode)
			item.FlowSteps = append(item.FlowSteps, tuiapp.FlowStep{
				Name:     node.DisplayName,
				NodeType: node.NodeType,
				Status:   node.State,
			})
		}
		if len(item.SubtaskItems) == 0 {
			for _, st := range f.Subtasks {
				status := strings.TrimSpace(st.Status)
				if status == "" {
					status = strings.TrimSpace(st.WorkStatus)
				}
				item.SubtaskItems = append(item.SubtaskItems, tuiapp.SubtaskItem{
					Title:  st.Title,
					Status: status,
				})
			}
		}
		for i := len(f.Executions) - 1; i >= 0; i-- {
			if sid := stringValue(f.Executions[i].SessionID); sid != "" {
				flowRecentExecutionID = sid
				break
			}
		}
		if flowActiveExecutionID == "" {
			flowActiveExecutionID = flowRecentExecutionID
		}
	}

	var depsResp struct {
		Data []struct {
			DependsOnID string `json:"depends_on_id"`
		} `json:"data"`
	}
	depsPath := "/v1/tasks/" + url.PathEscape(taskID) + "/dependencies"
	if apiClient.request(ctx, "GET", depsPath, nil, &depsResp) == nil {
		for _, dep := range depsResp.Data {
			item.Dependencies = append(item.Dependencies, tuiapp.TaskDependency{
				TaskID:    dep.DependsOnID,
				Direction: "depends_on",
			})
		}
	}

	var eventsResp struct {
		Data []struct {
			EventType  string         `json:"event_type"`
			ActorType  string         `json:"actor_type"`
			FlowNodeID *string        `json:"flow_node_id"`
			Payload    map[string]any `json:"payload"`
			CreatedAt  time.Time      `json:"created_at"`
		} `json:"data"`
	}
	eventsPath := "/v1/tasks/" + url.PathEscape(taskID) + "/events?limit=50"
	if apiClient.request(ctx, "GET", eventsPath, nil, &eventsResp) == nil {
		for i := len(eventsResp.Data) - 1; i >= 0; i-- {
			ev := eventsResp.Data[i]
			item.Events = append(item.Events, tuiapp.TaskEvent{
				EventType:  ev.EventType,
				ActorType:  ev.ActorType,
				FlowNodeID: stringValue(ev.FlowNodeID),
				Payload:    ev.Payload,
				CreatedAt:  ev.CreatedAt,
			})
		}
	}

	sessions, err := apiClient.ListChatSessions(ctx, chatListSessionsFilter{
		ScopeType: "project_task",
		ScopeID:   taskID,
		Limit:     50,
	})
	if err == nil {
		populateTUITaskDetailSessions(item, sessions.Data)
	}
	if item.ActiveExecutionSessionID == "" {
		item.ActiveExecutionSessionID = flowActiveExecutionID
	}
	if item.RecentExecutionSessionID == "" {
		item.RecentExecutionSessionID = flowRecentExecutionID
	}
	if item.SessionID == "" {
		switch {
		case item.ActiveExecutionSessionID != "":
			item.SessionID = item.ActiveExecutionSessionID
		case item.RecentExecutionSessionID != "":
			item.SessionID = item.RecentExecutionSessionID
		}
	}

	return item, nil
}

func populateTUITaskDetailSessions(item *tuiapp.TaskDetailItem, sessions []cliChatSession) {
	if item == nil {
		return
	}

	item.SessionID = ""
	item.DiscussionSessionID = ""
	item.ActiveExecutionSessionID = ""
	item.RecentExecutionSessionID = ""

	for _, session := range sessions {
		if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(session.Mode)) {
		case "sync":
			if item.DiscussionSessionID == "" {
				item.DiscussionSessionID = session.ID.String()
			}
		case "async":
			if item.RecentExecutionSessionID == "" {
				item.RecentExecutionSessionID = session.ID.String()
			}
			if strings.EqualFold(strings.TrimSpace(session.Status), "active") && item.ActiveExecutionSessionID == "" {
				item.ActiveExecutionSessionID = session.ID.String()
			}
		}
	}

	switch {
	case item.ActiveExecutionSessionID != "":
		item.SessionID = item.ActiveExecutionSessionID
	case item.RecentExecutionSessionID != "":
		item.SessionID = item.RecentExecutionSessionID
	}
}

func resolveTUIChatSessionID(ctx context.Context, client *cliAPIClient, sessionID string) (uuid.UUID, error) {
	trimmed := strings.TrimSpace(sessionID)
	if id, err := uuid.Parse(trimmed); err == nil {
		return id, nil
	}

	scopeType := ""
	scopeID := ""
	switch {
	case strings.HasPrefix(trimmed, "session-org-"), trimmed == "session-general",
		trimmed == "org-session":
		scopeType = "organization"
	case strings.HasPrefix(trimmed, "session-task-"), trimmed == "task-session":
		scopeType = "project_task"
		// Extract task ID if embedded: "session-task-<uuid>"
		if after, ok := strings.CutPrefix(trimmed, "session-task-"); ok {
			if _, err := uuid.Parse(after); err == nil {
				scopeID = after
			}
		}
	case strings.HasPrefix(trimmed, "session-project-"), trimmed == "project-session":
		scopeType = "project"
		// Extract project ID if embedded: "session-project-<uuid>"
		if after, ok := strings.CutPrefix(trimmed, "session-project-"); ok {
			if _, err := uuid.Parse(after); err == nil {
				scopeID = after
			}
		}
	default:
		return uuid.Nil, fmt.Errorf("unknown session %q: expected UUID or known TUI alias", trimmed)
	}

	filter := chatListSessionsFilter{
		Status:    "active",
		ScopeType: scopeType,
		Limit:     20,
	}
	if scopeID != "" {
		filter.ScopeID = scopeID
	}
	sessions, err := client.ListChatSessions(ctx, filter)
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolve %s session: %w", scopeType, err)
	}

	// If we have a project scope_id and no existing session, create one.
	if len(sessions.Data) == 0 && scopeID != "" && scopeType == "project" {
		created, createErr := client.CreateChatSession(ctx, chatCreateSessionRequest{
			ScopeType: scopeType,
			ScopeID:   scopeID,
			Mode:      "sync",
		})
		if createErr != nil {
			return uuid.Nil, fmt.Errorf("create %s session: %w", scopeType, createErr)
		}
		return created.Data.ID, nil
	}
	// If no active org session exists, create one so chat send from TUI works
	// without requiring a prior manual session bootstrap.
	if len(sessions.Data) == 0 && scopeType == "organization" {
		var orgResp struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := client.request(ctx, "GET", "/v1/orgs/current", nil, &orgResp); err != nil {
			return uuid.Nil, fmt.Errorf("resolve organization for chat session: %w", err)
		}
		created, createErr := client.CreateChatSession(ctx, chatCreateSessionRequest{
			ScopeType: "organization",
			ScopeID:   orgResp.Data.ID,
			Mode:      "sync",
		})
		if createErr != nil {
			return uuid.Nil, fmt.Errorf("create organization session: %w", createErr)
		}
		return created.Data.ID, nil
	}
	// If a specific project_task scope ID was provided, create missing session.
	if len(sessions.Data) == 0 && scopeType == "project_task" && scopeID != "" {
		created, createErr := client.CreateChatSession(ctx, chatCreateSessionRequest{
			ScopeType: scopeType,
			ScopeID:   scopeID,
			Mode:      "async",
		})
		if createErr != nil {
			return uuid.Nil, fmt.Errorf("create %s session: %w", scopeType, createErr)
		}
		return created.Data.ID, nil
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
