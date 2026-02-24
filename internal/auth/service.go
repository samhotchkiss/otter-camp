package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	oclock "github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/ratelimit"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	sessionLifetime       = 30 * 24 * time.Hour
	accountLockDuration   = 30 * time.Minute
	magicLinkTTL          = 15 * time.Minute
	minPasswordLength     = 12
	defaultBcryptCost     = 12
	authModeDefault       = "password"
	authModeLocal         = "local"
	localhostIPv4         = "127.0.0.1"
	localhostIPv6         = "::1"
	defaultAuthEnvOrgVar  = "OTTERCAMP_DEFAULT_ORG_ID"
	defaultAuthModeEnvVar = "OTTERCAMP_AUTH_MODE"
)

type HumanUserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	GetByEmail(ctx context.Context, organizationID uuid.UUID, email string) (repo.HumanUser, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
	IncrFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	ResetFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	SetLockedUntil(ctx context.Context, id uuid.UUID, lockedUntil *time.Time) (repo.HumanUser, error)
	Update(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
}

type AuthSessionRepository interface {
	Create(ctx context.Context, session repo.AuthSession) (repo.AuthSession, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (repo.AuthSession, error)
	Revoke(ctx context.Context, id uuid.UUID) error
	RevokeAll(ctx context.Context, userID uuid.UUID) (int64, error)
	TouchLastUsed(ctx context.Context, id uuid.UUID) (repo.AuthSession, error)
	ExtendExpiry(ctx context.Context, id uuid.UUID, expiresAt time.Time) (repo.AuthSession, error)
}

type APIKeyRepository interface {
	Create(ctx context.Context, apiKey repo.APIKey) (repo.APIKey, error)
	GetByKeyHash(ctx context.Context, keyHash string) (repo.APIKey, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]repo.APIKey, error)
	Revoke(ctx context.Context, id uuid.UUID) (repo.APIKey, error)
	TouchLastUsed(ctx context.Context, id uuid.UUID) (repo.APIKey, error)
}

type Options struct {
	Clock        oclock.Clock
	IPLimiter    *ratelimit.IPLimiter
	Audit        AuditRecorder
	DefaultOrgID uuid.UUID
	AuthMode     string
	BcryptCost   int
}

type service struct {
	clock        oclock.Clock
	ipLimiter    *ratelimit.IPLimiter
	audit        AuditRecorder
	defaultOrgID uuid.UUID
	authMode     string
	bcryptCost   int

	humanUsers HumanUserRepository
	sessions   AuthSessionRepository
	apiKeys    APIKeyRepository

	magicLinksMu sync.Mutex
	magicLinks   map[string]magicLinkRecord
}

type magicLinkRecord struct {
	userID    uuid.UUID
	expiresAt time.Time
}

func NewService(humanUsers HumanUserRepository, sessions AuthSessionRepository, apiKeys APIKeyRepository, opts Options) (Service, error) {
	if humanUsers == nil {
		return nil, fmt.Errorf("auth service requires human user repository")
	}
	if sessions == nil {
		return nil, fmt.Errorf("auth service requires session repository")
	}
	if apiKeys == nil {
		return nil, fmt.Errorf("auth service requires api key repository")
	}

	if opts.Clock == nil {
		opts.Clock = oclock.Real{}
	}
	if opts.IPLimiter == nil {
		opts.IPLimiter = ratelimit.NewIPLimiter(opts.Clock, ratelimit.DefaultLoginLimit, ratelimit.DefaultLoginWindow)
	}

	if opts.DefaultOrgID == uuid.Nil {
		defaultOrgRaw := strings.TrimSpace(os.Getenv(defaultAuthEnvOrgVar))
		if defaultOrgRaw != "" {
			orgID, err := uuid.Parse(defaultOrgRaw)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", defaultAuthEnvOrgVar, err)
			}
			opts.DefaultOrgID = orgID
		}
	}

	mode := strings.ToLower(strings.TrimSpace(opts.AuthMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(os.Getenv(defaultAuthModeEnvVar)))
	}
	if mode == "" {
		mode = authModeDefault
	}
	if mode != authModeDefault && mode != authModeLocal {
		return nil, fmt.Errorf("invalid auth mode %q", mode)
	}

	cost := opts.BcryptCost
	if cost <= 0 {
		cost = defaultBcryptCost
	}

	return &service{
		clock:        opts.Clock,
		ipLimiter:    opts.IPLimiter,
		audit:        opts.Audit,
		defaultOrgID: opts.DefaultOrgID,
		authMode:     mode,
		bcryptCost:   cost,
		humanUsers:   humanUsers,
		sessions:     sessions,
		apiKeys:      apiKeys,
		magicLinks:   make(map[string]magicLinkRecord),
	}, nil
}

func (s *service) Login(ctx context.Context, email, password, ipAddr, userAgent string) (*LoginResult, error) {
	orgID, err := s.resolveOrgID(ctx)
	if err != nil {
		return nil, err
	}

	email = strings.TrimSpace(email)
	password = strings.TrimSpace(password)
	ipAddr = strings.TrimSpace(ipAddr)
	userAgent = strings.TrimSpace(userAgent)

	if s.authMode == authModeLocal && email == "" && password == "" && isLoopback(ipAddr) {
		return s.autoLoginLoopback(ctx, orgID, ipAddr, userAgent)
	}

	if s.authMode != authModeLocal && !s.ipLimiter.Allow(ipAddr) {
		return nil, ErrRateLimited
	}

	if email == "" || password == "" {
		return nil, ErrInvalidCredentials
	}

	user, err := s.humanUsers.GetByEmail(ctx, orgID, email)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive {
		return nil, ErrInvalidCredentials
	}

	now := s.clock.Now().UTC()
	if user.FailedLoginAttempts >= 10 && user.LockedUntil != nil && user.LockedUntil.After(now) {
		return nil, ErrAccountLocked
	}

	if err := validatePassword(user.PasswordHash, password); err != nil {
		if lockErr := s.recordFailedAttempt(ctx, user.ID); lockErr != nil {
			return nil, lockErr
		}
		return nil, ErrInvalidCredentials
	}

	if _, err := s.humanUsers.ResetFailedAttempts(ctx, user.ID); err != nil {
		return nil, err
	}

	return s.createSessionForUser(ctx, user, now, ipAddr, userAgent)
}

func (s *service) Logout(ctx context.Context, sessionToken string) error {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return ErrInvalidSession
	}

	session, err := s.getSessionByRawToken(ctx, sessionToken)
	if err != nil {
		return err
	}

	if err := s.sessions.Revoke(ctx, session.ID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrInvalidSession
		}
		return err
	}

	return nil
}

func (s *service) RefreshSession(ctx context.Context, sessionToken string) (*SessionInfo, error) {
	session, err := s.getSessionByRawToken(ctx, strings.TrimSpace(sessionToken))
	if err != nil {
		return nil, err
	}

	now := s.clock.Now().UTC()
	if session.RevokedAt != nil {
		return nil, ErrSessionRevoked
	}
	if !session.ExpiresAt.After(now) {
		return nil, ErrSessionExpired
	}

	extended, err := s.sessions.ExtendExpiry(ctx, session.ID, now.Add(sessionLifetime))
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrInvalidSession
		}
		return nil, err
	}
	session.ExpiresAt = extended.ExpiresAt

	touched, err := s.sessions.TouchLastUsed(ctx, session.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrInvalidSession
		}
		return nil, err
	}
	session.LastUsedAt = touched.LastUsedAt

	return sessionToInfo(session), nil
}

func (s *service) ValidateSession(ctx context.Context, sessionToken string) (*SessionInfo, error) {
	session, err := s.getSessionByRawToken(ctx, strings.TrimSpace(sessionToken))
	if err != nil {
		return nil, err
	}

	now := s.clock.Now().UTC()
	if session.RevokedAt != nil {
		return nil, ErrSessionRevoked
	}
	if !session.ExpiresAt.After(now) {
		return nil, ErrSessionExpired
	}

	return sessionToInfo(session), nil
}

func (s *service) ValidateAPIKey(ctx context.Context, rawKey string) (*APIKeyInfo, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return nil, ErrInvalidAPIKey
	}

	apiKey, err := s.apiKeys.GetByKeyHash(ctx, sha256Hex(rawKey))
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidAPIKey
	}
	if err != nil {
		return nil, err
	}

	now := s.clock.Now().UTC()
	if apiKey.RevokedAt != nil {
		return nil, ErrInvalidAPIKey
	}
	if apiKey.ExpiresAt != nil && !apiKey.ExpiresAt.After(now) {
		return nil, ErrInvalidAPIKey
	}

	touched, err := s.apiKeys.TouchLastUsed(ctx, apiKey.ID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrInvalidAPIKey
		}
		return nil, err
	}
	return apiKeyToInfo(touched), nil
}

func (s *service) IssueAPIKey(ctx context.Context, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*IssueResult, error) {
	if userID == uuid.Nil {
		return nil, ErrForbidden
	}
	if strings.TrimSpace(displayName) == "" {
		return nil, fmt.Errorf("display name is required")
	}

	raw, hash, prefix, err := generateAPIKey()
	if err != nil {
		return nil, err
	}

	created, err := s.apiKeys.Create(ctx, repo.APIKey{
		UserID:      userID,
		KeyHash:     hash,
		KeyPrefix:   prefix,
		DisplayName: strings.TrimSpace(displayName),
		Scopes:      append([]string(nil), scopes...),
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return nil, err
	}

	return &IssueResult{
		RawKey: raw,
		APIKey: apiKeyToInfo(created),
	}, nil
}

func (s *service) RevokeAPIKey(ctx context.Context, keyID, requestingUserID uuid.UUID) error {
	if keyID == uuid.Nil || requestingUserID == uuid.Nil {
		return ErrForbidden
	}

	keys, err := s.apiKeys.ListByUser(ctx, requestingUserID)
	if err != nil {
		return err
	}

	ownsKey := false
	for _, key := range keys {
		if key.ID == keyID {
			ownsKey = true
			break
		}
	}
	if !ownsKey {
		return ErrForbidden
	}

	if _, err := s.apiKeys.Revoke(ctx, keyID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrInvalidAPIKey
		}
		return err
	}
	return nil
}

func (s *service) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]*APIKeyInfo, error) {
	keys, err := s.apiKeys.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	out := make([]*APIKeyInfo, 0, len(keys))
	for _, key := range keys {
		out = append(out, apiKeyToInfo(key))
	}
	return out, nil
}

func (s *service) MagicLink(ctx context.Context, email string) (*MagicLinkResult, error) {
	orgID, err := s.resolveOrgID(ctx)
	if err != nil {
		return nil, err
	}

	email = strings.TrimSpace(email)
	if email == "" {
		return nil, ErrInvalidCredentials
	}

	user, err := s.humanUsers.GetByEmail(ctx, orgID, email)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	token, err := generateMagicToken()
	if err != nil {
		return nil, err
	}

	expiresAt := s.clock.Now().UTC().Add(magicLinkTTL)
	s.magicLinksMu.Lock()
	s.magicLinks[token] = magicLinkRecord{
		userID:    user.ID,
		expiresAt: expiresAt,
	}
	s.magicLinksMu.Unlock()

	return &MagicLinkResult{
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *service) ResetPassword(ctx context.Context, token, newPassword string) error {
	token = strings.TrimSpace(token)
	newPassword = strings.TrimSpace(newPassword)

	if len(newPassword) < minPasswordLength {
		return ErrWeakPassword
	}

	s.magicLinksMu.Lock()
	link, ok := s.magicLinks[token]
	if !ok {
		s.magicLinksMu.Unlock()
		return ErrTokenExpired
	}
	if !link.expiresAt.After(s.clock.Now().UTC()) {
		delete(s.magicLinks, token)
		s.magicLinksMu.Unlock()
		return ErrTokenExpired
	}
	delete(s.magicLinks, token)
	s.magicLinksMu.Unlock()

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	user, err := s.humanUsers.GetByID(ctx, link.userID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrInvalidCredentials
		}
		return err
	}

	hash := string(hashedPassword)
	user.PasswordHash = &hash
	if _, err := s.humanUsers.Update(ctx, user); err != nil {
		return err
	}
	if _, err := s.humanUsers.ResetFailedAttempts(ctx, user.ID); err != nil {
		return err
	}
	if _, err := s.sessions.RevokeAll(ctx, user.ID); err != nil {
		return err
	}

	return nil
}

func (s *service) UnlockAccount(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return ErrForbidden
	}
	_, err := s.humanUsers.ResetFailedAttempts(ctx, userID)
	if errors.Is(err, repo.ErrNotFound) {
		return ErrInvalidCredentials
	}
	return err
}

func (s *service) resolveOrgID(ctx context.Context) (uuid.UUID, error) {
	if orgID, ok := OrgIDFromContext(ctx); ok {
		return orgID, nil
	}
	if s.defaultOrgID != uuid.Nil {
		return s.defaultOrgID, nil
	}
	return uuid.Nil, ErrOrgRequired
}

func (s *service) autoLoginLoopback(ctx context.Context, orgID uuid.UUID, ipAddr, userAgent string) (*LoginResult, error) {
	users, err := s.humanUsers.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	for _, user := range users {
		if user.Role != "admin" || !user.IsActive {
			continue
		}
		return s.createSessionForUser(ctx, user, s.clock.Now().UTC(), ipAddr, userAgent)
	}

	return nil, ErrLocalAuthUnavailable
}

func (s *service) createSessionForUser(ctx context.Context, user repo.HumanUser, now time.Time, ipAddr, userAgent string) (*LoginResult, error) {
	rawToken, tokenHash, err := generateSessionToken()
	if err != nil {
		return nil, err
	}

	session, err := s.sessions.Create(ctx, repo.AuthSession{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: now.Add(sessionLifetime),
		UserAgent: stringPointerOrNil(userAgent),
		IPAddress: stringPointerOrNil(ipAddr),
	})
	if err != nil {
		return nil, err
	}
	session.User = &user

	return &LoginResult{
		SessionToken: rawToken,
		Session:      sessionToInfo(session),
	}, nil
}

func (s *service) recordFailedAttempt(ctx context.Context, userID uuid.UUID) error {
	updated, err := s.humanUsers.IncrFailedAttempts(ctx, userID)
	if err != nil {
		return err
	}
	if updated.FailedLoginAttempts < 10 {
		return nil
	}

	lockedUntil := s.clock.Now().UTC().Add(accountLockDuration)
	_, err = s.humanUsers.SetLockedUntil(ctx, userID, &lockedUntil)
	return err
}

func (s *service) getSessionByRawToken(ctx context.Context, rawToken string) (repo.AuthSession, error) {
	if rawToken == "" {
		return repo.AuthSession{}, ErrInvalidSession
	}

	session, err := s.sessions.GetByTokenHash(ctx, sha256Hex(rawToken))
	if errors.Is(err, repo.ErrNotFound) {
		return repo.AuthSession{}, ErrInvalidSession
	}
	if err != nil {
		return repo.AuthSession{}, err
	}
	return session, nil
}

func validatePassword(passwordHash *string, password string) error {
	if passwordHash == nil || strings.TrimSpace(*passwordHash) == "" {
		return ErrInvalidCredentials
	}
	return bcrypt.CompareHashAndPassword([]byte(*passwordHash), []byte(password))
}

func sessionToInfo(session repo.AuthSession) *SessionInfo {
	info := &SessionInfo{
		SessionID:  session.ID,
		UserID:     session.UserID,
		ExpiresAt:  session.ExpiresAt,
		LastUsedAt: session.LastUsedAt,
		RevokedAt:  session.RevokedAt,
	}
	if session.User != nil {
		info.OrganizationID = session.User.OrganizationID
		info.Email = session.User.Email
		info.Role = session.User.Role
	}
	return info
}

func apiKeyToInfo(apiKey repo.APIKey) *APIKeyInfo {
	return &APIKeyInfo{
		ID:          apiKey.ID,
		UserID:      apiKey.UserID,
		KeyPrefix:   apiKey.KeyPrefix,
		DisplayName: apiKey.DisplayName,
		Scopes:      append([]string(nil), apiKey.Scopes...),
		CreatedAt:   apiKey.CreatedAt,
		LastUsedAt:  apiKey.LastUsedAt,
		ExpiresAt:   apiKey.ExpiresAt,
		RevokedAt:   apiKey.RevokedAt,
	}
}

func stringPointerOrNil(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isLoopback(rawIP string) bool {
	rawIP = strings.TrimSpace(rawIP)
	if rawIP == "" {
		return false
	}
	if rawIP == localhostIPv4 || rawIP == localhostIPv6 {
		return true
	}

	parsed := net.ParseIP(rawIP)
	return parsed != nil && parsed.IsLoopback()
}
