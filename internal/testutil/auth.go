package testutil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"golang.org/x/crypto/bcrypt"
)

const DefaultUserPassword = "password-123"

func MakeOrg(t testing.TB, db *pgxpool.Pool) uuid.UUID {
	t.Helper()

	created, err := repo.NewOrgRepo(db).Create(context.Background(), repo.Organization{
		Slug:        "org-" + strings.ToLower(uuid.NewString()),
		DisplayName: "Test Org",
	})
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	return created.ID
}

func MakeUser(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, role string) repo.HumanUser {
	t.Helper()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(DefaultUserPassword), 12)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	password := string(passwordHash)

	created, err := repo.NewHumanUserRepo(db).Create(context.Background(), repo.HumanUser{
		OrganizationID: orgID,
		Email:          strings.ToLower(uuid.NewString()) + "@example.com",
		DisplayName:    "Test User",
		PasswordHash:   &password,
		Role:           strings.TrimSpace(role),
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return created
}

func LoginUser(t testing.TB, srv *httptest.Server, email, password string) string {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"email":    strings.TrimSpace(email),
		"password": password,
	})
	if err != nil {
		t.Fatalf("marshal login payload: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/auth/login", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute login request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d want=%d", resp.StatusCode, http.StatusOK)
	}

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if token, _ := envelope.Data["session_token"].(string); strings.TrimSpace(token) != "" {
		return token
	}
	if token, _ := envelope.Data["token"].(string); strings.TrimSpace(token) != "" {
		return token
	}

	t.Fatal("login response missing session token")
	return ""
}

func MakeAPIKey(t testing.TB, db *pgxpool.Pool, userID uuid.UUID) string {
	t.Helper()

	raw := "otk_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	hash := sha256.Sum256([]byte(raw))

	_, err := repo.NewAPIKeyRepo(db).Create(context.Background(), repo.APIKey{
		UserID:      userID,
		KeyHash:     hex.EncodeToString(hash[:]),
		KeyPrefix:   raw[:8],
		DisplayName: "Test API Key",
		Scopes:      []string{"chat:read"},
		ExpiresAt:   ptrTime(time.Now().UTC().Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return raw
}

func ptrTime(v time.Time) *time.Time {
	return &v
}
