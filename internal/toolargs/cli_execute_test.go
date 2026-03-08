package toolargs

import "testing"

func TestNormalizeCLIExecuteRecoversMalformedRawAliases(t *testing.T) {
	normalized := Normalize("cli.execute", map[string]any{
		"_raw": `{"cmd":"cat <<'EOF' > docs/strategy.md\n# Strategy\n- Recover the task\nEOF","working_dir":"recovery","timeout_ms":1500`,
	})

	if got := normalized["command"]; got != "cat <<'EOF' > docs/strategy.md\n# Strategy\n- Recover the task\nEOF" {
		t.Fatalf("command = %v, want recovered heredoc command", got)
	}
	if got := normalized["working_directory"]; got != "recovery" {
		t.Fatalf("working_directory = %v, want recovery", got)
	}
	if got := normalized["timeout_seconds"]; got != 2 {
		t.Fatalf("timeout_seconds = %v, want 2", got)
	}
	if _, exists := normalized["_raw"]; exists {
		t.Fatalf("expected _raw to be removed: %+v", normalized)
	}
}

func TestNormalizeCLIExecuteDirectAliases(t *testing.T) {
	normalized := Normalize("cli_execute", map[string]any{
		"cmd":        "go test ./...",
		"cwd":        "subdir",
		"timeout_ms": 250,
		"env":        map[string]any{"MODE": "test", "COUNT": 2},
	})

	if got := normalized["command"]; got != "go test ./..." {
		t.Fatalf("command = %v, want go test ./...", got)
	}
	if got := normalized["working_directory"]; got != "subdir" {
		t.Fatalf("working_directory = %v, want subdir", got)
	}
	if got := normalized["timeout_seconds"]; got != 1 {
		t.Fatalf("timeout_seconds = %v, want 1", got)
	}
	envOverrides, ok := normalized["env_overrides"].(map[string]any)
	if !ok {
		t.Fatalf("env_overrides type = %T, want map[string]any", normalized["env_overrides"])
	}
	if envOverrides["MODE"] != "test" || envOverrides["COUNT"] != "2" {
		t.Fatalf("env_overrides = %+v, want MODE=test COUNT=2", envOverrides)
	}
}

func TestNormalizeCLIExecuteTreatsPlainRawAsCommand(t *testing.T) {
	normalized := Normalize("cli.execute", map[string]any{"_raw": "git status --short"})
	if got := normalized["command"]; got != "git status --short" {
		t.Fatalf("command = %v, want git status --short", got)
	}
	if _, exists := normalized["_raw"]; exists {
		t.Fatalf("expected _raw to be removed: %+v", normalized)
	}
}

func TestCLIExecuteAttemptFingerprintStableAcrossAliases(t *testing.T) {
	first := AttemptFingerprint("cli.execute", map[string]any{
		"cmd":        "git status",
		"cwd":        "repo",
		"timeout_ms": 500,
	})
	second := AttemptFingerprint("cli.execute", map[string]any{
		"command":           "git status",
		"working_directory": "repo",
		"timeout_seconds":   1,
	})

	if first != second {
		t.Fatalf("attempt fingerprints differ: %q vs %q", first, second)
	}
}
