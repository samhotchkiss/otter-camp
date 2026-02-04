package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
