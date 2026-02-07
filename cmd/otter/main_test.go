package main

import "testing"

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
