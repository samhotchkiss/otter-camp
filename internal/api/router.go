package api

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
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

	r.Use(middleware.SetHeader("Content-Type", "application/json"))

	r.Get("/health", handleHealth)
	r.Get("/", handleRoot)
	r.Post("/api/waitlist", HandleWaitlist)
	r.Get("/api/search", SearchHandler)
	r.Handle("/ws", &ws.Handler{Hub: hub})

	taskHandler := &TaskHandler{Hub: hub}
	r.Get("/api/tasks", taskHandler.ListTasks)
	r.Post("/api/tasks", taskHandler.CreateTask)
	r.Patch("/api/tasks/{id}", taskHandler.UpdateTask)
	r.Patch("/api/tasks/{id}/status", taskHandler.UpdateTaskStatus)

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

func getVersion() string {
	if v := os.Getenv("VERSION"); v != "" {
		return v
	}
	return "dev"
}
