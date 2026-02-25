package security

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	openAIKeyPattern      = regexp.MustCompile(`sk-[A-Za-z0-9][A-Za-z0-9-]{19,}`)
	anthropicLinePattern  = regexp.MustCompile(`(?m)(ANTHROPIC_API_KEY=)([^\s"']+)`)
	knownEnvSecretPattern = regexp.MustCompile(`(?m)(OPENAI_API_KEY|ANTHROPIC_API_KEY|OTTERCAMP_MASTER_KEY|OTTERCAMP_DB_URL)=([^\s"']+)`)
	authorizationPattern  = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)[A-Za-z0-9._-]{20,}`)
	bearerPattern         = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._-]{20,}`)
	jwtPattern            = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	secretSlugPattern     = regexp.MustCompile(`\$secret\.[A-Za-z0-9_\-]+`)
)

type SecretScrubber struct{}

func NewSecretScrubber() *SecretScrubber {
	return &SecretScrubber{}
}

func (s *SecretScrubber) Scrub(input string) string {
	if s == nil || strings.TrimSpace(input) == "" {
		return input
	}

	out := input
	out = openAIKeyPattern.ReplaceAllString(out, "[REDACTED]")
	out = anthropicLinePattern.ReplaceAllString(out, "${1}[REDACTED]")
	out = knownEnvSecretPattern.ReplaceAllString(out, "${1}=[REDACTED]")
	out = authorizationPattern.ReplaceAllString(out, "${1}[REDACTED]")
	out = bearerPattern.ReplaceAllString(out, "Bearer [REDACTED]")
	out = jwtPattern.ReplaceAllString(out, "[JWT_REDACTED]")
	out = secretSlugPattern.ReplaceAllString(out, "[REDACTED]")
	return out
}

func (s *SecretScrubber) ScrubMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = s.scrubAny(value)
	}
	return cloned
}

func (s *SecretScrubber) ScrubJSON(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(`{}`)
	}

	var parsed any
	if err := json.Unmarshal(input, &parsed); err != nil {
		return json.RawMessage(s.Scrub(string(input)))
	}

	scrubbed := s.scrubAny(parsed)
	encoded, err := json.Marshal(scrubbed)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}

func (s *SecretScrubber) scrubAny(value any) any {
	switch typed := value.(type) {
	case string:
		return s.Scrub(typed)
	case map[string]any:
		return s.ScrubMap(typed)
	case []any:
		items := make([]any, len(typed))
		for idx := range typed {
			items[idx] = s.scrubAny(typed[idx])
		}
		return items
	default:
		return typed
	}
}
