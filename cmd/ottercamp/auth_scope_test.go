package main

import "testing"

func TestInteractiveClientAPIKeyScopesCoverTUIRuntimeOperatorFlows(t *testing.T) {
	scopes := interactiveClientAPIKeyScopes()
	required := []string{
		"realtime:read",
		"workspace:read",
		"chat:read",
		"chat:write",
		"projects:read",
		"projects:write",
		"agents:read",
	}

	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		seen[scope] = struct{}{}
	}
	for _, scope := range required {
		if _, ok := seen[scope]; !ok {
			t.Fatalf("interactiveClientAPIKeyScopes missing %q; scopes=%v", scope, scopes)
		}
	}
}
