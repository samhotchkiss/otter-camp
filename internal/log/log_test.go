package oclog

import (
	"bytes"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/config"
)

func TestNewProductionUsesJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	cfg := config.Config{Mode: config.ModeProduction, LogLevel: config.LogLevelInfo}

	logger, err := New(cfg, &buf)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger.Info("server started", "addr", ":4110")
	out := buf.String()
	if !strings.HasPrefix(out, "{") {
		t.Fatalf("expected JSON output, got %q", out)
	}
	if !strings.Contains(out, "\"msg\":\"server started\"") {
		t.Fatalf("expected msg field in JSON output, got %q", out)
	}
}

func TestNewDevelopmentUsesTextHandler(t *testing.T) {
	var buf bytes.Buffer
	cfg := config.Config{Mode: config.ModeDevelopment, LogLevel: config.LogLevelInfo}

	logger, err := New(cfg, &buf)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger.Info("server started", "addr", ":4110")
	out := buf.String()
	if strings.HasPrefix(out, "{") {
		t.Fatalf("expected text output, got %q", out)
	}
	if !strings.Contains(out, "msg=\"server started\"") {
		t.Fatalf("expected text msg field, got %q", out)
	}
}

func TestNewRejectsInvalidLevel(t *testing.T) {
	cfg := config.Config{Mode: config.ModeDevelopment, LogLevel: "invalid"}
	if _, err := New(cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestToSlogLevelSupportsAllConfiguredLevels(t *testing.T) {
	levels := []config.LogLevel{
		config.LogLevelDebug,
		config.LogLevelInfo,
		config.LogLevelWarn,
		config.LogLevelError,
	}

	for _, level := range levels {
		if _, err := toSlogLevel(level); err != nil {
			t.Fatalf("toSlogLevel(%q) returned error: %v", level, err)
		}
	}
}
