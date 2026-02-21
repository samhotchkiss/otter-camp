package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
	"github.com/stretchr/testify/require"
)

func TestMigrateFromOpenClawCallsCronImportEndpoint(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var mu sync.Mutex
	var gotMethod string
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod = r.Method
		gotPath = r.URL.String()
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/import/openclaw-cron" {
			_, _ = w.Write([]byte(`{"total":3,"imported":2,"updated":1,"skipped":0}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	require.NoError(t, ottercli.SaveConfig(ottercli.Config{
		APIBaseURL: srv.URL,
		Token:      "token-1",
		DefaultOrg: "org-1",
	}))

	handleMigrate([]string{"from-openclaw", "cron", "--json"})

	mu.Lock()
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/v1/jobs/import/openclaw-cron", gotPath)
	mu.Unlock()
}

func TestMigrateFromOpenClawCronSummaryOutput(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs/import/openclaw-cron" {
			_, _ = w.Write([]byte(`{"total":4,"imported":1,"updated":2,"skipped":1,"warnings":["skip missing-agent: agent not found"]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	require.NoError(t, ottercli.SaveConfig(ottercli.Config{
		APIBaseURL: srv.URL,
		Token:      "token-1",
		DefaultOrg: "org-1",
	}))

	output := captureStdout(t, func() {
		handleMigrate([]string{"from-openclaw", "cron"})
	})
	require.Contains(t, output, "OpenClaw cron import complete")
	require.True(t, strings.Contains(output, "imported=1") && strings.Contains(output, "updated=2") && strings.Contains(output, "skipped=1"))
	require.Contains(t, output, "warning: skip missing-agent: agent not found")
}

func captureStdout(t *testing.T, run func()) string {
	t.Helper()

	original := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	defer func() {
		os.Stdout = original
	}()

	run()
	_ = w.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	_ = r.Close()
	return buf.String()
}
