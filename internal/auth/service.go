package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/ratelimit"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"golang.org/x/crypto/bcrypt"
)

const (
	authModeStandard = "standard"
	authModeLocal    = "local"
)

const (
	sessionTTLDefault   = 30 * 24 * time.Hour
	accountLockDuration = 30 * time.Minute
	magicLinkTTLDefault = 15 * time.Minute
	maxFailedAttempts   = 10
)

const (
	apiKeyPrefix   = "otk_"
	apiKeyBodySize = 40
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var (
	ErrInvalidCredentials       = errors.New("invalid credentials")
	ErrAccountLocked            = errors.New("account is locked")
	ErrRateLimited              = errors.New("rate limited")
	ErrSessionExpired           = errors.New("session expired")
	ErrSessionRevoked           = errors.New("session revoked")
	ErrInvalidSession           = errors.New("invalid session")
	ErrInvalidAPIKey            = errors.New("invalid api key")
	ErrForbidden                = errors.New("forbidden")
	ErrTokenExpired             = errors.New("token expired")
	ErrNoDefaultOrganization    = errors.New("default organization is not configured")
	ErrNoAdminForLocalAutoLogin = errors.New("no admin user available for local auto-login")
)

type Service interface {
	Login(ctx context.Context, email, password, ipAddr, userAgent string) (*LoginResult, error)
	Logout(ctx context.Context, sessionToken string) error
	RefreshSession(ctx context.Context, sessionToken string) (*SessionInfo, error)
	ValidateSession(ctx context.Context, sessionToken string) (*SessionInfo, error)
	ValidateAPIKey(ctx context.Context, rawKey string) (*APIKeyInfo, error)
	IssueAPIKey(ctx context.Context, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*IssueResult, error)
	RevokeAPIKey(ctx context.Context, keyID, requestingUserID uuid.UUID, requestingRole string, requestingOrgID uuid.UUID) error
	ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]*APIKeyInfo, error)
	MagicLink(ctx context.Context, email string) (*MagicLinkResult, error)
	ResetPassword(ctx context.Context, token, newPassword string) error
	UnlockAccount(ctx context.Context, userID uuid.UUID) error
}

type AuditRecorder = audit.AuditRecorder

type LoginResult struct {
	SessionToken string
	Session      *SessionInfo
}

type SessionInfo struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	Email          string
	DisplayName    string
	Role           string
	ExpiresAt      time.Time
	LastUsedAt     time.Time
	RevokedAt      *time.Time
	UserAgent      *string
	IPAddress      *string
}

type APIKeyInfo struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	Email          string
	DisplayName    string
	Role           string
	KeyPrefix      string
	Scopes         []string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
	ExpiresAt      *time.Time
	RevokedAt      *time.Time
}

type IssueResult struct {
	KeyID       uuid.UUID
	RawKey      string
	KeyPrefix   string
	DisplayName string
	CreatedAt   time.Time
}

type MagicLinkResult struct {
	Token     string
	ExpiresAt time.Time
}

type Options struct {
	Users    UserRepository
	Sessions SessionRepository
	APIKeys  APIKeyRepository

	Clock        clock.Clock
	IPLimiter    *ratelimit.Limiter
	RandomReader io.Reader
	BcryptCost   int

	AuthMode     string
	DefaultOrgID uuid.UUID

	SessionTTL   time.Duration
	MagicLinkTTL time.Duration

	MaxFailedLoginAttempts int
	AuditRecorder          AuditRecorder
}

type UserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	GetByEmail(ctx context.Context, organizationID uuid.UUID, email string) (repo.HumanUser, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
	Update(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
	IncrFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	ResetFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	SetLockedUntil(ctx context.Context, id uuid.UUID, lockedUntil *time.Time) (repo.HumanUser, error)
}

type SessionRepository interface {
	Create(ctx context.Context, session repo.AuthSession) (repo.AuthSession, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (repo.AuthSession, error)
	Revoke(ctx context.Context, id uuid.UUID) error
	TouchLastUsed(ctx context.Context, id uuid.UUID) (repo.AuthSession, error)
	ExtendExpiry(ctx context.Context, id uuid.UUID, expiresAt time.Time) (repo.AuthSession, error)
}

type APIKeyRepository interface {
	Create(ctx context.Context, apiKey repo.APIKey) (repo.APIKey, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.APIKey, error)
	GetByKeyHash(ctx context.Context, keyHash string) (repo.APIKey, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]repo.APIKey, error)
	Revoke(ctx context.Context, id uuid.UUID) (repo.APIKey, error)
	TouchLastUsed(ctx context.Context, id uuid.UUID) (repo.APIKey, error)
}

type service struct {
	users    UserRepository
	sessions SessionRepository
	apiKeys  APIKeyRepository

	clock      clock.Clock
	ipLimiter  *ratelimit.Limiter
	random     io.Reader
	bcryptCost int

	authMode   string
	defaultOrg uuid.UUID

	sessionTTL   time.Duration
	magicLinkTTL time.Duration
	maxFailed    int

	audit AuditRecorder

	magicLinksMu sync.Mutex
	magicLinks   map[string]magicLinkToken
}

type magicLinkToken struct {
	orgID     uuid.UUID
	email     string
	expiresAt time.Time
	used      bool
}

func NewService(opts Options) (Service, error) {
	if opts.Users == nil || opts.Sessions == nil || opts.APIKeys == nil {
		return nil, fmt.Errorf("auth service requires users, sessions, and api_keys repositories")
	}

	clk := opts.Clock
	if clk == nil {
		clk = clock.Real{}
	}

	authMode := strings.ToLower(strings.TrimSpace(opts.AuthMode))
	if authMode == "" {
		authMode = strings.ToLower(strings.TrimSpace(os.Getenv("OTTERCAMP_AUTH_MODE")))
	}
	if authMode == "" {
		authMode = authModeStandard
	}
	if authMode != authModeStandard && authMode != authModeLocal {
		return nil, fmt.Errorf("invalid auth mode %q", authMode)
	}

	defaultOrg := opts.DefaultOrgID
	if defaultOrg == uuid.Nil {
		if raw := strings.TrimSpace(os.Getenv("OTTERCAMP_DEFAULT_ORG_ID")); raw != "" {
			parsed, err := uuid.Parse(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid OTTERCAMP_DEFAULT_ORG_ID: %w", err)
			}
			defaultOrg = parsed
		}
	}

	sessionTTL := opts.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = sessionTTLDefault
	}

	magicLinkTTL := opts.MagicLinkTTL
	if magicLinkTTL <= 0 {
		magicLinkTTL = magicLinkTTLDefault
	}

	randomReader := opts.RandomReader
	if randomReader == nil {
		randomReader = rand.Reader
	}

	bcryptCost := opts.BcryptCost
	if bcryptCost == 0 {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")), "test") {
			bcryptCost = bcrypt.MinCost
		} else {
			bcryptCost = 12
		}
	}

	limiter := opts.IPLimiter
	if limiter == nil {
		limiter = ratelimit.New(20, 15*time.Minute, clk)
	}

	maxFailed := opts.MaxFailedLoginAttempts
	if maxFailed <= 0 {
		maxFailed = maxFailedAttempts
	}

	recorder := opts.AuditRecorder
	if recorder == nil {
		recorder = audit.NewNoopRecorder()
	}

	return &service{
		users:        opts.Users,
		sessions:     opts.Sessions,
		apiKeys:      opts.APIKeys,
		clock:        clk,
		ipLimiter:    limiter,
		random:       randomReader,
		bcryptCost:   bcryptCost,
		authMode:     authMode,
		defaultOrg:   defaultOrg,
		sessionTTL:   sessionTTL,
		magicLinkTTL: magicLinkTTL,
		maxFailed:    maxFailed,
		audit:        recorder,
		magicLinks:   make(map[string]magicLinkToken),
	}, nil
}

func (s *service) Login(ctx context.Context, email, password, ipAddr, userAgent string) (*LoginResult, error) {
	orgID, err := s.resolveOrganizationID(ctx)
	if err != nil {
		return nil, err
	}

	if s.authMode == authModeLocal && isLoopbackIP(ipAddr) && strings.TrimSpace(email) == "" && strings.TrimSpace(password) == "" {
		return s.localAutoLogin(ctx, orgID, ipAddr, userAgent)
	}

	if s.authMode != authModeLocal && !s.ipLimiter.Allow(ipAddr) {
		return nil, ErrRateLimited
	}

	user, err := s.users.GetByEmail(ctx, orgID, strings.TrimSpace(email))
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive {
		return nil, ErrInvalidCredentials
	}

	now := s.clock.Now()
	if user.LockedUntil != nil && user.FailedLoginAttempts >= s.maxFailed && user.LockedUntil.After(now) {
		return nil, ErrAccountLocked
	}

	if user.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)) != nil {
		updated, incrErr := s.users.IncrFailedAttempts(ctx, user.ID)
		if incrErr == nil && updated.FailedLoginAttempts >= s.maxFailed {
			lockUntil := now.Add(accountLockDuration)
			_, _ = s.users.SetLockedUntil(ctx, user.ID, &lockUntil)
			if updated.FailedLoginAttempts == s.maxFailed {
				s.recordLockoutAudit(ctx, user, ipAddr, lockUntil)
			}
		}
		return nil, ErrInvalidCredentials
	}

	if _, err := s.users.ResetFailedAttempts(ctx, user.ID); err != nil {
		return nil, err
	}

	return s.createSession(ctx, user, ipAddr, userAgent)
}

func (s *service) Logout(ctx context.Context, sessionToken string) error {
	tokenHash := hashToken(strings.TrimSpace(sessionToken))
	if tokenHash == "" {
		return ErrInvalidSession
	}

	session, err := s.sessions.GetByTokenHash(ctx, tokenHash)
	if errors.Is(err, repo.ErrNotFound) {
		return ErrInvalidSession
	}
	if err != nil {
		return err
	}

	return s.sessions.Revoke(ctx, session.ID)
}

func (s *service) RefreshSession(ctx context.Context, sessionToken string) (*SessionInfo, error) {
	validated, err := s.validateSession(ctx, sessionToken)
	if err != nil {
		return nil, err
	}

	expiresAt := s.clock.Now().Add(s.sessionTTL)
	extended, err := s.sessions.ExtendExpiry(ctx, validated.ID, expiresAt)
	if err != nil {
		return nil, err
	}
	touched, err := s.sessions.TouchLastUsed(ctx, validated.ID)
	if err != nil {
		return nil, err
	}

	validated.ExpiresAt = extended.ExpiresAt
	validated.LastUsedAt = touched.LastUsedAt
	return validated, nil
}

// RefreshSessionToken extends the existing session and issues a new session token.
// The original session remains valid until its own expiry/revocation window.
func (s *service) RefreshSessionToken(ctx context.Context, sessionToken, ipAddr, userAgent string) (*LoginResult, error) {
	session, err := s.RefreshSession(ctx, sessionToken)
	if err != nil {
		return nil, err
	}

	user, err := s.users.GetByID(ctx, session.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive {
		return nil, ErrInvalidSession
	}

	return s.createSession(ctx, user, ipAddr, userAgent)
}

func (s *service) ValidateSession(ctx context.Context, sessionToken string) (*SessionInfo, error) {
	return s.validateSession(ctx, sessionToken)
}

func (s *service) validateSession(ctx context.Context, sessionToken string) (*SessionInfo, error) {
	tokenHash := hashToken(strings.TrimSpace(sessionToken))
	if tokenHash == "" {
		return nil, ErrInvalidSession
	}

	session, err := s.sessions.GetByTokenHash(ctx, tokenHash)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidSession
	}
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	if session.RevokedAt != nil {
		return nil, ErrSessionRevoked
	}
	if !session.ExpiresAt.After(now) {
		return nil, ErrSessionExpired
	}

	if session.User == nil {
		return nil, ErrInvalidSession
	}

	return sessionToInfo(session), nil
}

func (s *service) ValidateAPIKey(ctx context.Context, rawKey string) (*APIKeyInfo, error) {
	keyHash := hashToken(strings.TrimSpace(rawKey))
	if keyHash == "" {
		return nil, ErrInvalidAPIKey
	}

	found, err := s.apiKeys.GetByKeyHash(ctx, keyHash)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidAPIKey
	}
	if err != nil {
		return nil, err
	}

	now := s.clock.Now()
	if found.RevokedAt != nil {
		return nil, ErrInvalidAPIKey
	}
	if found.ExpiresAt != nil && !found.ExpiresAt.After(now) {
		return nil, ErrInvalidAPIKey
	}

	touched, err := s.apiKeys.TouchLastUsed(ctx, found.ID)
	if err != nil {
		return nil, err
	}

	owner, err := s.users.GetByID(ctx, touched.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidAPIKey
	}
	if err != nil {
		return nil, err
	}
	if !owner.IsActive {
		return nil, ErrInvalidAPIKey
	}

	info := apiKeyToInfo(touched)
	info.OrganizationID = owner.OrganizationID
	info.Email = owner.Email
	info.DisplayName = owner.DisplayName
	info.Role = owner.Role
	return info, nil
}

func (s *service) IssueAPIKey(ctx context.Context, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*IssueResult, error) {
	rawKey, err := s.generateAPIKey()
	if err != nil {
		return nil, err
	}

	keyHash := hashToken(rawKey)
	keyPrefix := rawKey
	if len(keyPrefix) > 8 {
		keyPrefix = keyPrefix[:8]
	}

	created, err := s.apiKeys.Create(ctx, repo.APIKey{
		UserID:      userID,
		KeyHash:     keyHash,
		KeyPrefix:   keyPrefix,
		DisplayName: strings.TrimSpace(displayName),
		Scopes:      append([]string{}, scopes...),
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return nil, err
	}

	return &IssueResult{
		KeyID:       created.ID,
		RawKey:      rawKey,
		KeyPrefix:   created.KeyPrefix,
		DisplayName: created.DisplayName,
		CreatedAt:   created.CreatedAt,
	}, nil
}

func (s *service) RevokeAPIKey(ctx context.Context, keyID, requestingUserID uuid.UUID, requestingRole string, requestingOrgID uuid.UUID) error {
	key, err := s.apiKeys.GetByID(ctx, keyID)
	if errors.Is(err, repo.ErrNotFound) {
		return ErrInvalidAPIKey
	}
	if err != nil {
		return err
	}

	if key.UserID != requestingUserID {
		if !strings.EqualFold(strings.TrimSpace(requestingRole), "admin") {
			return ErrForbidden
		}

		owner, ownerErr := s.users.GetByID(ctx, key.UserID)
		if errors.Is(ownerErr, repo.ErrNotFound) {
			return ErrInvalidAPIKey
		}
		if ownerErr != nil {
			return ownerErr
		}
		if owner.OrganizationID != requestingOrgID {
			return ErrForbidden
		}
	}
	_, err = s.apiKeys.Revoke(ctx, keyID)
	return err
}

func (s *service) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]*APIKeyInfo, error) {
	found, err := s.apiKeys.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	items := make([]*APIKeyInfo, 0, len(found))
	for _, item := range found {
		items = append(items, apiKeyToInfo(item))
	}
	return items, nil
}

func (s *service) MagicLink(ctx context.Context, email string) (*MagicLinkResult, error) {
	orgID, err := s.resolveOrganizationID(ctx)
	if err != nil {
		return nil, err
	}

	normalizedEmail := strings.TrimSpace(email)
	if normalizedEmail == "" {
		return nil, ErrInvalidCredentials
	}

	if _, err := s.users.GetByEmail(ctx, orgID, normalizedEmail); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	token, err := s.generateToken(24)
	if err != nil {
		return nil, err
	}

	expiresAt := s.clock.Now().Add(s.magicLinkTTL)
	s.magicLinksMu.Lock()
	s.magicLinks[token] = magicLinkToken{
		orgID:     orgID,
		email:     normalizedEmail,
		expiresAt: expiresAt,
	}
	s.magicLinksMu.Unlock()

	return &MagicLinkResult{
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *service) ConsumeMagicLink(ctx context.Context, token, ipAddr, userAgent string) (*LoginResult, error) {
	normalizedToken := strings.TrimSpace(token)
	if normalizedToken == "" {
		return nil, ErrTokenExpired
	}

	now := s.clock.Now()
	s.magicLinksMu.Lock()
	record, ok := s.magicLinks[normalizedToken]
	if !ok || record.used || !record.expiresAt.After(now) {
		s.magicLinksMu.Unlock()
		return nil, ErrTokenExpired
	}
	record.used = true
	s.magicLinks[normalizedToken] = record
	s.magicLinksMu.Unlock()

	user, err := s.users.GetByEmail(ctx, record.orgID, record.email)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive {
		return nil, ErrInvalidCredentials
	}

	return s.createSession(ctx, user, ipAddr, userAgent)
}

func (s *service) ResetPassword(ctx context.Context, token, newPassword string) error {
	normalizedToken := strings.TrimSpace(token)
	normalizedPassword := strings.TrimSpace(newPassword)
	if normalizedToken == "" || normalizedPassword == "" {
		return ErrTokenExpired
	}

	now := s.clock.Now()
	s.magicLinksMu.Lock()
	record, ok := s.magicLinks[normalizedToken]
	if !ok || record.used || !record.expiresAt.After(now) {
		s.magicLinksMu.Unlock()
		return ErrTokenExpired
	}
	record.used = true
	s.magicLinks[normalizedToken] = record
	s.magicLinksMu.Unlock()

	user, err := s.users.GetByEmail(ctx, record.orgID, record.email)
	if err != nil {
		return err
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(normalizedPassword), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	passwordHash := string(hashed)

	_, err = s.users.Update(ctx, repo.HumanUser{
		ID:                  user.ID,
		Email:               user.Email,
		DisplayName:         user.DisplayName,
		PasswordHash:        &passwordHash,
		Role:                user.Role,
		IsActive:            user.IsActive,
		Settings:            user.Settings,
		LastLoginAt:         user.LastLoginAt,
		LockedUntil:         user.LockedUntil,
		FailedLoginAttempts: user.FailedLoginAttempts,
	})
	return err
}

func (s *service) UnlockAccount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.users.ResetFailedAttempts(ctx, userID)
	return err
}

func (s *service) localAutoLogin(ctx context.Context, orgID uuid.UUID, ipAddr, userAgent string) (*LoginResult, error) {
	users, err := s.users.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	for _, user := range users {
		if user.Role == "admin" && user.IsActive {
			return s.createSession(ctx, user, ipAddr, userAgent)
		}
	}
	return nil, ErrNoAdminForLocalAutoLogin
}

func (s *service) recordLockoutAudit(ctx context.Context, user repo.HumanUser, ipAddr string, lockedUntil time.Time) {
	if s.audit == nil {
		return
	}

	targetType := "human_user"
	targetID := user.ID
	event := audit.Event{
		OrgID:         user.OrganizationID,
		EventType:     "login_failed_lockout",
		PrincipalType: "human",
		PrincipalID:   user.ID,
		TargetType:    &targetType,
		TargetID:      &targetID,
		Metadata: map[string]any{
			"email":        user.Email,
			"ip_address":   strings.TrimSpace(ipAddr),
			"locked_until": lockedUntil.UTC().Format(time.RFC3339),
		},
	}
	s.audit.RecordAsync(ctx, event)
}

func (s *service) createSession(ctx context.Context, user repo.HumanUser, ipAddr, userAgent string) (*LoginResult, error) {
	rawToken, err := s.generateToken(32)
	if err != nil {
		return nil, err
	}
	tokenHash := hashToken(rawToken)

	expiresAt := s.clock.Now().Add(s.sessionTTL)
	session := repo.AuthSession{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}
	if trimmed := strings.TrimSpace(ipAddr); trimmed != "" {
		session.IPAddress = &trimmed
	}
	if trimmed := strings.TrimSpace(userAgent); trimmed != "" {
		session.UserAgent = &trimmed
	}

	created, err := s.sessions.Create(ctx, session)
	if err != nil {
		return nil, err
	}

	created.User = &user
	return &LoginResult{
		SessionToken: rawToken,
		Session:      sessionToInfo(created),
	}, nil
}

func (s *service) resolveOrganizationID(ctx context.Context) (uuid.UUID, error) {
	if orgID, ok := OrganizationIDFromContext(ctx); ok {
		return orgID, nil
	}
	if s.defaultOrg != uuid.Nil {
		return s.defaultOrg, nil
	}
	return uuid.Nil, ErrNoDefaultOrganization
}

func (s *service) generateToken(numBytes int) (string, error) {
	tokenBytes := make([]byte, numBytes)
	if _, err := io.ReadFull(s.random, tokenBytes); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(tokenBytes), nil
}

func (s *service) generateAPIKey() (string, error) {
	rawBytes := make([]byte, apiKeyBodySize)
	if _, err := io.ReadFull(s.random, rawBytes); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}

	chars := make([]byte, apiKeyBodySize)
	for i, b := range rawBytes {
		chars[i] = base58Alphabet[int(b)%len(base58Alphabet)]
	}
	return apiKeyPrefix + string(chars), nil
}

func hashToken(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

func sessionToInfo(session repo.AuthSession) *SessionInfo {
	info := &SessionInfo{
		ID:         session.ID,
		UserID:     session.UserID,
		ExpiresAt:  session.ExpiresAt,
		LastUsedAt: session.LastUsedAt,
		RevokedAt:  session.RevokedAt,
		UserAgent:  session.UserAgent,
		IPAddress:  session.IPAddress,
	}
	if session.User != nil {
		info.OrganizationID = session.User.OrganizationID
		info.Email = session.User.Email
		info.DisplayName = session.User.DisplayName
		info.Role = session.User.Role
	}
	return info
}

func apiKeyToInfo(key repo.APIKey) *APIKeyInfo {
	return &APIKeyInfo{
		ID:          key.ID,
		UserID:      key.UserID,
		KeyPrefix:   key.KeyPrefix,
		DisplayName: key.DisplayName,
		Scopes:      append([]string{}, key.Scopes...),
		CreatedAt:   key.CreatedAt,
		LastUsedAt:  key.LastUsedAt,
		ExpiresAt:   key.ExpiresAt,
		RevokedAt:   key.RevokedAt,
	}
}

type orgContextKey struct{}

func WithOrganizationID(ctx context.Context, organizationID uuid.UUID) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, orgContextKey{}, organizationID)
}

func OrganizationIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	if ctx == nil {
		return uuid.Nil, false
	}
	value, ok := ctx.Value(orgContextKey{}).(uuid.UUID)
	if !ok || value == uuid.Nil {
		return uuid.Nil, false
	}
	return value, true
}

func isLoopbackIP(ipAddr string) bool {
	candidate := strings.TrimSpace(ipAddr)
	if candidate == "" {
		return false
	}

	if host, _, err := net.SplitHostPort(candidate); err == nil {
		candidate = host
	}

	parsed := net.ParseIP(candidate)
	return parsed != nil && parsed.IsLoopback()
}
