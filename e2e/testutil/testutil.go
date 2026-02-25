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
	env     []string
	cleanup func()
	cmd     *exec.Cmd
	stdout  bytes.Buffer
	stderr  bytes.Buffer
}

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
	return server, baseURL
}

func (s *ServerProcess) Stop(t *testing.T) {
	t.Helper()
	if s == nil {
		return
	}
	defer func() {
		if s.cleanup != nil {
			s.cleanup()
			s.cleanup = nil
		}
	}()
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
		return
	}

	_ = s.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() {
		done <- s.cmd.Wait()
	}()

	select {
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
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
