package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{name: "overflow", input: "99999999999999999999", wantErr: true},
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

func TestResolveIssueIDUsesIssueNumberQueryFilter(t *testing.T) {
	var requestedIssueNumber string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects":
			_, _ = w.Write([]byte(`{"projects":[{"id":"project-1","name":"Project One","slug":"project-one"}],"total":1}`))
			return
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues":
			values, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil {
				t.Fatalf("failed to parse query: %v", err)
			}
			requestedIssueNumber = values.Get("issue_number")
			if requestedIssueNumber == "42" {
				_, _ = w.Write([]byte(`{"items":[{"id":"issue-42","project_id":"project-1","issue_number":42,"title":"Issue 42","state":"open","origin":"local","approval_state":"draft","work_status":"queued","priority":"P2"}],"total":1}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
	}))
	defer srv.Close()

	client := &ottercli.Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	got, err := resolveIssueID(client, "project-1", "42")
	if err != nil {
		t.Fatalf("resolveIssueID() error = %v", err)
	}
	if got != "issue-42" {
		t.Fatalf("resolveIssueID() = %q, want issue-42", got)
	}
	if requestedIssueNumber != "42" {
		t.Fatalf("issue_number query = %q, want 42", requestedIssueNumber)
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
			name:  "workflow flags remain in flag args",
			input: []string{"Agent Avatars", "--workflow", "--schedule", "15m", "--template-title", "Run {{datetime}}"},
			wantFlags: []string{
				"--workflow",
				"--schedule", "15m",
				"--template-title", "Run {{datetime}}",
			},
			wantNames: []string{"Agent Avatars"},
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

func TestBuildWorkflowSchedulePayload(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		tz       string
		wantKind string
		wantExpr string
		wantMs   int
		wantTZ   string
	}{
		{
			name:     "defaults to cron",
			schedule: "",
			tz:       "",
			wantKind: "cron",
			wantExpr: "0 6 * * *",
			wantTZ:   "America/Denver",
		},
		{
			name:     "duration every schedule",
			schedule: "15m",
			wantKind: "every",
			wantMs:   900000,
		},
		{
			name:     "cron with timezone",
			schedule: "0 9 * * *",
			tz:       "America/New_York",
			wantKind: "cron",
			wantExpr: "0 9 * * *",
			wantTZ:   "America/New_York",
		},
	}

	for _, tt := range tests {
		payload := buildWorkflowSchedulePayload(tt.schedule, tt.tz)
		if payload["kind"] != tt.wantKind {
			t.Fatalf("%s: kind = %#v, want %q", tt.name, payload["kind"], tt.wantKind)
		}
		if tt.wantExpr != "" && payload["expr"] != tt.wantExpr {
			t.Fatalf("%s: expr = %#v, want %q", tt.name, payload["expr"], tt.wantExpr)
		}
		if tt.wantMs > 0 {
			if got, ok := payload["everyMs"].(int); !ok || got != tt.wantMs {
				t.Fatalf("%s: everyMs = %#v, want %d", tt.name, payload["everyMs"], tt.wantMs)
			}
		}
		if tt.wantTZ != "" && payload["tz"] != tt.wantTZ {
			t.Fatalf("%s: tz = %#v, want %q", tt.name, payload["tz"], tt.wantTZ)
		}
	}
}

func TestReleaseGatePayloadOK(t *testing.T) {
	if releaseGatePayloadOK(nil) {
		t.Fatalf("releaseGatePayloadOK(nil) = true, want false")
	}
	if releaseGatePayloadOK(map[string]interface{}{"ok": false}) {
		t.Fatalf("releaseGatePayloadOK(false) = true, want false")
	}
	if !releaseGatePayloadOK(map[string]interface{}{"ok": true}) {
		t.Fatalf("releaseGatePayloadOK(true) = false, want true")
	}
}

func TestParseChameleonSessionAgentID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "valid lowercase uuid",
			input: "agent:chameleon:oc:a1b2c3d4-5678-90ab-cdef-1234567890ab",
			want:  "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		},
		{
			name:  "valid uppercase uuid normalized",
			input: "agent:chameleon:oc:A1B2C3D4-5678-90AB-CDEF-1234567890AB",
			want:  "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		},
		{
			name:    "invalid format",
			input:   "agent:main:slack",
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		got, err := parseChameleonSessionAgentID(tt.input)
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
			t.Fatalf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}

func TestSlugifyAgentName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "Derek", want: "derek"},
		{input: "Engineering Lead", want: "engineering-lead"},
		{input: "  ", want: "agent"},
		{input: "Nova!!!", want: "nova"},
	}

	for _, tt := range tests {
		if got := slugifyAgentName(tt.input); got != tt.want {
			t.Fatalf("slugifyAgentName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildAgentCreatePayloadIncludesRoleWhenProvided(t *testing.T) {
	payload := buildAgentCreatePayload("Riley", "riley", "gpt-5.2-codex", "Engineering Lead")

	if payload["slot"] != "riley" {
		t.Fatalf("slot = %v, want riley", payload["slot"])
	}
	if payload["display_name"] != "Riley" {
		t.Fatalf("display_name = %v, want Riley", payload["display_name"])
	}
	if payload["model"] != "gpt-5.2-codex" {
		t.Fatalf("model = %v, want gpt-5.2-codex", payload["model"])
	}
	if payload["role"] != "Engineering Lead" {
		t.Fatalf("role = %v, want Engineering Lead", payload["role"])
	}
}

func TestBuildAgentCreatePayloadOmitsRoleWhenEmpty(t *testing.T) {
	payload := buildAgentCreatePayload("Riley", "riley", "gpt-5.2-codex", "   ")
	if _, ok := payload["role"]; ok {
		t.Fatalf("expected role key to be omitted when empty, got payload: %#v", payload)
	}
}

func TestMemoryResolveMemoryWriteKind(t *testing.T) {
	tests := []struct {
		name         string
		daily        bool
		explicitKind string
		want         string
		wantErr      bool
	}{
		{name: "daily flag overrides", daily: true, explicitKind: "note", want: "daily"},
		{name: "default to note", daily: false, explicitKind: "", want: "note"},
		{name: "explicit long term", daily: false, explicitKind: "long_term", want: "long_term"},
		{name: "invalid explicit kind", daily: false, explicitKind: "bad", wantErr: true},
		{name: "explicit kind normalized", daily: false, explicitKind: " Long_Term ", want: "long_term"},
	}

	for _, tt := range tests {
		got, err := resolveMemoryWriteKind(tt.daily, tt.explicitKind)
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
			t.Fatalf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}

func TestMemoryValidateAgentUUID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid uuid", input: "11111111-2222-3333-4444-555555555555"},
		{name: "empty", input: "", wantErr: true},
		{name: "invalid", input: "not-a-uuid", wantErr: true},
	}

	for _, tt := range tests {
		err := validateAgentUUID(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("%s: expected error, got none", tt.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.name, err)
		}
	}
}

func TestHandleIssueAskParsesQuestionSpecs(t *testing.T) {
	questions, err := parseIssueAskQuestions([]string{
		`{"id":"q1","text":"Realtime transport?","type":"select","required":true,"options":["WebSocket","Polling"]}`,
		`{"id":"q2","text":"Target latency","type":"number","default":1.5}`,
	})
	if err != nil {
		t.Fatalf("parseIssueAskQuestions() error = %v", err)
	}
	if len(questions) != 2 {
		t.Fatalf("parseIssueAskQuestions() len = %d, want 2", len(questions))
	}
	if questions[0].ID != "q1" || questions[0].Type != "select" || len(questions[0].Options) != 2 {
		t.Fatalf("unexpected first question payload: %#v", questions[0])
	}
	if questions[1].ID != "q2" || questions[1].Type != "number" {
		t.Fatalf("unexpected second question payload: %#v", questions[1])
	}
}

func TestHandleIssueAskRejectsMalformedQuestionSpecs(t *testing.T) {
	_, err := parseIssueAskQuestions([]string{`{"id":"q1","text":"Bad"}`})
	if err == nil || !strings.Contains(err.Error(), "type is required") {
		t.Fatalf("expected type validation error, got %v", err)
	}

	_, err = parseIssueAskQuestions([]string{`{"id":"q1","text":"Choose","type":"select"}`})
	if err == nil || !strings.Contains(err.Error(), "requires options") {
		t.Fatalf("expected options validation error, got %v", err)
	}

	_, err = parseIssueAskQuestions([]string{`{not-json}`})
	if err == nil || !strings.Contains(err.Error(), "invalid --question") {
		t.Fatalf("expected invalid json error, got %v", err)
	}
}

func TestHandleIssueRespondParsesKeyedResponses(t *testing.T) {
	responses, err := parseIssueRespondEntries([]string{
		`q1="WebSocket"`,
		`q2=true`,
		`q3=["Desktop web","Mobile web"]`,
		`q4=1.5`,
		`q5=free-form text`,
	})
	if err != nil {
		t.Fatalf("parseIssueRespondEntries() error = %v", err)
	}

	if responses["q1"] != "WebSocket" {
		t.Fatalf("q1 = %#v, want %q", responses["q1"], "WebSocket")
	}
	if responses["q2"] != true {
		t.Fatalf("q2 = %#v, want true", responses["q2"])
	}
	if responses["q4"] != 1.5 {
		t.Fatalf("q4 = %#v, want 1.5", responses["q4"])
	}
	if responses["q5"] != "free-form text" {
		t.Fatalf("q5 = %#v, want free-form text", responses["q5"])
	}
	rawList, ok := responses["q3"].([]interface{})
	if !ok || len(rawList) != 2 {
		t.Fatalf("q3 = %#v, want 2-entry list", responses["q3"])
	}
}

func TestHandleIssueRespondRejectsMalformedResponses(t *testing.T) {
	_, err := parseIssueRespondEntries([]string{`=true`})
	if err == nil || !strings.Contains(err.Error(), "question id is required") {
		t.Fatalf("expected missing id error, got %v", err)
	}

	_, err = parseIssueRespondEntries([]string{`q1=`})
	if err == nil || !strings.Contains(err.Error(), "response value is required") {
		t.Fatalf("expected missing value error, got %v", err)
	}

	_, err = parseIssueRespondEntries([]string{`q1=true`, `q1=false`})
	if err == nil || !strings.Contains(err.Error(), "duplicate response key") {
		t.Fatalf("expected duplicate key error, got %v", err)
	}
}
