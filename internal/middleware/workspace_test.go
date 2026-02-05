package middleware

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceFromContext(t *testing.T) {
	t.Run("returns empty string when not set", func(t *testing.T) {
		ctx := context.Background()
		assert.Equal(t, "", WorkspaceFromContext(ctx))
	})

	t.Run("returns workspace ID when set", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), WorkspaceIDKey, "550e8400-e29b-41d4-a716-446655440000")
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", WorkspaceFromContext(ctx))
	})
}

func TestUserFromContext(t *testing.T) {
	t.Run("returns empty string when not set", func(t *testing.T) {
		ctx := context.Background()
		assert.Equal(t, "", UserFromContext(ctx))
	})

	t.Run("returns user ID when set", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), UserIDKey, "user-123")
		assert.Equal(t, "user-123", UserFromContext(ctx))
	})
}

func TestRequireWorkspace(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID := WorkspaceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(workspaceID))
	})

	t.Run("returns 401 when no workspace", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		RequireWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), "missing or invalid workspace")
	})

	t.Run("extracts workspace from X-Workspace-ID header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Workspace-ID", "550e8400-e29b-41d4-a716-446655440000")
		rec := httptest.NewRecorder()

		RequireWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", rec.Body.String())
	})

	t.Run("extracts workspace from X-Org-ID header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Org-ID", "550e8400-e29b-41d4-a716-446655440000")
		rec := httptest.NewRecorder()

		RequireWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", rec.Body.String())
	})

	t.Run("extracts workspace from query parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test?org_id=550e8400-e29b-41d4-a716-446655440000", nil)
		rec := httptest.NewRecorder()

		RequireWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", rec.Body.String())
	})

	t.Run("extracts workspace from JWT Bearer token", func(t *testing.T) {
		t.Setenv("TRUST_UNVERIFIED_JWT_WORKSPACE_CLAIMS", "true")

		claims := map[string]string{
			"org_id": "550e8400-e29b-41d4-a716-446655440000",
			"sub":    "user-123",
		}
		token := createTestJWT(t, claims)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()

		RequireWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", rec.Body.String())
	})

	t.Run("prefers JWT over headers", func(t *testing.T) {
		t.Setenv("TRUST_UNVERIFIED_JWT_WORKSPACE_CLAIMS", "true")

		claims := map[string]string{
			"org_id": "11111111-1111-1111-1111-111111111111",
		}
		token := createTestJWT(t, claims)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Workspace-ID", "22222222-2222-2222-2222-222222222222")
		rec := httptest.NewRecorder()

		RequireWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		// JWT takes precedence
		assert.Equal(t, "11111111-1111-1111-1111-111111111111", rec.Body.String())
	})

	t.Run("does not trust JWT claims by default", func(t *testing.T) {
		claims := map[string]string{
			"org_id": "11111111-1111-1111-1111-111111111111",
		}
		token := createTestJWT(t, claims)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Workspace-ID", "22222222-2222-2222-2222-222222222222")
		rec := httptest.NewRecorder()

		RequireWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "22222222-2222-2222-2222-222222222222", rec.Body.String())
	})

	t.Run("rejects invalid UUID in header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Workspace-ID", "not-a-valid-uuid")
		rec := httptest.NewRecorder()

		RequireWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestOptionalWorkspace(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID := WorkspaceFromContext(r.Context())
		if workspaceID == "" {
			workspaceID = "none"
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(workspaceID))
	})

	t.Run("proceeds without workspace", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()

		OptionalWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "none", rec.Body.String())
	})

	t.Run("sets workspace when present", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Workspace-ID", "550e8400-e29b-41d4-a716-446655440000")
		rec := httptest.NewRecorder()

		OptionalWorkspace(handler).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", rec.Body.String())
	})
}

func TestParseJWTClaims(t *testing.T) {
	t.Run("parses valid JWT", func(t *testing.T) {
		claims := map[string]string{
			"org_id":          "550e8400-e29b-41d4-a716-446655440000",
			"organization_id": "org-123",
			"workspace_id":    "ws-456",
			"sub":             "user-789",
		}
		token := createTestJWT(t, claims)

		result := parseJWTClaims(token)

		require.NotNil(t, result)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", result.OrgID)
		assert.Equal(t, "org-123", result.OrganizationID)
		assert.Equal(t, "ws-456", result.WorkspaceID)
		assert.Equal(t, "user-789", result.Sub)
	})

	t.Run("returns nil for invalid JWT", func(t *testing.T) {
		assert.Nil(t, parseJWTClaims("not.a.jwt"))
		assert.Nil(t, parseJWTClaims("only.two"))
		assert.Nil(t, parseJWTClaims(""))
	})

	t.Run("returns nil for malformed payload", func(t *testing.T) {
		// Valid structure but invalid base64 in payload
		assert.Nil(t, parseJWTClaims("header.!!!invalid!!!.signature"))
	})
}

func TestFirstValidUUID(t *testing.T) {
	t.Run("returns first valid UUID", func(t *testing.T) {
		result := firstValidUUID("", "not-uuid", "550e8400-e29b-41d4-a716-446655440000", "650e8400-e29b-41d4-a716-446655440001")
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", result)
	})

	t.Run("returns empty when no valid UUID", func(t *testing.T) {
		result := firstValidUUID("", "not-uuid", "also-not-uuid")
		assert.Equal(t, "", result)
	})

	t.Run("trims whitespace", func(t *testing.T) {
		result := firstValidUUID("  550e8400-e29b-41d4-a716-446655440000  ")
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", result)
	})
}

// createTestJWT creates a minimal JWT for testing (NOT cryptographically valid)
func createTestJWT(t *testing.T, claims map[string]string) string {
	t.Helper()

	header := base64.URLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

	claimsJSON, err := json.Marshal(claims)
	require.NoError(t, err)
	payload := base64.URLEncoding.EncodeToString(claimsJSON)

	signature := base64.URLEncoding.EncodeToString([]byte("test-signature"))

	return header + "." + payload + "." + signature
}
