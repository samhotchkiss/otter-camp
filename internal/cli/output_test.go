package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestOutputFormatterTableMode(t *testing.T) {
	var out bytes.Buffer
	formatter, err := NewOutputFormatter(OutputModeTable, &out, true)
	if err != nil {
		t.Fatalf("NewOutputFormatter returned error: %v", err)
	}
	if err := formatter.WriteTable([]string{"ID", "Name"}, [][]string{{"1", "Alpha"}}); err != nil {
		t.Fatalf("WriteTable returned error: %v", err)
	}
	result := out.String()
	if !strings.Contains(result, "ID") || !strings.Contains(result, "Name") {
		t.Fatalf("table output missing headers: %q", result)
	}
	if !strings.Contains(result, "Alpha") {
		t.Fatalf("table output missing row: %q", result)
	}
}

func TestOutputFormatterJSONMode(t *testing.T) {
	var out bytes.Buffer
	formatter, err := NewOutputFormatter(OutputModeJSON, &out, true)
	if err != nil {
		t.Fatalf("NewOutputFormatter returned error: %v", err)
	}
	if err := formatter.WriteJSON(map[string]any{"data": map[string]any{"id": "abc"}}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}
	result := out.String()
	if !strings.Contains(result, "\"data\"") || !strings.Contains(result, "\"id\": \"abc\"") {
		t.Fatalf("json output mismatch: %q", result)
	}
}

func TestOutputFormatterQuietMode(t *testing.T) {
	var out bytes.Buffer
	formatter, err := NewOutputFormatter(OutputModeQuiet, &out, true)
	if err != nil {
		t.Fatalf("NewOutputFormatter returned error: %v", err)
	}
	if err := formatter.WriteQuiet("  abc123  "); err != nil {
		t.Fatalf("WriteQuiet returned error: %v", err)
	}
	if got := out.String(); got != "abc123\n" {
		t.Fatalf("quiet output = %q, want %q", got, "abc123\\n")
	}
}
