package native

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
)

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name         string
		html         string
		maxLen       int
		wantTitle    string
		wantContains string
		wantTrunc    bool
	}{
		{
			name:         "basic",
			html:         `<html><head><title>Hello</title></head><body><p>World</p></body></html>`,
			maxLen:       50000,
			wantTitle:    "Hello",
			wantContains: "World",
		},
		{
			name:         "strips scripts",
			html:         `<p>Before</p><script>alert('xss')</script><p>After</p>`,
			maxLen:       50000,
			wantContains: "Before",
		},
		{
			name:         "strips styles",
			html:         `<style>body{color:red}</style><p>Content</p>`,
			maxLen:       50000,
			wantContains: "Content",
		},
		{
			name:      "truncation",
			html:      `<p>This is a fairly long paragraph that should be truncated at some point.</p>`,
			maxLen:    10,
			wantTrunc: true,
		},
		{
			name:         "nested elements",
			html:         `<div><h1>Header</h1><p>Paragraph with <strong>bold</strong> text.</p></div>`,
			maxLen:       50000,
			wantContains: "Header",
		},
		{
			name:         "empty body",
			html:         ``,
			maxLen:       50000,
			wantTitle:    "",
			wantContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, content, truncated := htmlToText([]byte(tt.html), tt.maxLen, false)
			if tt.wantTitle != "" && title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			if tt.wantContains != "" && len(content) > 0 {
				if !containsStr(content, tt.wantContains) {
					t.Errorf("content %q does not contain %q", content, tt.wantContains)
				}
			}
			if tt.wantTrunc && !truncated {
				t.Error("expected truncated = true")
			}
			// Script content should never appear.
			if containsStr(content, "alert") {
				t.Error("script content should be stripped")
			}
			if containsStr(content, "color:red") {
				t.Error("style content should be stripped")
			}
		})
	}
}

func TestValidateFetchURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://example.com", false},
		{"http://example.com/path?q=1", false},
		{"ftp://example.com", true},
		{"file:///etc/passwd", true},
		{"https://localhost/foo", true},
		{"https://127.0.0.1/foo", true},
		{"https://10.0.0.1/foo", true},
		{"https://192.168.1.1", true},
		{"https://[::1]/foo", true},
		{"https://0.0.0.0/foo", true},
		{"https://169.254.1.1/foo", true}, // link-local
		{"not-a-url", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := validateFetchURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateFetchURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestParseBraveResponse(t *testing.T) {
	resp := map[string]any{
		"web": map[string]any{
			"results": []any{
				map[string]any{
					"title":       "Result 1",
					"url":         "https://example.com/1",
					"description": "First result snippet",
				},
				map[string]any{
					"title":       "Result 2",
					"url":         "https://example.com/2",
					"description": "Second result snippet",
				},
			},
		},
	}
	body, _ := json.Marshal(resp)

	results, err := parseBraveResponse(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0]["title"] != "Result 1" {
		t.Errorf("title = %q, want %q", results[0]["title"], "Result 1")
	}
	if results[0]["url"] != "https://example.com/1" {
		t.Errorf("url = %q, want %q", results[0]["url"], "https://example.com/1")
	}
	if results[0]["snippet"] != "First result snippet" {
		t.Errorf("snippet = %q, want %q", results[0]["snippet"], "First result snippet")
	}
}

func TestParseBraveResponseMaxResults(t *testing.T) {
	resp := map[string]any{
		"web": map[string]any{
			"results": []any{
				map[string]any{"title": "A", "url": "https://a.com", "description": "a"},
				map[string]any{"title": "B", "url": "https://b.com", "description": "b"},
				map[string]any{"title": "C", "url": "https://c.com", "description": "c"},
			},
		},
	}
	body, _ := json.Marshal(resp)

	results, err := parseBraveResponse(body, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (capped), got %d", len(results))
	}
}

func TestParseBraveResponseEmpty(t *testing.T) {
	body := []byte(`{"query":{"original":"test"}}`)
	results, err := parseBraveResponse(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestParseDuckDuckGoHTML(t *testing.T) {
	html := `<html><body>
		<div class="result">
			<a class="result__a" href="https://example.com/1">Result Title 1</a>
			<a class="result__snippet">This is snippet one.</a>
		</div>
		<div class="result">
			<a class="result__a" href="https://example.com/2">Result Title 2</a>
			<a class="result__snippet">Snippet two here.</a>
		</div>
	</body></html>`

	results, err := parseDuckDuckGoHTML([]byte(html), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0]["title"] != "Result Title 1" {
		t.Errorf("title = %q, want %q", results[0]["title"], "Result Title 1")
	}
	if results[0]["url"] != "https://example.com/1" {
		t.Errorf("url = %q, want %q", results[0]["url"], "https://example.com/1")
	}
	if results[0]["snippet"] != "This is snippet one." {
		t.Errorf("snippet = %q, want %q", results[0]["snippet"], "This is snippet one.")
	}
}

func TestParseDuckDuckGoHTMLMaxResults(t *testing.T) {
	html := `<html><body>
		<a class="result__a" href="https://a.com">A</a>
		<a class="result__a" href="https://b.com">B</a>
		<a class="result__a" href="https://c.com">C</a>
	</body></html>`

	results, err := parseDuckDuckGoHTML([]byte(html), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (capped), got %d", len(results))
	}
}

// stubSecretResolver implements secretResolver for testing.
type stubSecretResolver struct {
	secrets map[string]string
}

func (s *stubSecretResolver) Get(_ context.Context, _ uuid.UUID, slug string) (string, error) {
	v, ok := s.secrets[slug]
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return v, nil
}

func TestHandleWebSearchWithBraveKey(t *testing.T) {
	// Set up a mock Brave API server.
	braveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		resp := map[string]any{
			"web": map[string]any{
				"results": []any{
					map[string]any{
						"title":       "Mock Result",
						"url":         "https://example.com",
						"description": "Mock snippet",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer braveServer.Close()

	// Override the brave endpoint for testing.
	origEndpoint := braveSearchEndpoint
	defer func() { setBraveEndpoint(origEndpoint) }()
	setBraveEndpoint(braveServer.URL)

	orgID := uuid.New()
	executor := &NativeToolExecutor{
		secrets: &stubSecretResolver{secrets: map[string]string{"brave-search-api-key": "test-key"}},
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
	})

	result, err := executor.handleWebSearch(ctx, map[string]any{"query": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if result["source"] != "brave" {
		t.Errorf("source = %q, want brave", result["source"])
	}
	results, ok := result["results"].([]map[string]any)
	if !ok || len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0]["title"] != "Mock Result" {
		t.Errorf("title = %q, want Mock Result", results[0]["title"])
	}
}

func TestHandleWebSearchFallbackDDG(t *testing.T) {
	// Set up a mock DDG server.
	ddgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body>
			<a class="result__a" href="https://ddg.example.com">DDG Result</a>
			<a class="result__snippet">DDG snippet text</a>
		</body></html>`)
	}))
	defer ddgServer.Close()

	origEndpoint := duckDuckGoHTMLEndpoint
	defer func() { setDDGEndpoint(origEndpoint) }()
	setDDGEndpoint(ddgServer.URL)

	orgID := uuid.New()
	executor := &NativeToolExecutor{
		secrets: &stubSecretResolver{secrets: map[string]string{}}, // No brave key
	}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
	})

	result, err := executor.handleWebSearch(ctx, map[string]any{"query": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if result["source"] != "duckduckgo" {
		t.Errorf("source = %q, want duckduckgo", result["source"])
	}
}

func TestHandleWebFetchLocalServerBlocked(t *testing.T) {
	// httptest servers run on 127.0.0.1 — SSRF validation correctly blocks these.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><head><title>Test</title></head><body><p>Should not reach here</p></body></html>`)
	}))
	defer ts.Close()

	orgID := uuid.New()
	executor := &NativeToolExecutor{}
	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
	})

	result, err := executor.handleWebFetch(ctx, map[string]any{"url": ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	errMsg, _ := result["error"].(string)
	if errMsg == "" {
		t.Error("expected SSRF error for httptest local server")
	}
}

func TestWebFetchHTMLConversion(t *testing.T) {
	// Test the content extraction pipeline directly since SSRF blocks local httptest servers.
	body := []byte(`<html><head><title>Test Page</title></head><body><p>Hello from test server!</p></body></html>`)
	title, content, truncated := htmlToText(body, 50000, false)
	if title != "Test Page" {
		t.Errorf("title = %q, want Test Page", title)
	}
	if !containsStr(content, "Hello from test server") {
		t.Errorf("content %q does not contain expected text", content)
	}
	if truncated {
		t.Error("expected truncated = false")
	}
}

func TestHandleWebFetchSSRF(t *testing.T) {
	orgID := uuid.New()
	executor := &NativeToolExecutor{}

	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
	})

	result, err := executor.handleWebFetch(ctx, map[string]any{"url": "http://127.0.0.1:8080/secret"})
	if err != nil {
		t.Fatal(err)
	}
	errMsg, _ := result["error"].(string)
	if errMsg == "" {
		t.Error("expected SSRF error for localhost URL")
	}
}

func TestHandleWebSearchMissingQuery(t *testing.T) {
	executor := &NativeToolExecutor{}
	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: uuid.New(),
	})
	result, err := executor.handleWebSearch(ctx, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result["error"] != "query is required" {
		t.Errorf("error = %q, want 'query is required'", result["error"])
	}
}

func TestHandleWebFetchMissingURL(t *testing.T) {
	executor := &NativeToolExecutor{}
	ctx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: uuid.New(),
	})
	result, err := executor.handleWebFetch(ctx, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result["error"] != "url is required" {
		t.Errorf("error = %q, want 'url is required'", result["error"])
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findStr(s, substr))
}

func findStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
