package config

import (
	"testing"
)

func TestLoadFromLookupParsesValues(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"OTTERCAMP_MODE":      "test",
		"OTTERCAMP_LOG_LEVEL": "debug",
		"OTTERCAMP_ADDR":      "127.0.0.1:9999",
	})

	cfg, err := LoadFromLookup(lookup)
	if err != nil {
		t.Fatalf("LoadFromLookup returned error: %v", err)
	}

	if cfg.Mode != ModeTest {
		t.Fatalf("mode = %q, want %q", cfg.Mode, ModeTest)
	}

	if cfg.LogLevel != LogLevelDebug {
		t.Fatalf("log level = %q, want %q", cfg.LogLevel, LogLevelDebug)
	}

	if cfg.Addr != "127.0.0.1:9999" {
		t.Fatalf("addr = %q, want %q", cfg.Addr, "127.0.0.1:9999")
	}
}

func TestLoadFromLookupAppliesDefaults(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"OTTERCAMP_MODE": "development",
	})

	cfg, err := LoadFromLookup(lookup)
	if err != nil {
		t.Fatalf("LoadFromLookup returned error: %v", err)
	}

	if cfg.LogLevel != LogLevelInfo {
		t.Fatalf("log level = %q, want %q", cfg.LogLevel, LogLevelInfo)
	}

	if cfg.Addr != ":4110" {
		t.Fatalf("addr = %q, want %q", cfg.Addr, ":4110")
	}
}

func TestLoadFromLookupModeRequired(t *testing.T) {
	_, err := LoadFromLookup(mapLookup(nil))
	if err == nil {
		t.Fatal("expected error for missing OTTERCAMP_MODE")
	}
}

func TestLoadFromLookupRejectsUnknownMode(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"OTTERCAMP_MODE": "banana",
	})

	_, err := LoadFromLookup(lookup)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestLoadFromLookupRejectsInvalidLogLevel(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"OTTERCAMP_MODE":      "production",
		"OTTERCAMP_LOG_LEVEL": "loud",
	})

	_, err := LoadFromLookup(lookup)
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func mapLookup(m map[string]string) func(string) (string, bool) {
	if m == nil {
		m = map[string]string{}
	}

	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}
