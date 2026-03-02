package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestApplyAnthropicSubscriptionHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	applyAnthropicSubscriptionHeaders(req, "sk-ant-oat-test-token")

	if got := req.Header.Get("Authorization"); got != "Bearer sk-ant-oat-test-token" {
		t.Fatalf("Authorization = %q, want Bearer token", got)
	}
	if got := req.Header.Get("anthropic-version"); got != defaultAnthropicVersion {
		t.Fatalf("anthropic-version = %q, want %q", got, defaultAnthropicVersion)
	}
	if got := req.Header.Get("anthropic-beta"); got == "" || !strings.Contains(got, "oauth-2025-04-20") {
		t.Fatalf("anthropic-beta = %q, want oauth beta header", got)
	}
	if got := req.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want empty for subscription auth", got)
	}
}

func TestResolveAnthropicSubscriptionStateRefreshesExpiredToken(t *testing.T) {
	fixedNow := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	refreshCalls := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		if r.Method != http.MethodPost {
			t.Fatalf("token endpoint method = %s, want POST", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode refresh payload: %v", err)
		}
		if payload["grant_type"] != "refresh_token" {
			t.Fatalf("grant_type = %v, want refresh_token", payload["grant_type"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"sk-ant-oat-new","refresh_token":"refresh-new","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	resolver := &stubSecretResolver{
		values: map[string]string{
			"ref:access":  "sk-ant-oat-old",
			"ref:refresh": "refresh-old",
		},
	}

	gateway := &LiveModelGateway{
		secrets:    resolver,
		httpClient: tokenServer.Client(),
		now:        func() time.Time { return fixedNow },
		subscription: struct {
			mu     sync.Mutex
			tokens map[uuid.UUID]anthropicSubscriptionTokenState
		}{
			tokens: map[uuid.UUID]anthropicSubscriptionTokenState{},
		},
	}
	connectionID := uuid.New()
	gateway.subscription.tokens[connectionID] = anthropicSubscriptionTokenState{
		AccessToken:  "sk-ant-oat-expired",
		RefreshToken: "refresh-cached",
		ExpiresAt:    fixedNow.Add(-time.Minute),
	}

	state, err := gateway.resolveAnthropicSubscriptionState(context.Background(), uuid.New(), connectionID, anthropicAuthConfig{
		Mode:            anthropicAuthModeSubscription,
		AccessTokenRef:  "ref:access",
		RefreshTokenRef: "ref:refresh",
		TokenURL:        tokenServer.URL,
		ClientID:        "test-client-id",
	})
	if err != nil {
		t.Fatalf("resolveAnthropicSubscriptionState error: %v", err)
	}
	if state.AccessToken != "sk-ant-oat-new" {
		t.Fatalf("access token = %q, want refreshed token", state.AccessToken)
	}
	if state.RefreshToken != "refresh-new" {
		t.Fatalf("refresh token = %q, want refreshed token", state.RefreshToken)
	}
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if got := resolver.resolveCalls["ref:access"]; got != 0 {
		t.Fatalf("access token secret resolve calls = %d, want 0 when cached token is refreshed", got)
	}
}

type stubSecretResolver struct {
	values       map[string]string
	resolveCalls map[string]int
}

func (s *stubSecretResolver) ResolveRef(_ context.Context, _ uuid.UUID, ref string) (string, error) {
	if s.resolveCalls == nil {
		s.resolveCalls = map[string]int{}
	}
	s.resolveCalls[ref]++
	return s.values[ref], nil
}
