package api

import (
	"regexp"
	"strings"
)

var chameleonSessionKeyRegex = regexp.MustCompile(
	`^agent:chameleon:oc:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`,
)

// ValidateChameleonSessionKey returns true when the key matches the canonical
// Spec 110 format: agent:chameleon:oc:{agentUUID}.
func ValidateChameleonSessionKey(sessionKey string) bool {
	_, ok := ExtractChameleonSessionAgentID(sessionKey)
	return ok
}

// ExtractChameleonSessionAgentID extracts the UUID from a canonical chameleon
// session key and normalizes it to lowercase.
func ExtractChameleonSessionAgentID(sessionKey string) (string, bool) {
	matches := chameleonSessionKeyRegex.FindStringSubmatch(strings.TrimSpace(sessionKey))
	if len(matches) != 2 {
		return "", false
	}
	return strings.ToLower(matches[1]), true
}

// ExtractSessionAgentIdentity extracts the agent identity token from session
// keys. Canonical chameleon keys return a UUID. Legacy keys return the first
// agent token after the "agent:" prefix.
func ExtractSessionAgentIdentity(sessionKey string) string {
	if canonicalID, ok := ExtractChameleonSessionAgentID(sessionKey); ok {
		return canonicalID
	}

	trimmed := strings.TrimSpace(sessionKey)
	if trimmed == "" || !strings.HasPrefix(strings.ToLower(trimmed), "agent:") {
		return ""
	}

	rest := strings.TrimSpace(trimmed[len("agent:"):])
	if rest == "" {
		return ""
	}

	if idx := strings.IndexRune(rest, ':'); idx >= 0 {
		return strings.TrimSpace(rest[:idx])
	}
	return rest
}
