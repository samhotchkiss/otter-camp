package main

import (
	"log"
	"net/http"

	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/config"
)

func main() {
	cfg := config.Load()
	port := cfg.Port
	if port == "" {
		port = "8080"
	}

	router := api.NewRouter()

	log.Printf("🦦 Otter Camp starting on port %s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
// Deploy trigger: 1770312576
