package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

func TestParsePositiveInt(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "valid", input: "42", want: 42},
		{name: "trimmed", input: " 7 ", want: 7},
		{name: "zero", input: "0", wantErr: true},
		{name: "negative-like", input: "-1", wantErr: true},
		{name: "alpha", input: "abc", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, tt := range tests {
		got, err := parsePositiveInt(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("%s: expected error, got none", tt.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.name, err)
		}
		if got != tt.want {
			t.Fatalf("%s: got %d want %d", tt.name, got, tt.want)
		}
	}
}

func TestResolveIssueIDUUIDBypassesLookup(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	got, err := resolveIssueID(nil, "", id)
	if err != nil {
		t.Fatalf("resolveIssueID returned error for UUID input: %v", err)
	}
	if got != id {
		t.Fatalf("resolveIssueID got %q want %q", got, id)
	}
}

func TestFriendlyAuthErrorMessage(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		parts []string
	}{
		{
			name: "missing token",
			err:  errors.New("missing auth token"),
			parts: []string{
				"No auth config found.",
				"otter auth login --token <your-token> --org <org-id>",
				"https://otter.camp/settings",
				"API Tokens",
			},
		},
		{
			name: "missing org",
			err:  errors.New("missing org id; pass --org or set defaultOrg in config"),
			parts: []string{
				"No auth config found.",
				"otter auth login --token <your-token> --org <org-id>",
				"https://otter.camp/settings",
				"API Tokens",
			},
		},
	}

	for _, tt := range tests {
		got := formatCLIError(tt.err)
		for _, part := range tt.parts {
			if !strings.Contains(got, part) {
				t.Fatalf("%s: expected %q in message %q", tt.name, part, got)
			}
		}
	}
}

func TestFriendlyAuthErrorMessageFallback(t *testing.T) {
	err := errors.New("request failed (500): boom")
	if got := formatCLIError(err); got != err.Error() {
		t.Fatalf("formatCLIError() = %q, want %q", got, err.Error())
	}
}

func TestDeriveManagedRepoURL(t *testing.T) {
	tests := []struct {
		name      string
		apiBase   string
		orgID     string
		projectID string
		want      string
	}{
		{
			name:      "plain host",
			apiBase:   "https://api.otter.camp",
			orgID:     "org-123",
			projectID: "project-456",
			want:      "https://api.otter.camp/git/org-123/project-456.git",
		},
		{
			name:      "host with trailing slash",
			apiBase:   "https://api.otter.camp/",
			orgID:     "org-123",
			projectID: "project-456",
			want:      "https://api.otter.camp/git/org-123/project-456.git",
		},
		{
			name:      "host with api path suffix",
			apiBase:   "https://api.otter.camp/api/v1",
			orgID:     "org-123",
			projectID: "project-456",
			want:      "https://api.otter.camp/git/org-123/project-456.git",
		},
		{
			name:      "localhost with port",
			apiBase:   "http://localhost:8080/api",
			orgID:     "org-123",
			projectID: "project-456",
			want:      "http://localhost:8080/git/org-123/project-456.git",
		},
		{
			name:      "empty org",
			apiBase:   "https://api.otter.camp",
			orgID:     "",
			projectID: "project-456",
			want:      "",
		},
		{
			name:      "invalid base url",
			apiBase:   "://bad",
			orgID:     "org-123",
			projectID: "project-456",
			want:      "",
		},
	}

	for _, tt := range tests {
		got := deriveManagedRepoURL(tt.apiBase, tt.orgID, tt.projectID)
		if got != tt.want {
			t.Fatalf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}

func TestProjectCreateSplitArgsSupportsInterspersedFlags(t *testing.T) {
	tests := []struct {
		name      string
		input     []string
		wantFlags []string
		wantNames []string
		wantErr   bool
	}{
		{
			name:      "flags before name",
			input:     []string{"--description", "Description here", "Agent Avatars"},
			wantFlags: []string{"--description", "Description here"},
			wantNames: []string{"Agent Avatars"},
		},
		{
			name:      "flags after name",
			input:     []string{"Agent Avatars", "--description", "Description here"},
			wantFlags: []string{"--description", "Description here"},
			wantNames: []string{"Agent Avatars"},
		},
		{
			name:      "mixed with bool and equals flag",
			input:     []string{"Agent Avatars", "--json", "--repo-url=https://example.com/repo.git"},
			wantFlags: []string{"--json", "--repo-url=https://example.com/repo.git"},
			wantNames: []string{"Agent Avatars"},
		},
		{
			name:      "double dash keeps remaining as name",
			input:     []string{"--description", "Desc", "--", "Agent", "--description"},
			wantFlags: []string{"--description", "Desc"},
			wantNames: []string{"Agent", "--description"},
		},
		{
			name:    "missing flag value",
			input:   []string{"Agent Avatars", "--description"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		gotFlags, gotNames, err := splitProjectCreateArgs(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("%s: expected error, got none", tt.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.name, err)
		}
		if strings.Join(gotFlags, "|") != strings.Join(tt.wantFlags, "|") {
			t.Fatalf("%s: flags got %v want %v", tt.name, gotFlags, tt.wantFlags)
		}
		if strings.Join(gotNames, "|") != strings.Join(tt.wantNames, "|") {
			t.Fatalf("%s: names got %v want %v", tt.name, gotNames, tt.wantNames)
		}
	}
}

func TestResolveIssueCommentAuthorRef(t *testing.T) {
	tests := []struct {
		name            string
		explicitAuthor  string
		envAuthor       string
		whoamiName      string
		whoamiStatus    int
		wantAgentID     string
		wantErrContains string
		wantWhoamiCalls int
	}{
		{
			name:            "explicit author takes precedence",
			explicitAuthor:  "stone",
			envAuthor:       "ivy",
			whoamiName:      "Sam User",
			whoamiStatus:    http.StatusOK,
			wantAgentID:     "agent-stone",
			wantWhoamiCalls: 0,
		},
		{
			name:            "env author used when explicit missing",
			envAuthor:       "ivy",
			whoamiName:      "Sam User",
			whoamiStatus:    http.StatusOK,
			wantAgentID:     "agent-ivy",
			wantWhoamiCalls: 0,
		},
		{
			name:            "whoami fallback when explicit and env missing",
			whoamiName:      "Sam User",
			whoamiStatus:    http.StatusOK,
			wantAgentID:     "agent-sam",
			wantWhoamiCalls: 1,
		},
		{
			name:            "missing all author sources returns clear error",
			whoamiName:      "",
			whoamiStatus:    http.StatusOK,
			wantErrContains: "comment requires --author or OTTER_AGENT_ID",
			wantWhoamiCalls: 1,
		},
		{
			name:            "whoami request failure still returns clear error",
			whoamiStatus:    http.StatusInternalServerError,
			wantErrContains: "comment requires --author or OTTER_AGENT_ID",
			wantWhoamiCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			whoamiCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/agents":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"agents":[{"id":"agent-stone","name":"Stone","slug":"stone"},{"id":"agent-ivy","name":"Ivy","slug":"ivy"},{"id":"agent-sam","name":"Sam User","slug":"sam-user"}]}`))
				case "/api/auth/validate":
					whoamiCalls++
					if tt.whoamiStatus >= 400 {
						w.WriteHeader(tt.whoamiStatus)
						_, _ = w.Write([]byte(`{"error":"validation failed"}`))
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"valid":true,"user":{"id":"user-1","name":"` + tt.whoamiName + `","email":"sam@example.com"}}`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"error":"not found"}`))
				}
			}))
			defer server.Close()

			client := &ottercli.Client{
				BaseURL: server.URL,
				Token:   "token-1",
				OrgID:   "org-1",
				HTTP:    server.Client(),
			}

			agentID, err := resolveIssueCommentAuthorRef(client, tt.explicitAuthor, tt.envAuthor)
			if tt.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrContains)
				}
				if !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Fatalf("error = %q, want contains %q", err.Error(), tt.wantErrContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantErrContains == "" && agentID != tt.wantAgentID {
				t.Fatalf("agentID = %q, want %q", agentID, tt.wantAgentID)
			}
			if whoamiCalls != tt.wantWhoamiCalls {
				t.Fatalf("whoamiCalls = %d, want %d", whoamiCalls, tt.wantWhoamiCalls)
			}
		})
	}
}

func TestIssueViewOutputResolvesOwnerName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"agents":[{"id":"agent-1","name":"Stone","slug":"stone"}]}`))
	}))
	defer server.Close()

	client := &ottercli.Client{
		BaseURL: server.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    server.Client(),
	}

	ownerID := "agent-1"
	body := "Details"
	output := issueViewOutput(client, ottercli.Issue{
		ID:           "issue-1",
		IssueNumber:  7,
		Title:        "View output test",
		State:        "open",
		WorkStatus:   "queued",
		Priority:     "P2",
		OwnerAgentID: &ownerID,
		Body:         &body,
	}, false)

	if !strings.Contains(output, "Owner: Stone") {
		t.Fatalf("expected owner name in output, got:\n%s", output)
	}
	if strings.Contains(output, "Owner: agent-1") {
		t.Fatalf("expected owner id to be resolved, got:\n%s", output)
	}
}

func TestIssueViewOutputJSON(t *testing.T) {
	ownerID := "agent-1"
	output := issueViewOutput(nil, ottercli.Issue{
		ID:           "issue-1",
		ProjectID:    "project-1",
		IssueNumber:  8,
		Title:        "JSON output test",
		State:        "open",
		WorkStatus:   "queued",
		Priority:     "P1",
		OwnerAgentID: &ownerID,
	}, true)

	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("expected valid JSON output, got error: %v\noutput: %s", err, output)
	}
	if payload["id"] != "issue-1" {
		t.Fatalf("payload id = %#v, want issue-1", payload["id"])
	}
	if payload["work_status"] != "queued" {
		t.Fatalf("payload work_status = %#v, want queued", payload["work_status"])
	}
}
