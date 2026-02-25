package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSecretSlugPrefersPositional(t *testing.T) {
	got := resolveSecretSlug([]string{"positional-slug"}, "flag-slug")
	if got != "positional-slug" {
		t.Fatalf("resolveSecretSlug positional = %q, want %q", got, "positional-slug")
	}

	got = resolveSecretSlug(nil, "flag-slug")
	if got != "flag-slug" {
		t.Fatalf("resolveSecretSlug flag fallback = %q, want %q", got, "flag-slug")
	}
}

func TestReadSecretValueFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("file-secret-value\n"), 0o600); err != nil {
		t.Fatalf("write temp secret file: %v", err)
	}

	got, err := readSecretValueWithTerminal("", path, nil, nil, func(*os.File) (bool, error) {
		return false, nil
	})
	if err != nil {
		t.Fatalf("readSecretValueWithTerminal from file: %v", err)
	}
	if got != "file-secret-value" {
		t.Fatalf("secret value = %q, want %q", got, "file-secret-value")
	}
}

func TestReadSecretValuePromptsOnTTY(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()

	if _, err := writeEnd.WriteString("typed-secret\n"); err != nil {
		t.Fatalf("write typed secret: %v", err)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}

	var stderr bytes.Buffer
	got, err := readSecretValueWithTerminal("", "", readEnd, &stderr, func(*os.File) (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("readSecretValueWithTerminal tty read: %v", err)
	}
	if got != "typed-secret" {
		t.Fatalf("secret value = %q, want %q", got, "typed-secret")
	}
	if !strings.Contains(stderr.String(), "Secret value:") {
		t.Fatalf("stderr = %q, want prompt containing %q", stderr.String(), "Secret value:")
	}
}
