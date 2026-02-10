package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/api"
)

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	router := api.NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("expected content-type application/json, got %q", ct)
	}
}

func TestHealthEndpointFields(t *testing.T) {
	t.Parallel()

	router := api.NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	for _, field := range []string{"status", "uptime", "version", "timestamp"} {
		if payload[field] == "" {
			t.Fatalf("expected %s to be set, got empty", field)
		}
	}

	if _, err := time.Parse(time.RFC3339, payload["timestamp"]); err != nil {
		t.Fatalf("expected timestamp to be RFC3339, got %q", payload["timestamp"])
	}
}

func TestRootEndpoint(t *testing.T) {
	t.Parallel()

	router := api.NewRouter()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if payload["name"] == "" || payload["tagline"] == "" {
		t.Fatalf("expected name and tagline to be set, got %#v", payload)
	}
	if payload["docs"] == "" || payload["health"] == "" {
		t.Fatalf("expected docs and health to be set, got %#v", payload)
	}
}

func TestLocalRuntimeDefaults(t *testing.T) {
	t.Parallel()

	composeBytes, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatalf("failed to read docker-compose.yml: %v", err)
	}
	compose := string(composeBytes)
	for _, snippet := range []string{
		"image: pgvector/pgvector:pg16",
		"\"4200:4200\"",
		"PORT: 4200",
		"VITE_API_URL: \"\"",
	} {
		if !strings.Contains(compose, snippet) {
			t.Fatalf("expected docker-compose.yml to contain %q", snippet)
		}
	}

	envBytes, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("failed to read .env.example: %v", err)
	}
	env := string(envBytes)
	if !strings.Contains(env, "PORT=4200") {
		t.Fatalf("expected .env.example to set PORT=4200")
	}

	setupBytes, err := os.ReadFile("../../scripts/setup.sh")
	if err != nil {
		t.Fatalf("failed to read scripts/setup.sh: %v", err)
	}
	setup := string(setupBytes)
	for _, snippet := range []string{
		"PORT=4200",
		"OTTERCAMP_URL=http://localhost:4200",
		"Dashboard: http://localhost:4200",
	} {
		if !strings.Contains(setup, snippet) {
			t.Fatalf("expected scripts/setup.sh to contain %q", snippet)
		}
	}
}
