package ottercli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadConfig(t *testing.T) {
	tmp := t.TempDir()
	oldHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", oldHome)

	cfg := Config{
		APIBaseURL:  "http://localhost:4200",
		Token:       "oc_sess_test",
		DefaultOrg:  "00000000-0000-0000-0000-000000000000",
		DatabaseURL: "postgres://user:pass@localhost:5432/otter?sslmode=disable",
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if loaded.APIBaseURL != cfg.APIBaseURL || loaded.Token != cfg.Token || loaded.DefaultOrg != cfg.DefaultOrg || loaded.DatabaseURL != cfg.DatabaseURL {
		t.Fatalf("config mismatch: %#v", loaded)
	}

	path, err := ConfigPath()
	if err != nil {
		t.Fatalf("config path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config not written: %v", err)
	}

	if filepath.Dir(path) == "" {
		t.Fatalf("expected config dir")
	}
}
