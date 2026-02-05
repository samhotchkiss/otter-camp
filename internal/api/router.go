package api

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
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

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
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
	agentsHandler := &AgentsHandler{}
	
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
		r.Get("/tasks", taskHandler.ListTasks)
		r.Post("/tasks", taskHandler.CreateTask)
		r.Get("/agents", agentsHandler.List)
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

	_ = json.NewEncoder(w).Encode(map[string]string{
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
