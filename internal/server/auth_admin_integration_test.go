//go:build integration

package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestAdminMagicLinkAndConsumeFlow(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")

	testServer, adminUser, _ := newAuthTestServer(t, "standard")
	defer testServer.Close()

	adminToken := loginToken(t, testServer.URL, adminUser.Email, "admin-password")

	listed := mustJSON(t, http.MethodGet, testServer.URL+"/v1/admin/users?email="+adminUser.Email, nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if listed.StatusCode != http.StatusOK {
		t.Fatalf("list users status = %d, want %d body=%s", listed.StatusCode, http.StatusOK, string(listed.Body))
	}
	userID := jsonPathString(t, listed.Body, "data", "0", "id")
	if userID == "" {
		t.Fatalf("expected user id in list response: %s", string(listed.Body))
	}

	magic := mustJSON(t, http.MethodPost, testServer.URL+"/v1/admin/users/"+userID+"/magic-link", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	if magic.StatusCode != http.StatusOK {
		t.Fatalf("magic-link status = %d, want %d body=%s", magic.StatusCode, http.StatusOK, string(magic.Body))
	}
	magicURL := jsonPathString(t, magic.Body, "data", "magic_link_url")
	if !strings.Contains(magicURL, "/auth/magic?token=") {
		t.Fatalf("unexpected magic link url: %q", magicURL)
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(magicURL)
	if err != nil {
		t.Fatalf("consume magic link request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("consume status = %d, want %d", resp.StatusCode, http.StatusFound)
	}
	if location := resp.Header.Get("Location"); location != "/" {
		t.Fatalf("redirect location = %q, want %q", location, "/")
	}

	var sessionToken string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "ottercamp_session" {
			sessionToken = strings.TrimSpace(cookie.Value)
			break
		}
	}
	if sessionToken == "" {
		t.Fatal("expected ottercamp_session cookie")
	}

	me := mustJSON(t, http.MethodGet, testServer.URL+"/v1/auth/me", nil, map[string]string{
		"Authorization": "Bearer " + sessionToken,
	})
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want %d body=%s", me.StatusCode, http.StatusOK, string(me.Body))
	}
	if got := jsonPathString(t, me.Body, "data", "email"); got != adminUser.Email {
		t.Fatalf("me email = %q, want %q", got, adminUser.Email)
	}
}
