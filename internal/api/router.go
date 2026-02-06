package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
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
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Org-ID", "X-Workspace-ID"},
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

	webhookHandler := &WebhookHandler{Hub: hub}
	feedPushHandler := NewFeedPushHandler(hub)
	execApprovalsHandler := &ExecApprovalsHandler{Hub: hub}
	taskHandler := &TaskHandler{Hub: hub}
	attachmentsHandler := &AttachmentsHandler{}
	agentsHandler := &AgentsHandler{Store: agentStore, DB: db}
	workflowsHandler := &WorkflowsHandler{DB: db}
	openclawSyncHandler := &OpenClawSyncHandler{Hub: hub, DB: db}
	githubSyncDeadLettersHandler := &GitHubSyncDeadLettersHandler{}
	githubSyncHealthHandler := &GitHubSyncHealthHandler{}
	githubPullRequestsHandler := &GitHubPullRequestsHandler{}
	githubIntegrationHandler := NewGitHubIntegrationHandler(db)
	projectChatHandler := &ProjectChatHandler{Hub: hub}

	// Initialize project store and handler
	var projectStore *store.ProjectStore
	if db != nil {
		projectStore = store.NewProjectStore(db)
		githubSyncJobStore := store.NewGitHubSyncJobStore(db)
		githubSyncDeadLettersHandler.Store = githubSyncJobStore
		githubSyncHealthHandler.Store = githubSyncJobStore
		githubPullRequestsHandler.Store = store.NewGitHubIssuePRStore(db)
		githubPullRequestsHandler.ProjectRepos = store.NewProjectRepoStore(db)
		githubIntegrationHandler.SyncJobs = githubSyncJobStore
		projectChatHandler.ChatStore = store.NewProjectChatStore(db)
	}
	projectsHandler := &ProjectsHandler{Store: projectStore, DB: db}
	projectChatHandler.ProjectStore = projectStore

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
		r.Get("/user/prefixes", HandleUserCommandPrefixesList)
		r.Post("/user/prefixes", HandleUserCommandPrefixesCreate)
		r.Delete("/user/prefixes/{id}", HandleUserCommandPrefixesDelete)
		r.Post("/webhooks/openclaw", webhookHandler.OpenClawHandler)
		r.Get("/approvals/exec", execApprovalsHandler.List)
		r.Post("/approvals/exec/{id}/respond", execApprovalsHandler.Respond)
		r.With(middleware.OptionalWorkspace).Get("/inbox", HandleInbox)
		r.Get("/tasks", taskHandler.ListTasks)
		r.Post("/tasks", taskHandler.CreateTask)
		r.With(middleware.OptionalWorkspace).Get("/agents", agentsHandler.List)
		r.Get("/workflows", workflowsHandler.List)
		r.With(middleware.OptionalWorkspace).Get("/projects", projectsHandler.List)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}", projectsHandler.Get)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/chat", projectChatHandler.List)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/chat/search", projectChatHandler.Search)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/chat/messages", projectChatHandler.Create)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/chat/messages/{messageID}/save-to-notes", projectChatHandler.SaveToNotes)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/content/bootstrap", projectChatHandler.BootstrapContent)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/content/assets", projectChatHandler.UploadContentAsset)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/content/metadata", projectChatHandler.GetContentMetadata)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/content/search", projectChatHandler.SearchContent)
		r.With(middleware.OptionalWorkspace).Get("/projects/{id}/pull-requests", githubPullRequestsHandler.ListByProject)
		r.With(middleware.OptionalWorkspace).Post("/projects/{id}/pull-requests", githubPullRequestsHandler.CreateForProject)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Get("/projects/{id}/repo/branches", githubIntegrationHandler.GetProjectBranches)
		r.With(RequireCapability(db, CapabilityGitHubIntegrationAdmin)).Put("/projects/{id}/repo/branches", githubIntegrationHandler.UpdateProjectBranches)
		r.With(RequireCapability(db, CapabilityGitHubManualSync)).Post("/projects/{id}/repo/sync", githubIntegrationHandler.ManualRepoSync)
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
		r.Get("/sync/agents", openclawSyncHandler.GetAgents)
		r.Patch("/tasks/{id}", taskHandler.UpdateTask)
		r.Patch("/tasks/{id}/status", taskHandler.UpdateTaskStatus)
		r.Post("/messages/attachments", attachmentsHandler.Upload)
		r.Get("/attachments/{id}", attachmentsHandler.GetAttachment)
		r.Get("/export", HandleExport)
		r.Post("/import", HandleImport)
		r.Post("/import/validate", HandleImportValidate)
	})

	// WebSocket handlers
	r.Handle("/ws", &ws.Handler{Hub: hub})
	r.Handle("/ws/openclaw", ws.NewOpenClawHandler(hub))

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
