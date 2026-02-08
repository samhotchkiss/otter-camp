package main

import (
	"errors"
	"strings"
	"testing"
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
