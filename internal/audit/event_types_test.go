package audit

import (
	"regexp"
	"testing"
)

func TestAuthEventTypeConstantsFollowDomainActionNaming(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z0-9]+(?:\.[a-z0-9_]+)+$`)

	eventTypes := []string{
		EventAuthLogin,
		EventAuthLogout,
		EventAuthLoginFailed,
		EventAuthSessionRevoked,
		EventAPIKeyIssued,
		EventAPIKeyRevoked,
	}

	for _, eventType := range eventTypes {
		if !pattern.MatchString(eventType) {
			t.Fatalf("event type %q does not match domain.action format", eventType)
		}
	}
}
