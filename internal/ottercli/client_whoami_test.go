package ottercli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientWhoAmIUsesValidateEndpointAndTokenQuery(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"org_id":"org-1","org_slug":"team-a","user":{"id":"u1","name":"Sam","email":"sam@example.com"}}`))
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "oc_sess_abc",
		HTTP:    srv.Client(),
	}

	resp, err := client.WhoAmI()
	if err != nil {
		t.Fatalf("WhoAmI() error = %v", err)
	}
	if !resp.Valid {
		t.Fatalf("expected valid response")
	}
	if resp.OrgID != "org-1" || resp.OrgSlug != "team-a" {
		t.Fatalf("unexpected org context: org_id=%q org_slug=%q", resp.OrgID, resp.OrgSlug)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %s, want GET", gotMethod)
	}
	if !strings.HasPrefix(gotPath, "/api/auth/validate?") || !strings.Contains(gotPath, "token=oc_sess_abc") {
		t.Fatalf("unexpected path/query: %s", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("expected no bearer auth header, got %q", gotAuth)
	}
}

func TestClientWhoAmINormalizesConfiguredAPIPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/validate" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"org_id":"org-1","org_slug":"team-a","user":{"id":"u1","name":"Sam","email":"sam@example.com"}}`))
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		APIBaseURL: srv.URL + "/api",
		Token:      "oc_sess_abc",
	}, "")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.HTTP = srv.Client()

	if _, err := client.WhoAmI(); err != nil {
		t.Fatalf("WhoAmI() error = %v", err)
	}
}

func TestClientWhoAmIRequiresToken(t *testing.T) {
	client := &Client{BaseURL: "https://api.otter.camp"}
	if _, err := client.WhoAmI(); err == nil || !strings.Contains(err.Error(), "missing auth token") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}
