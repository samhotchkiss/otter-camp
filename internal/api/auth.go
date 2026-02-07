package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

const (
	openClawTokenPrefix = "oc_auth_"
	defaultAuthTTL      = 10 * time.Minute
	defaultSessionTTL   = 7 * 24 * time.Hour
	clockSkewAllowance  = 2 * time.Minute
)

type AuthRequestStart struct {
	OrgID string `json:"org_id"`
}

type AuthRequestResponse struct {
	RequestID     string              `json:"request_id"`
	State         string              `json:"state"`
	ExpiresAt     time.Time           `json:"expires_at"`
	ExchangeURL   string              `json:"exchange_url"`
	OpenClawEvent OpenClawAuthRequest `json:"openclaw_request"`
}

type OpenClawAuthRequest struct {
	RequestID   string    `json:"request_id"`
	State       string    `json:"state"`
	OrgID       string    `json:"org_id"`
	CallbackURL string    `json:"callback_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type AuthExchangeRequest struct {
	RequestID string `json:"request_id"`
	Token     string `json:"token"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type openClawClaims struct {
	Sub string `json:"sub"`
	Iss string `json:"iss"`
	Exp int64  `json:"exp"`
	Iat int64  `json:"iat"`
}

var (
	authDB     *sql.DB
	authDBErr  error
	authDBOnce sync.Once
)

// HandleLogin creates a pending OpenClaw auth request.
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req AuthRequestStart
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	orgID := strings.TrimSpace(req.OrgID)
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	state, err := generateRandomToken(24)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create auth request"})
		return
	}

	expiresAt := time.Now().UTC().Add(authRequestTTL())
	requestID, err := insertAuthRequest(r.Context(), db, orgID, state, expiresAt, requestIP(r), r.UserAgent())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create auth request"})
		return
	}

	exchangeURL := "/api/auth/exchange?request_id=" + requestID + "&token="
	callbackURL := strings.TrimSuffix(getPublicBaseURL(r), "/") + exchangeURL

	resp := AuthRequestResponse{
		RequestID:   requestID,
		State:       state,
		ExpiresAt:   expiresAt,
		ExchangeURL: exchangeURL,
		OpenClawEvent: OpenClawAuthRequest{
			RequestID:   requestID,
			State:       state,
			OrgID:       orgID,
			CallbackURL: callbackURL,
			ExpiresAt:   expiresAt,
		},
	}

	sendJSON(w, http.StatusOK, resp)
}

// HandleAuthExchange validates an OpenClaw token and creates a session.
func HandleAuthExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var req AuthExchangeRequest
	if r.Method == http.MethodGet {
		req.RequestID = strings.TrimSpace(r.URL.Query().Get("request_id"))
		req.Token = strings.TrimSpace(r.URL.Query().Get("token"))
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}

	req.RequestID = strings.TrimSpace(req.RequestID)
	req.Token = strings.TrimSpace(req.Token)

	if req.RequestID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing request_id"})
		return
	}
	if !uuidRegex.MatchString(req.RequestID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request_id"})
		return
	}
	if req.Token == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing token"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	authReq, err := getAuthRequest(r.Context(), db, req.RequestID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "auth request not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load auth request"})
		return
	}

	if authReq.Status != "pending" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "auth request not pending"})
		return
	}

	if time.Now().UTC().After(authReq.ExpiresAt) {
		_ = markAuthRequestExpired(r.Context(), db, authReq.ID)
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "auth request expired"})
		return
	}

	claims, err := validateOpenClawToken(req.Token)
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
		return
	}

	userID, displayName, email, err := upsertAuthUser(r.Context(), db, authReq.OrgID, claims)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create user"})
		return
	}

	sessionToken, sessionExpiry, err := createSession(r.Context(), db, authReq.OrgID, userID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create session"})
		return
	}

	if err := markAuthRequestCompleted(r.Context(), db, authReq.ID); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to finalize auth request"})
		return
	}

	name := displayName
	if strings.TrimSpace(name) == "" {
		name = "OpenClaw User"
	}

	resp := LoginResponse{
		Token: sessionToken,
		User: User{
			ID:    userID,
			Email: email,
			Name:  name,
		},
	}

	w.Header().Set("X-Session-Expires-At", sessionExpiry.UTC().Format(time.RFC3339))
	sendJSON(w, http.StatusOK, resp)
}

type authRequestRecord struct {
	ID        string
	OrgID     string
	Status    string
	ExpiresAt time.Time
}

func insertAuthRequest(ctx context.Context, db *sql.DB, orgID, state string, expiresAt time.Time, ip, userAgent string) (string, error) {
	var id string
	err := db.QueryRowContext(
		ctx,
		`INSERT INTO auth_requests (org_id, state, expires_at, request_ip, user_agent)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		orgID,
		state,
		expiresAt,
		ip,
		userAgent,
	).Scan(&id)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23503" {
			return "", sql.ErrNoRows
		}
		return "", err
	}
	return id, nil
}

func getAuthRequest(ctx context.Context, db *sql.DB, id string) (authRequestRecord, error) {
	var rec authRequestRecord
	err := db.QueryRowContext(
		ctx,
		`SELECT id, org_id, status, expires_at FROM auth_requests WHERE id = $1`,
		id,
	).Scan(&rec.ID, &rec.OrgID, &rec.Status, &rec.ExpiresAt)
	return rec, err
}

func markAuthRequestExpired(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(
		ctx,
		`UPDATE auth_requests SET status = 'expired', updated_at = NOW() WHERE id = $1`,
		id,
	)
	return err
}

func markAuthRequestCompleted(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(
		ctx,
		`UPDATE auth_requests SET status = 'completed', updated_at = NOW(), completed_at = NOW() WHERE id = $1`,
		id,
	)
	return err
}

func upsertAuthUser(ctx context.Context, db *sql.DB, orgID string, claims openClawClaims) (string, string, string, error) {
	displayName := strings.TrimSpace(claims.Sub)
	var id string
	var name sql.NullString
	var email sql.NullString
	err := db.QueryRowContext(
		ctx,
		`INSERT INTO users (org_id, subject, issuer, display_name)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (org_id, issuer, subject)
		 DO UPDATE SET updated_at = NOW()
		 RETURNING id, display_name, email`,
		orgID,
		claims.Sub,
		claims.Iss,
		displayName,
	).Scan(&id, &name, &email)
	if err != nil {
		return "", "", "", err
	}

	return id, name.String, email.String, nil
}

func createSession(ctx context.Context, db *sql.DB, orgID, userID string) (string, time.Time, error) {
	token, err := generateRandomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	sessionToken := "oc_sess_" + token
	expiresAt := time.Now().UTC().Add(sessionTTL())

	_, err = db.ExecContext(
		ctx,
		`INSERT INTO sessions (org_id, user_id, token, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		orgID,
		userID,
		sessionToken,
		expiresAt,
	)
	if err != nil {
		return "", time.Time{}, err
	}
	return sessionToken, expiresAt, nil
}

func validateOpenClawToken(token string) (openClawClaims, error) {
	var claims openClawClaims
	secret := openClawAuthSecret()
	if secret == "" {
		return claims, errors.New("auth secret not configured")
	}

	if !strings.HasPrefix(token, openClawTokenPrefix) {
		return claims, errors.New("invalid token prefix")
	}

	raw := strings.TrimPrefix(token, openClawTokenPrefix)
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return claims, errors.New("invalid token format")
	}

	payloadB64 := parts[0]
	signatureB64 := parts[1]

	payload, err := decodeBase64(payloadB64)
	if err != nil {
		return claims, errors.New("invalid token payload")
	}

	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, errors.New("invalid token payload")
	}

	if strings.TrimSpace(claims.Sub) == "" {
		return claims, errors.New("token missing subject")
	}

	issuer := openClawAuthIssuer()
	if claims.Iss != issuer {
		return claims, errors.New("invalid token issuer")
	}

	expectedSig := computeOpenClawAuthSignature(payloadB64, secret)
	providedSig, err := decodeBase64(signatureB64)
	if err != nil || !hmac.Equal(expectedSig, providedSig) {
		return claims, errors.New("invalid token signature")
	}

	now := time.Now().UTC().Unix()
	if claims.Exp == 0 || now > claims.Exp+int64(clockSkewAllowance.Seconds()) {
		return claims, errors.New("token expired")
	}
	if claims.Iat == 0 || claims.Iat > now+int64(clockSkewAllowance.Seconds()) {
		return claims, errors.New("invalid token issued time")
	}

	return claims, nil
}

func computeOpenClawAuthSignature(payloadB64, secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payloadB64))
	return mac.Sum(nil)
}

func decodeBase64(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("empty value")
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return nil, errors.New("invalid base64")
}

func generateRandomToken(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("invalid length")
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func getAuthDB() (*sql.DB, error) {
	authDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			authDBErr = errors.New("DATABASE_URL is not set")
			return
		}
		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			authDBErr = err
			return
		}
		if err := db.Ping(); err != nil {
			_ = db.Close()
			authDBErr = err
			return
		}
		authDB = db
	})
	return authDB, authDBErr
}

func openClawAuthSecret() string {
	return strings.TrimSpace(os.Getenv("OPENCLAW_AUTH_SECRET"))
}

func openClawAuthIssuer() string {
	if issuer := strings.TrimSpace(os.Getenv("OPENCLAW_AUTH_ISSUER")); issuer != "" {
		return issuer
	}
	return "openclaw"
}

func authRequestTTL() time.Duration {
	if value := strings.TrimSpace(os.Getenv("OPENCLAW_AUTH_TTL")); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d > 0 {
			return d
		}
	}
	return defaultAuthTTL
}

func sessionTTL() time.Duration {
	if value := strings.TrimSpace(os.Getenv("OPENCLAW_SESSION_TTL")); value != "" {
		if d, err := time.ParseDuration(value); err == nil && d > 0 {
			return d
		}
	}
	return defaultSessionTTL
}

func getPublicBaseURL(r *http.Request) string {
	if base := strings.TrimSpace(os.Getenv("OTTER_PUBLIC_BASE_URL")); base != "" {
		return base
	}
	if r != nil && r.Host != "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		return scheme + "://" + r.Host
	}
	return "http://localhost:3000"
}

func requestIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// MagicLinkResponse is the response for generating a magic login link
type MagicLinkResponse struct {
	URL       string    `json:"url"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// HandleMagicLink generates a simple auth token for MVP testing.
// This bypasses the full OpenClaw auth flow.
// Usage: POST /api/auth/magic with optional {"name": "Sam", "email": "sam@example.com"}
func HandleMagicLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	// Parse optional user info
	var req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // Ignore errors, use defaults

	if req.Name == "" {
		req.Name = "Sam"
	}
	if req.Email == "" {
		req.Email = "sam@otter.camp"
	}

	// Generate a simple token
	token, err := generateRandomToken(32)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to generate token"})
		return
	}
	authToken := "oc_magic_" + token
	expiresAt := time.Now().UTC().Add(sessionTTL())

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	// Create a demo org if needed
	var orgID string
	if err := db.QueryRowContext(
		r.Context(),
		`INSERT INTO organizations (name, slug) VALUES ('Demo Org', 'demo') 
		 ON CONFLICT (slug) DO UPDATE SET name = 'Demo Org' 
		 RETURNING id`,
	).Scan(&orgID); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create demo org"})
		return
	}

	// Create a demo user
	var userID string
	if err := db.QueryRowContext(
		r.Context(),
		`INSERT INTO users (org_id, display_name, email, subject, issuer) 
		 VALUES ($1, $2, $3, 'magic', 'otter.camp')
		 ON CONFLICT (org_id, issuer, subject) DO UPDATE SET display_name = $2, email = $3
		 RETURNING id`,
		orgID,
		req.Name,
		req.Email,
	).Scan(&userID); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create demo user"})
		return
	}

	// Create session
	if _, err := db.ExecContext(
		r.Context(),
		`INSERT INTO sessions (org_id, user_id, token, expires_at) VALUES ($1, $2, $3, $4)`,
		orgID,
		userID,
		authToken,
		expiresAt,
	); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create session"})
		return
	}

	// Build the magic link URL
	baseURL := getPublicBaseURL(r)
	// Use sam.otter.camp if we detect we're in production
	if strings.Contains(baseURL, "api.otter.camp") {
		baseURL = "https://sam.otter.camp"
	}
	magicURL := baseURL + "/?auth=" + authToken

	sendJSON(w, http.StatusOK, MagicLinkResponse{
		URL:       magicURL,
		Token:     authToken,
		ExpiresAt: expiresAt,
	})
}

// HandleValidateToken validates a magic link token and sets a session cookie.
// GET /api/auth/validate?token=xxx
func HandleValidateToken(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing token"})
		return
	}

	// Optional unsafe mode for local-only development.
	if strings.HasPrefix(token, "oc_magic_") && allowInsecureMagicTokenValidation() {
		// Set the auth cookie
		http.SetCookie(w, &http.Cookie{
			Name:     "otter_auth",
			Value:    token,
			Path:     "/",
			MaxAge:   int(sessionTTL().Seconds()),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		})

		sendJSON(w, http.StatusOK, map[string]interface{}{
			"valid": true,
			"user": User{
				ID:    "demo-user",
				Name:  "Sam",
				Email: "sam@otter.camp",
			},
		})
		return
	}

	// Try to validate against DB
	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid token"})
		return
	}

	var userID, userName, userEmail string
	var expiresAt time.Time
	err = db.QueryRowContext(r.Context(),
		`SELECT s.user_id, s.expires_at, u.display_name, u.email 
		 FROM sessions s 
		 JOIN users u ON s.user_id = u.id 
		 WHERE s.token = $1`,
		token).Scan(&userID, &expiresAt, &userName, &userEmail)

	if err != nil || time.Now().After(expiresAt) {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid or expired token"})
		return
	}

	// Set the auth cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "otter_auth",
		Value:    token,
		Path:     "/",
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"valid": true,
		"user": User{
			ID:    userID,
			Name:  userName,
			Email: userEmail,
		},
	})
}

func allowInsecureMagicTokenValidation() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("OTTERCAMP_ALLOW_INSECURE_MAGIC_AUTH")))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_INSECURE_MAGIC_TOKEN_VALIDATION")))
	}
	return value == "1" || value == "true" || value == "yes"
}
