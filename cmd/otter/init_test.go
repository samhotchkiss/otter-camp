package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandleInitHostedModeFlagPrintsHandoff(t *testing.T) {
	var out bytes.Buffer
	err := runInitCommand([]string{"--mode", "hosted"}, strings.NewReader(""), &out)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if !strings.Contains(out.String(), "Visit otter.camp/setup to get started.") {
		t.Fatalf("expected hosted handoff message, got %q", out.String())
	}
}

func TestHandleInitPromptRoutesHostedSelection(t *testing.T) {
	var out bytes.Buffer
	err := runInitCommand(nil, strings.NewReader("2\n"), &out)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Welcome to Otter Camp!") {
		t.Fatalf("expected welcome prompt, got %q", output)
	}
	if !strings.Contains(output, "[2] Hosted") {
		t.Fatalf("expected hosted option in prompt, got %q", output)
	}
	if !strings.Contains(output, "Visit otter.camp/setup to get started.") {
		t.Fatalf("expected hosted handoff message, got %q", output)
	}
}

func TestHandleInitPromptDefaultsToLocalSelection(t *testing.T) {
	var out bytes.Buffer
	err := runInitCommand(nil, strings.NewReader("\n"), &out)
	if err != nil {
		t.Fatalf("runInitCommand() error = %v", err)
	}
	if !strings.Contains(out.String(), "Local install selected.") {
		t.Fatalf("expected local-mode message, got %q", out.String())
	}
}

func TestHandleInitRejectsInvalidModeFlag(t *testing.T) {
	err := runInitCommand([]string{"--mode", "cloud"}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil {
		t.Fatalf("expected invalid mode error")
	}
	if !strings.Contains(err.Error(), "--mode must be local or hosted") {
		t.Fatalf("expected mode validation error, got %v", err)
	}
}
