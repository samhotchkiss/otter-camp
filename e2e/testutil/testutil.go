//go:build e2e

package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultAdminEmail    = "admin@localhost"
	defaultAdminPassword = "test-bootstrap-password"
)

type ServerProcess struct {
	baseURL string
	rootDir string
	binary  string
	dbURL   string
	env     []string
	cleanup func()
	cmd     *exec.Cmd
	worker  *exec.Cmd
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	wstdout bytes.Buffer
	wstderr bytes.Buffer
}

var (
	baseURLDBMu sync.RWMutex
	baseURLDB   = map[string]string{}
)

func StartServer(t *testing.T) (*ServerProcess, string) {
	t.Helper()

	rootDir := findRepoRoot(t)
	binary := findBinary(t, rootDir)

	port := freePort(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	envMap, cleanup := e2eEnv(t)
	envMap["OTTERCAMP_MODE"] = "test"
	envMap["OTTERCAMP_AUTH_MODE"] = "standard"
	envMap["OTTERCAMP_ADDR"] = fmt.Sprintf("127.0.0.1:%d", port)
	envMap["OTTERCAMP_STORAGE_ROOT"] = filepath.Join(t.TempDir(), "objects")
	envMap["OTTERCAMP_ADMIN_EMAIL"] = defaultEnv("OTTERCAMP_E2E_ADMIN_EMAIL", defaultAdminEmail)
	envMap["OTTERCAMP_ADMIN_PASSWORD"] = defaultEnv("OTTERCAMP_E2E_ADMIN_PASSWORD", defaultAdminPassword)

	server := &ServerProcess{
		baseURL: baseURL,
		rootDir: rootDir,
		binary:  binary,
		dbURL:   strings.TrimSpace(envMap["OTTERCAMP_DATABASE_URL"]),
		env:     mapToEnv(envMap),
		cleanup: cleanup,
	}

	cmd := exec.Command(binary, "serve", "--port", strconv.Itoa(port))
	cmd.Dir = rootDir
	cmd.Env = server.env
	cmd.Stdout = &server.stdout
	cmd.Stderr = &server.stderr
	server.cmd = cmd

	if err := cmd.Start(); err != nil {
		if server.cleanup != nil {
			server.cleanup()
		}
		t.Fatalf("start server: %v", err)
	}

	if err := waitForHealthLive(baseURL, 30*time.Second, 250*time.Millisecond); err != nil {
		server.Stop(t)
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, server.stdout.String(), server.stderr.String())
	}
	baseURLDBMu.Lock()
	baseURLDB[baseURL] = server.dbURL
	baseURLDBMu.Unlock()
	return server, baseURL
}

func StartServerWithWorker(t *testing.T) (*ServerProcess, string) {
	t.Helper()
	server, baseURL := StartServer(t)
	server.StartWorker(t)
	return server, baseURL
}

func (s *ServerProcess) StartWorker(t *testing.T) {
	t.Helper()
	if s == nil {
		t.Fatal("server process is nil")
	}
	if s.worker != nil {
		return
	}

	cmd := exec.Command(s.binary, "worker")
	cmd.Dir = s.rootDir
	cmd.Env = s.env
	cmd.Stdout = &s.wstdout
	cmd.Stderr = &s.wstderr
	s.worker = cmd

	if err := cmd.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("worker exited early\nstdout:\n%s\nstderr:\n%s", s.wstdout.String(), s.wstderr.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *ServerProcess) Stop(t *testing.T) {
	t.Helper()
	if s == nil {
		return
	}
	baseURLDBMu.Lock()
	delete(baseURLDB, s.baseURL)
	baseURLDBMu.Unlock()

	defer func() {
		if s.cleanup != nil {
			s.cleanup()
			s.cleanup = nil
		}
	}()

	stopCommand(s.worker)
	stopCommand(s.cmd)
}

func stopCommand(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return
	}

	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	case <-done:
	}
}

func (s *ServerProcess) RunCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	if s == nil {
		t.Fatal("server process is nil")
	}
	cmd := exec.Command(s.binary, args...)
	cmd.Dir = s.rootDir
	cmd.Env = s.env

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), 0
	}

	exitCode := 1
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	}
	t.Logf("cli stderr: %s", stderr.String())
	return stdout.String(), exitCode
}

func ResetState(t *testing.T, baseURL string) {
	t.Helper()
	_, status := POST(t, baseURL, "/test/reset", "", map[string]any{})
	if status != http.StatusNoContent {
		t.Fatalf("POST /test/reset status=%d want=%d", status, http.StatusNoContent)
	}
}

func AdminToken(t *testing.T, baseURL string) string {
	t.Helper()
	body, status := POST(t, baseURL, "/v1/auth/login", "", map[string]any{
		"email":    defaultEnv("OTTERCAMP_E2E_ADMIN_EMAIL", defaultAdminEmail),
		"password": defaultEnv("OTTERCAMP_E2E_ADMIN_PASSWORD", defaultAdminPassword),
	})
	if status != http.StatusOK {
		t.Fatalf("POST /v1/auth/login status=%d want=%d body=%s", status, http.StatusOK, string(body))
	}

	token := stringPath(t, body, "data", "token")
	if strings.TrimSpace(token) == "" {
		t.Fatalf("missing data.token body=%s", string(body))
	}
	return token
}

func GET(t *testing.T, baseURL, path, token string) ([]byte, int) {
	t.Helper()
	url := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new GET request: %v", err)
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s body: %v", path, err)
	}
	return body, resp.StatusCode
}

func POST(t *testing.T, baseURL, path, token string, body any) ([]byte, int) {
	t.Helper()
	url := strings.TrimRight(baseURL, "/") + path

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal POST %s body: %v", path, err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(http.MethodPost, url, reader)
	if err != nil {
		t.Fatalf("new POST request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST %s body: %v", path, err)
	}
	return rawBody, resp.StatusCode
}

func DELETE(t *testing.T, baseURL, path, token string) ([]byte, int) {
	t.Helper()
	url := strings.TrimRight(baseURL, "/") + path

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("new DELETE request: %v", err)
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("DELETE %s failed: %v", path, err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read DELETE %s body: %v", path, err)
	}
	return rawBody, resp.StatusCode
}

func JSONPath(t *testing.T, body []byte, path ...string) any {
	t.Helper()
	var current any
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatalf("unmarshal body: %v body=%s", err, string(body))
	}
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[segment]
			if !ok {
				t.Fatalf("missing json path %v at %q body=%s", path, segment, string(body))
			}
			current = next
		case []any:
			index, err := parseIndex(segment, len(typed))
			if err != nil {
				t.Fatalf("invalid array index for path %v at %q: %v body=%s", path, segment, err, string(body))
			}
			current = typed[index]
		default:
			t.Fatalf("invalid json path %v at %q with type %T body=%s", path, segment, current, string(body))
		}
	}
	return current
}

func stringPath(t *testing.T, body []byte, path ...string) string {
	t.Helper()
	value := JSONPath(t, body, path...)
	str, _ := value.(string)
	return str
}

type MemoryFilter struct {
	ScopeType    string
	ContainsText string
	MemoryType   string
	ProjectID    string
}

func WaitForMemory(t *testing.T, baseURL, token string, filter MemoryFilter, timeout time.Duration) map[string]any {
	t.Helper()

	scope := strings.TrimSpace(strings.ToLower(filter.ScopeType))
	switch scope {
	case "organization":
		scope = "org"
	case "project":
		scope = "project"
	case "task":
		scope = "task"
	}

	deadline := time.Now().Add(timeout)
	lastStatus := 0
	lastBody := []byte("{}")
	for time.Now().Before(deadline) {
		values := url.Values{}
		values.Set("status", "active")
		if scope != "" {
			values.Set("scope", scope)
		}
		if strings.TrimSpace(filter.MemoryType) != "" {
			values.Set("memory_type", strings.TrimSpace(filter.MemoryType))
		}
		if strings.TrimSpace(filter.ProjectID) != "" {
			values.Set("project_id", strings.TrimSpace(filter.ProjectID))
		}
		if strings.TrimSpace(filter.ContainsText) != "" {
			values.Set("search", strings.TrimSpace(filter.ContainsText))
		}

		path := "/v1/memory/items?" + values.Encode()
		body, status := GET(t, baseURL, path, token)
		lastStatus = status
		lastBody = body
		if status == http.StatusOK {
			rawItems, ok := JSONPath(t, body, "data").([]any)
			if ok {
				for _, raw := range rawItems {
					item, itemOK := raw.(map[string]any)
					if !itemOK {
						continue
					}
					if strings.TrimSpace(filter.ContainsText) != "" {
						content, _ := item["content"].(string)
						if !strings.Contains(strings.ToLower(content), strings.ToLower(strings.TrimSpace(filter.ContainsText))) {
							continue
						}
					}
					return item
				}
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for memory filter=%+v last_status=%d last_body=%s", filter, lastStatus, string(lastBody))
	return nil
}

func TriggerExtractionJob(t *testing.T, baseURL, token, sessionID string) {
	t.Helper()
	body, status := POST(t, baseURL, "/v1/memory/consolidate", token, map[string]any{
		"run_type":   "extraction",
		"session_id": strings.TrimSpace(sessionID),
	})
	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusAccepted {
		t.Fatalf("POST /v1/memory/consolidate extraction status=%d body=%s", status, string(body))
	}
}

func TriggerCompactionRun(t *testing.T, baseURL, token, orgID string) {
	t.Helper()
	_ = orgID
	body, status := POST(t, baseURL, "/v1/memory/consolidate", token, map[string]any{
		"type":     "compaction",
		"run_type": "compaction",
	})
	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusAccepted {
		t.Fatalf("POST /v1/memory/consolidate compaction status=%d body=%s", status, string(body))
	}
	if status != http.StatusAccepted {
		return
	}

	runID := stringPath(t, body, "data", "compaction_run_id")
	if strings.TrimSpace(runID) == "" {
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		runsBody, runsStatus := GET(t, baseURL, "/v1/memory/compaction-runs?limit=50", token)
		if runsStatus == http.StatusOK {
			items, ok := JSONPath(t, runsBody, "data").([]any)
			if ok {
				for _, raw := range items {
					item, itemOK := raw.(map[string]any)
					if !itemOK {
						continue
					}
					id, _ := item["id"].(string)
					if id != runID {
						continue
					}
					itemStatus, _ := item["status"].(string)
					if strings.EqualFold(strings.TrimSpace(itemStatus), "completed") {
						return
					}
				}
			}
		}
		time.Sleep(800 * time.Millisecond)
	}
}

func WaitForAssistantMessage(t *testing.T, baseURL, token, sessionID string, afterSequence int64, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	lastStatus := 0
	lastBody := []byte("{}")

	path := "/v1/chat-sessions/" + strings.TrimSpace(sessionID) + "/messages?limit=200"
	for time.Now().Before(deadline) {
		body, status := GET(t, baseURL, path, token)
		lastStatus = status
		lastBody = body
		if status == http.StatusTooManyRequests {
			time.Sleep(2 * time.Second)
			continue
		}
		if status == http.StatusOK {
			items, ok := JSONPath(t, body, "data").([]any)
			if ok {
				for i := len(items) - 1; i >= 0; i-- {
					item, itemOK := items[i].(map[string]any)
					if !itemOK {
						continue
					}
					role, _ := item["role"].(string)
					statusText, _ := item["status"].(string)
					if !strings.EqualFold(strings.TrimSpace(statusText), "final") &&
						!strings.EqualFold(strings.TrimSpace(statusText), "failed") {
						continue
					}
					sequenceRaw, hasSequence := item["sequence_number"].(float64)
					if !hasSequence {
						continue
					}
					if !strings.EqualFold(strings.TrimSpace(role), "assistant") &&
						!strings.EqualFold(strings.TrimSpace(role), "system") {
						continue
					}
					if int64(sequenceRaw) > afterSequence {
						return item
					}
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("timed out waiting for assistant response session=%s after_sequence=%d last_status=%d last_body=%s", sessionID, afterSequence, lastStatus, string(lastBody))
	return nil
}

func waitForHealthLive(baseURL string, timeout, pollInterval time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/health/live")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("server did not become healthy within %s", timeout)
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	current := wd
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		next := filepath.Dir(current)
		if next == current {
			t.Fatalf("repo root not found from %s", wd)
		}
		current = next
	}
}

func findBinary(t *testing.T, root string) string {
	t.Helper()
	if explicit := strings.TrimSpace(os.Getenv("OTTERCAMP_E2E_BINARY")); explicit != "" {
		return explicit
	}

	candidates := []string{
		filepath.Join(root, "ottercamp"),
		filepath.Join(root, "bin", "ottercamp"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}

	t.Fatalf("ottercamp binary not found; build first with `make build` or set OTTERCAMP_E2E_BINARY")
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate port: %v", err)
	}
	defer listener.Close()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener addr type: %T", listener.Addr())
	}
	return addr.Port
}

func e2eEnv(t *testing.T) (map[string]string, func()) {
	t.Helper()
	values := map[string]string{}
	for _, item := range os.Environ() {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		values[parts[0]] = parts[1]
	}

	if strings.TrimSpace(values["OTTERCAMP_DATABASE_URL"]) != "" {
		return values, nil
	}

	dbURL, cleanup := provisionTestDatabase(t)
	values["OTTERCAMP_DATABASE_URL"] = dbURL
	return values, cleanup
}

func mapToEnv(values map[string]string) []string {
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func defaultEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseIndex(raw string, length int) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	if value < 0 || value >= length {
		return 0, fmt.Errorf("index %d out of range", value)
	}
	return value, nil
}

func provisionTestDatabase(t *testing.T) (string, func()) {
	t.Helper()
	ctx := context.Background()

	templateURL := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_DATABASE_URL"))
	if templateURL == "" {
		templateURL = "postgres://localhost/ottercamp_test_template"
	}

	templateName, err := dbNameFromURL(templateURL)
	if err != nil {
		t.Fatalf("parse OTTERCAMP_TEST_DATABASE_URL: %v", err)
	}

	adminURL, err := withDBName(templateURL, "postgres")
	if err != nil {
		t.Fatalf("build admin URL: %v", err)
	}
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin DB: %v", err)
	}

	testDBName := fmt.Sprintf("ottercamp_e2e_%d_%s", time.Now().UnixNano(), strings.ReplaceAll(uuid.NewString(), "-", ""))
	createSQL := fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", pgx.Identifier{testDBName}.Sanitize(), pgx.Identifier{templateName}.Sanitize())
	if _, err := adminPool.Exec(ctx, createSQL); err != nil {
		adminPool.Close()
		t.Fatalf("create e2e database from template: %v", err)
	}

	testURL, err := withDBName(templateURL, testDBName)
	if err != nil {
		adminPool.Close()
		t.Fatalf("build e2e database URL: %v", err)
	}

	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = adminPool.Exec(cleanupCtx, `
			SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity
			WHERE datname = $1 AND pid <> pg_backend_pid()
		`, testDBName)
		dropSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s", pgx.Identifier{testDBName}.Sanitize())
		_, _ = adminPool.Exec(cleanupCtx, dropSQL)
		adminPool.Close()
	}
	return testURL, cleanup
}

func dbNameFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("database name missing in %q", rawURL)
	}
	return name, nil
}

func withDBName(rawURL, dbName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	return u.String(), nil
}
