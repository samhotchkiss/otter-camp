package security

import "testing"

func TestSecretScrubberScrub(t *testing.T) {
	scrubber := NewSecretScrubber()
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "openai key", input: "token=sk-abc123def456ghi789jkl", want: "token=[REDACTED]"},
		{name: "anthropic env", input: "ANTHROPIC_API_KEY=super-secret", want: "ANTHROPIC_API_KEY=[REDACTED]"},
		{name: "bearer token", input: "Authorization: Bearer abcdefghijklmnopqrstuvwx", want: "Authorization: Bearer [REDACTED]"},
		{name: "jwt", input: "token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc123.def456", want: "token=[JWT_REDACTED]"},
		{name: "known env var", input: "OPENAI_API_KEY=abc123", want: "OPENAI_API_KEY=[REDACTED]"},
		{name: "clean", input: "normal text", want: "normal text"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scrubber.Scrub(tc.input); got != tc.want {
				t.Fatalf("Scrub(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSecretScrubberScrubMapNested(t *testing.T) {
	scrubber := NewSecretScrubber()
	input := map[string]any{
		"outer": map[string]any{
			"inner": map[string]any{
				"secret": "Authorization: Bearer abcdefghijklmnopqrstuvwx",
			},
		},
	}

	scrubbed := scrubber.ScrubMap(input)
	outer, ok := scrubbed["outer"].(map[string]any)
	if !ok {
		t.Fatalf("outer type = %T, want map[string]any", scrubbed["outer"])
	}
	inner, ok := outer["inner"].(map[string]any)
	if !ok {
		t.Fatalf("inner type = %T, want map[string]any", outer["inner"])
	}
	if got := inner["secret"]; got != "Authorization: Bearer [REDACTED]" {
		t.Fatalf("nested secret = %v, want redacted", got)
	}
}
