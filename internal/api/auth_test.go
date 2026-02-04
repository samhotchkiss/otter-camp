package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleLogin_Success(t *testing.T) {
	t.Parallel()

	body := `{"email":"demo@ottercamp.io","password":"demo123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp LoginResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, "usr_demo", resp.User.ID)
	assert.Equal(t, "demo@ottercamp.io", resp.User.Email)
	assert.Equal(t, "Demo Otter", resp.User.Name)
}

func TestHandleLogin_WrongMethod(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	var resp AuthError
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Method not allowed", resp.Message)
}

func TestHandleLogin_InvalidJSON(t *testing.T) {
	t.Parallel()

	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp AuthError
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Invalid request body", resp.Message)
}

func TestHandleLogin_MissingEmail(t *testing.T) {
	t.Parallel()

	body := `{"password":"demo123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp AuthError
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Email and password are required", resp.Message)
}

func TestHandleLogin_MissingPassword(t *testing.T) {
	t.Parallel()

	body := `{"email":"demo@ottercamp.io"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp AuthError
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Email and password are required", resp.Message)
}

func TestHandleLogin_EmptyFields(t *testing.T) {
	t.Parallel()

	body := `{"email":"","password":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp AuthError
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Email and password are required", resp.Message)
}

func TestHandleLogin_UnknownEmail(t *testing.T) {
	t.Parallel()

	body := `{"email":"unknown@example.com","password":"anypassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp AuthError
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Invalid email or password", resp.Message)
}

func TestHandleLogin_WrongPassword(t *testing.T) {
	t.Parallel()

	body := `{"email":"demo@ottercamp.io","password":"wrongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	HandleLogin(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp AuthError
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Invalid email or password", resp.Message)
}

func TestGenerateJWT(t *testing.T) {
	t.Parallel()

	token, err := generateJWT("user123", "test@example.com", "Test User")
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// JWT should have 3 parts separated by dots
	parts := bytes.Split([]byte(token), []byte("."))
	assert.Len(t, parts, 3, "JWT should have header.payload.signature format")
}

func TestGetJWTSecret_Default(t *testing.T) {
	t.Parallel()

	// Unset any existing JWT_SECRET
	original := os.Getenv("JWT_SECRET")
	os.Unsetenv("JWT_SECRET")
	defer func() {
		if original != "" {
			os.Setenv("JWT_SECRET", original)
		}
	}()

	secret := getJWTSecret()
	assert.Equal(t, "otter-camp-dev-secret-change-in-production", secret)
}

func TestGetJWTSecret_FromEnv(t *testing.T) {
	// Not parallel due to env var manipulation
	original := os.Getenv("JWT_SECRET")
	os.Setenv("JWT_SECRET", "my-custom-secret")
	defer func() {
		if original != "" {
			os.Setenv("JWT_SECRET", original)
		} else {
			os.Unsetenv("JWT_SECRET")
		}
	}()

	secret := getJWTSecret()
	assert.Equal(t, "my-custom-secret", secret)
}

func TestLoginResponse_JSONStructure(t *testing.T) {
	t.Parallel()

	resp := LoginResponse{
		Token: "test-token",
		User: User{
			ID:    "user-123",
			Email: "test@example.com",
			Name:  "Test User",
		},
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "test-token", parsed["token"])
	user := parsed["user"].(map[string]interface{})
	assert.Equal(t, "user-123", user["id"])
	assert.Equal(t, "test@example.com", user["email"])
	assert.Equal(t, "Test User", user["name"])
}

func TestAuthError_JSONStructure(t *testing.T) {
	t.Parallel()

	authErr := AuthError{Message: "Something went wrong"}

	data, err := json.Marshal(authErr)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "Something went wrong", parsed["message"])
}

func TestHandleLogin_ViaRouter(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	body := `{"email":"demo@ottercamp.io","password":"demo123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp LoginResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Token)
}
