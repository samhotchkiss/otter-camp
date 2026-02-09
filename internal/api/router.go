package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/samhotchkiss/otter-camp/internal/gitserver"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

var startTime = time.Now()

type HealthResponse struct {
	Status    string `json:"status"`
	Uptime    string `json:"uptime"`
	Version   string `json:"version"`
	Timestamp string `json:"timestamp"`
}

func NewRouter() http.Handler {
	r := chi.NewRouter()

	hub := ws.NewHub()
	go hub.Run()

	// Initialize database connection (graceful - demo mode if unavailable)
	var db *sql.DB
	var agentStore *store.AgentStore

	if dbConn, err := store.DB(); err != nil {
		log.Printf("⚠️  Database not available, using demo mode: %v", err)
	} else {
		db = dbConn
		agentStore = store.NewAgentStore(db)
		log.Printf("✅ Database connected, Postgres-backed stores ready")
	}

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Org-ID", "X-Workspace-ID", "X-Session-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// Only set Content-Type for API routes
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Set JSON content-type only for API routes
			if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" || r.URL.Path == "/health" || r.URL.Path == "/ws" {
				w.Header().Set("Content-Type", "application/json")
			}
			next.ServeHTTP(w, r)
		})
	})

	r.Get("/health", handleHealth)
	r.Get("/api/feed", FeedHandler)

	webhookHandler := &WebhookHandler{Hub: hub}
	feedPushHandler := NewFeedPushHandler(hub)
	execApprovalsHandler := &ExecApprovalsHandler{Hub: hub}
	taskHandler := &TaskHandler{Hub: hub}
	openClawWSHandler := ws.NewOpenClawHandler(hub)
	emissionBuffer := NewEmissionBuffer(defaultEmissionBufferSize)
	emissionsHandler := &EmissionsHandler{Buffer: emissionBuffer, Hub: hub}
	messageHandler := &MessageHandler{OpenClawDispatcher: openClawWSHandler, Hub: hub}
	attachmentsHandler := &AttachmentsHandler{}
	agentsHandler := &AgentsHandler{Store: agentStore, DB: db}
	workflowsHandler := &WorkflowsHandler{DB: db}
	openclawSyncHandler := &OpenClawSyncHandler{Hub: hub, DB: db, EmissionBuffer: emissionBuffer}
	adminConnectionsHandler := &AdminConnectionsHandler{DB: db, OpenClawHandler: openClawWSHandler}
	githubSyncDeadLettersHandler := &GitHubSyncDeadLettersHandler{}
	githubSyncHealthHandler := &GitHubSyncHealthHandler{}
	githubPullRequestsHandler := &GitHubPullRequestsHandler{}
	githubIntegrationHandler := NewGitHubIntegrationHandler(db)
	projectChatHandler := &ProjectChatHandler{Hub: hub, OpenClawDispatcher: openClawWSHandler}
	issuesHandler := &IssuesHandler{Hub: hub, OpenClawDispatcher: openClawWSHandler}
	projectCommitsHandler := &ProjectCommitsHandler{}
	projectTreeHandler := &ProjectTreeHandler{}
	knowledgeHandler := &KnowledgeHandler{}
	websocketHandler := &ws.Handler{Hub: hub}
	projectIssueSyncHandler := &ProjectIssueSyncHandler{}

	// Initialize project store and handler
	var projectStore *store.ProjectStore
	var githubSyncJobStore *store.GitHubSyncJobStore
	var projectRepoStore *store.ProjectRepoStore
	var activityStore *store.ActivityStore
	if db != nil {
		projectStore = store.NewProjectStore(db)
		githubSyncJobStore = store.NewGitHubSyncJobStore(db)
		projectRepoStore = store.NewProjectRepoStore(db)
		activityStore = store.NewActivityStore(db)
		adminConnectionsHandler.EventStore = store.NewConnectionEventStore(db)
		githubSyncDeadLettersHandler.Store = githubSyncJobStore
		githubSyncHealthHandler.Store = githubSyncJobStore
		githubPullRequestsHandler.Store = store.NewGitHubIssuePRStore(db)
		githubPullRequestsHandler.ProjectRepos = projectRepoStore
		githubIntegrationHandler.SyncJobs = githubSyncJobStore
		projectChatHandler.ChatStore = store.NewProjectChatStore(db)
		projectChatHandler.IssueStore = store.NewProjectIssueStore(db)
		projectChatHandler.DB = db
		issuesHandler.IssueStore = store.NewProjectIssueStore(db)
		issuesHandler.ProjectStore = projectStore
		issuesHandler.CommitStore = store.NewProjectCommitStore(db)
		issuesHandler.ProjectRepos = projectRepoStore
		issuesHandler.DB = db
		projectCommitsHandler.ProjectStore = projectStore
		projectCommitsHandler.CommitStore = store.NewProjectCommitStore(db)
		projectCommitsHandler.ProjectRepos = projectRepoStore
		projectTreeHandler.ProjectStore = projectStore
		projectTreeHandler.ProjectRepos = projectRepoStore
		projectIssueSyncHandler.Projects = projectStore
		projectIssueSyncHandler.ProjectRepos = githubIntegrationHandler.ProjectRepos
		projectIssueSyncHandler.Installations = githubIntegrationHandler.Installations
		projectIssueSyncHandler.SyncJobs = githubSyncJobStore
		projectIssueSyncHandler.IssueStore = issuesHandler.IssueStore
		knowledgeHandler.Store = store.NewKnowledgeEntryStore(db)
	}
	projectsHandler := &ProjectsHandler{Store: projectStore, DB: db}
	projectChatHandler.ProjectStore = projectStore
	websocketHandler.IssueAuthorizer = wsIssueSubscriptionAuthorizer{IssueStore: issuesHandler.IssueStore}

	if db != nil && projectStore != nil {
		gitHandler := &gitserver.Handler{
			RepoResolver: func(ctx context.Context, orgID, projectID string) (string, error) {
				if authOrg := gitserver.OrgIDFromContext(ctx); authOrg != "" && authOrg != orgID {
					return "", store.ErrForbidden
				}
				workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, orgID)
				return projectStore.GetRepoPath(workspaceCtx, projectID)
			},
			ActivityStore: activityStore,
			ProjectRepos:  projectRepoStore,
			SyncJobs:      githubSyncJobStore,
			Hub:           hub,
		}
		gitAuth := gitserver.AuthMiddleware(func(ctx context.Context, token string) (gitserver.AuthInfo, error) {
			if db == nil {
				return gitserver.AuthInfo{}, errors.New("database not available")
			}
			return validateGitToken(ctx, db, token)
		})
		r.Mount("/git", gitAuth(gitHandler.Routes()))
	}

	// All API routes under /api prefix
	r.Route("/api", func(r chi.Router) {
		r.Post("/waitlist", HandleWaitlist)
		r.Get("/search", SearchHandler)
		r.Get("/commands/search", CommandSearchHandler)
		r.Post("/commands/execute", CommandExecuteHandler)
		r.Get("/feed", FeedHandlerV2)
		r.Post("/feed", feedPushHandler.Handle)
		r.Post("/auth/login", HandleLogin)
		r.Post("/auth/exchange", HandleAuthExchange)
		r.Get("/auth/exchange", HandleAuthExchange)
		r.Post("/auth/magic", HandleMagicLink)
		r.Get("/auth/validate", HandleValidateToken)
		r.Get("/orgs", HandleOrgsList(db))
		r.Get("/git/tokens", HandleGitTokensList)
		r.Post("/git/tokens", HandleGitTokensCreate)
		r.Post("/git/tokens/{id}/revoke", HandleGitTokensRevoke)
		r.Get("/git/keys", HandleGitSSHKeysList)
		r.Post("/git/keys", HandleGitSSHKeysCreate)
		r.Post("/git/keys/{id}/revoke", HandleGitSSHKeysRevoke)
		r.Get("/user/prefixes", HandleUserCommandPrefixesList)
		r.Post("/user/prefixes", HandleUserCommandPrefixesCreate)
		r.Delete("/user/prefixes/{id}", HandleUserCommandPrefixesDelete)
		r.Post("/webhooks/openclaw", webhookHandler.OpenClawHandler)
		r.With(middleware.OptionalWorkspace).Get("/emissions/recent", emissionsHandler.Recent)
		r.With(middleware.OptionalWorkspace).Post("/emissions", emissionsHandler.Ingest)
		r.Get("/approvals/exec", execApprovalsHandler.List)
		r.Post("/approvals/exec/{id}/respond", execApprovalsHandler.Respond)
		r.With(middleware.OptionalWorkspace).Get("/inbox", HandleInbox)
		r.Get("/tasks", taskHandler.ListTasks)
		r.Post("/tasks", taskHandler.CreateTask)
		r.With(middleware.OptionalWorkspace).Get("/agents", agentsHandler.List)
		r.Get("/workflows", workflowsHandler.List)
		r.With(middleware.OptionalWorkspace).Get("/projects", projectsHandler.List)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}", projectsHandler.Get)
		r.With(middleware.OptionalWorkspace).Patch("/projects/{id}", projectsHandler.Patch)
		r.With(middleware.OptionalWorkspace).Delete("/projects/{id}", projectsHandler.Delete)
		r.With(middleware.OptionalWorkspace).Patch("/projects/{id}/settings", projectsHandler.UpdateSettings)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/chat", projectChatHandler.List)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/chat/search", projectChatHandler.Search)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/chat/messages", projectChatHandler.Create)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/chat/reset", projectChatHandler.ResetSession)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/chat/messages/{messageID}/save-to-notes", projectChatHandler.SaveToNotes)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/content/bootstrap", projectChatHandler.BootstrapContent)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/content/assets", projectChatHandler.UploadContentAsset)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/content/rename", projectChatHandler.RenameContent)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/content/delete", projectChatHandler.DeleteContent)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/content/metadata", projectChatHandler.GetContentMetadata)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/content/search", projectChatHandler.SearchContent)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/commits", projectCommitsHandler.Create)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/commits", projectCommitsHandler.List)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/commits/{sha}", projectCommitsHandler.Get)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/commits/{sha}/diff", projectCommitsHandler.Diff)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/tree", projectTreeHandler.GetTree)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/blob", projectTreeHandler.GetBlob)
		r.With(middleware.OptionalWorkspace).Get("/knowledge", knowledgeHandler.List)
		r.With(middleware.OptionalWorkspace).Post("/knowledge/import", knowledgeHandler.Import)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/pull-requests", githubPullRequestsHandler.ListByProject)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/pull-requests", githubPullRequestsHandler.CreateForProject)
		r.With(middleware.OptionalWorkspace).Get("/issues", issuesHandler.List)
		r.With(middleware.OptionalWorkspace).Get("/issues/{id}", issuesHandler.Get)
		r.With(middleware.OptionalWorkspace).Post("/issues/{id}/comments", issuesHandler.CreateComment)
		r.With(middleware.OptionalWorkspace).Post("/issues/{id}/approval-state", issuesHandler.TransitionApprovalState)
		r.With(middleware.OptionalWorkspace).Post("/issues/{id}/approve", issuesHandler.Approve)
		r.With(middleware.OptionalWorkspace).Patch("/issues/{id}", issuesHandler.PatchIssue)
		r.With(middleware.OptionalWorkspace).Post("/issues/{id}/review/save", issuesHandler.SaveReview)
		r.With(middleware.OptionalWorkspace).Post("/issues/{id}/review/address", issuesHandler.AddressReview)
		r.With(middleware.OptionalWorkspace).Get("/issues/{id}/review/changes", issuesHandler.ReviewChanges)
		r.With(middleware.OptionalWorkspace).Get("/issues/{id}/review/history", issuesHandler.ReviewHistory)
		r.With(middleware.OptionalWorkspace).Get("/issues/{id}/review/history/{sha}", issuesHandler.ReviewVersion)
		r.With(middleware.OptionalWorkspace).Post("/issues/{id}/participants", issuesHandler.AddParticipant)
		r.With(middleware.OptionalWorkspace).Delete("/issues/{id}/participants/{agentID}", issuesHandler.RemoveParticipant)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/issues", issuesHandler.CreateIssue)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/issues/link", issuesHandler.CreateLinkedIssue)
		r.With(RequireCapability(db, CapabilityGitHubManualSync)).Post("/projects/{id}/issues/import", projectIssueSyncHandler.ManualImport)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/issues/status", projectIssueSyncHandler.Status)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Get("/projects/{id}/repo/branches", githubIntegrationHandler.GetProjectBranches)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Put("/projects/{id}/repo/branches", githubIntegrationHandler.UpdateProjectBranches)
		r.With(RequireCapability(db, CapabilityGitHubManualSync)).Post("/projects/{id}/repo/conflicts/resolve", githubIntegrationHandler.ResolveProjectConflict)
		r.With(RequireCapability(db, CapabilityGitHubManualSync)).Post("/projects/{id}/repo/sync", githubIntegrationHandler.ManualRepoSync)
		r.With(RequireCapability(db, CapabilityGitHubPublish)).Post("/projects/{id}/publish", githubIntegrationHandler.PublishProject)
		r.With(middleware.OptionalWorkspace).Post("/projects", projectsHandler.Create)
		r.With(middleware.OptionalWorkspace).Get("/github/integration/status", githubIntegrationHandler.IntegrationStatus)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Get("/github/integration/repos", githubIntegrationHandler.ListRepos)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Get("/github/integration/settings", githubIntegrationHandler.ListSettings)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Delete("/github/integration/connection", githubIntegrationHandler.Disconnect)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Put("/github/integration/settings/{projectID}", githubIntegrationHandler.UpdateSettings)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Post("/github/connect/start", githubIntegrationHandler.ConnectStart)
		r.Get("/github/connect/callback", githubIntegrationHandler.ConnectCallback)
		r.Post("/github/webhook", githubIntegrationHandler.GitHubWebhook)
		r.With(RequireCapability(db, CapabilityGitHubManualSync)).Get("/github/sync/health", githubSyncHealthHandler.Get)
		r.With(RequireCapability(db, CapabilityGitHubManualSync)).Get("/github/sync/dead-letters", githubSyncDeadLettersHandler.List)
		r.With(RequireCapability(db, CapabilityGitHubManualSync)).Post("/github/sync/dead-letters/{id}/replay", githubSyncDeadLettersHandler.Replay)
		r.Post("/sync/openclaw", openclawSyncHandler.Handle)
		r.Get("/sync/openclaw/dispatch/pending", openclawSyncHandler.PullDispatchQueue)
		r.Post("/sync/openclaw/dispatch/{id}/ack", openclawSyncHandler.AckDispatchQueue)
		r.Get("/sync/agents", openclawSyncHandler.GetAgents)
		r.Patch("/tasks/{id}", taskHandler.UpdateTask)
		r.Patch("/tasks/{id}/status", taskHandler.UpdateTaskStatus)
		r.Get("/messages", messageHandler.ListMessages)
		r.Post("/messages", messageHandler.CreateMessage)
		r.Get("/messages/{id}", messageHandler.GetMessage)
		r.Put("/messages/{id}", messageHandler.UpdateMessage)
		r.Delete("/messages/{id}", messageHandler.DeleteMessage)
		r.Get("/threads/{id}/messages", messageHandler.ListThreadMessages)
		r.Post("/messages/attachments", attachmentsHandler.Upload)
		r.Get("/attachments/{id}", attachmentsHandler.GetAttachment)
		r.Get("/export", HandleExport)
		r.Post("/import", HandleImport)
		r.Post("/import/validate", HandleImportValidate)

		// Admin endpoints
		r.With(middleware.OptionalWorkspace).Post("/admin/init-repos", HandleAdminInitRepos(db))
		r.With(middleware.OptionalWorkspace).Get("/admin/connections", adminConnectionsHandler.Get)
		r.With(middleware.OptionalWorkspace).Get("/admin/events", adminConnectionsHandler.GetEvents)
		r.With(middleware.OptionalWorkspace).Post("/admin/gateway/restart", adminConnectionsHandler.RestartGateway)
		r.With(middleware.OptionalWorkspace).Post("/admin/agents/{id}/ping", adminConnectionsHandler.PingAgent)
		r.With(middleware.OptionalWorkspace).Post("/admin/agents/{id}/reset", adminConnectionsHandler.ResetAgent)
		r.With(middleware.OptionalWorkspace).Post("/admin/diagnostics", adminConnectionsHandler.RunDiagnostics)
		r.With(middleware.OptionalWorkspace).Get("/admin/logs", adminConnectionsHandler.GetLogs)
		r.With(middleware.OptionalWorkspace).Get("/admin/cron/jobs", adminConnectionsHandler.GetCronJobs)
		r.With(middleware.OptionalWorkspace).Post("/admin/cron/jobs/{id}/run", adminConnectionsHandler.RunCronJob)
		r.With(middleware.OptionalWorkspace).Patch("/admin/cron/jobs/{id}", adminConnectionsHandler.ToggleCronJob)
		r.With(middleware.OptionalWorkspace).Get("/admin/processes", adminConnectionsHandler.GetProcesses)
		r.With(middleware.OptionalWorkspace).Post("/admin/processes/{id}/kill", adminConnectionsHandler.KillProcess)
	})

	// WebSocket handlers
	r.Handle("/ws", websocketHandler)
	r.Handle("/ws/openclaw", openClawWSHandler)
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(http.Dir(getUploadsStorageDir()))))

	// Static file fallback for frontend SPA (must be last)
	r.Get("/*", handleRoot)

	return r
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:    "ok",
		Uptime:    time.Since(startTime).Round(time.Second).String(),
		Version:   getVersion(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	// Check if we should serve the frontend
	staticDir := os.Getenv("STATIC_DIR")
	if staticDir == "" {
		staticDir = "./static"
	}

	// Check if static directory exists
	if _, err := os.Stat(staticDir); err == nil {
		// Serve index.html for root and non-API paths
		serveStatic(staticDir, w, r)
		return
	}

	// Fall back to JSON response if no static files
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	sendJSON(w, http.StatusOK, map[string]string{
		"name":    "Otter Camp",
		"tagline": "Work management for AI agent teams",
		"docs":    "/docs",
		"health":  "/health",
	})
}

func serveStatic(dir string, w http.ResponseWriter, r *http.Request) {
	// Remove Content-Type: application/json header for static files
	w.Header().Del("Content-Type")

	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	// Try to serve the exact file
	filePath := dir + path
	if _, err := os.Stat(filePath); err == nil {
		http.ServeFile(w, r, filePath)
		return
	}

	// For SPA: serve index.html for all non-asset routes
	if _, err := os.Stat(dir + "/index.html"); err == nil {
		http.ServeFile(w, r, dir+"/index.html")
		return
	}

	http.NotFound(w, r)
}

func getVersion() string {
	if v := os.Getenv("VERSION"); v != "" {
		return v
	}
	return "dev"
}
