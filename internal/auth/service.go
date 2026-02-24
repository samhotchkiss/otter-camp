package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/ratelimit"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	defaultSessionTTL   = 30 * 24 * time.Hour
	defaultLockDuration = 30 * time.Minute
	defaultMagicLinkTTL = 15 * time.Minute
	defaultBcryptCost   = 12
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountLocked      = errors.New("account is locked")
	ErrRateLimited        = errors.New("rate limited")
	ErrSessionExpired     = errors.New("session expired")
	ErrSessionRevoked     = errors.New("session revoked")
	ErrInvalidSession     = errors.New("invalid session")
	ErrInvalidAPIKey      = errors.New("invalid api key")
	ErrTokenExpired       = errors.New("token expired")
	ErrOrgIDRequired      = errors.New("organization id is required")
	ErrEmailRequired      = errors.New("email is required")
	ErrPasswordRequired   = errors.New("password is required")
)

var base58Alphabet = []byte("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz")

type Service interface {
	Login(ctx context.Context, email, password, ipAddr, userAgent string) (*LoginResult, error)
	Logout(ctx context.Context, sessionToken string) error
	RefreshSession(ctx context.Context, sessionToken string) (*SessionInfo, error)
	ValidateSession(ctx context.Context, sessionToken string) (*SessionInfo, error)
	ValidateAPIKey(ctx context.Context, rawKey string) (*APIKeyInfo, error)
	IssueAPIKey(ctx context.Context, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*IssueResult, error)
	RevokeAPIKey(ctx context.Context, keyID, requestingUserID uuid.UUID) error
	ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]*APIKeyInfo, error)
	MagicLink(ctx context.Context, email string) (*MagicLinkResult, error)
	ResetPassword(ctx context.Context, token, newPassword string) error
	UnlockAccount(ctx context.Context, userID uuid.UUID) error
}

type LoginResult struct {
	SessionToken string
	Session      *SessionInfo
}

type SessionInfo struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	OrganizationID uuid.UUID
	ExpiresAt      time.Time
	CreatedAt      time.Time
	LastUsedAt     time.Time
}

type APIKeyInfo struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	KeyPrefix   string
	DisplayName string
	Scopes      []string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	ExpiresAt   *time.Time
	RevokedAt   *time.Time
}

type IssueResult struct {
	RawKey string
	APIKey *APIKeyInfo
}

type MagicLinkResult struct {
	Token     string
	ExpiresAt time.Time
}

type AuditRecorder interface {
	RecordAuthEvent(ctx context.Context, event string, userID uuid.UUID) error
}

type HumanUserRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	GetByEmail(ctx context.Context, organizationID uuid.UUID, email string) (repo.HumanUser, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error)
	Update(ctx context.Context, user repo.HumanUser) (repo.HumanUser, error)
	IncrFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	ResetFailedAttempts(ctx context.Context, id uuid.UUID) (repo.HumanUser, error)
	SetLockedUntil(ctx context.Context, id uuid.UUID, lockedUntil *time.Time) (repo.HumanUser, error)
}

type AuthSessionRepository interface {
	Create(ctx context.Context, session repo.AuthSession) (repo.AuthSession, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (repo.AuthSession, error)
	Revoke(ctx context.Context, id uuid.UUID) error
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

type Config struct {
	DefaultOrgID  uuid.UUID
	AuthMode      string
	SessionTTL    time.Duration
	LockDuration  time.Duration
	MagicLinkTTL  time.Duration
	BcryptCost    int
	IPRateLimiter *ratelimit.Limiter
}

type service struct {
	userRepo    HumanUserRepository
	sessionRepo AuthSessionRepository
	apiKeyRepo  APIKeyRepository
	clock       clock.Clock
	recorder    AuditRecorder

	defaultOrgID uuid.UUID
	authMode     string
	sessionTTL   time.Duration
	lockDuration time.Duration
	magicLinkTTL time.Duration
	bcryptCost   int
	ipLimiter    *ratelimit.Limiter

	magicMu     sync.Mutex
	magicTokens map[string]magicLinkToken
}

type magicLinkToken struct {
	userID    uuid.UUID
	expiresAt time.Time
	used      bool
}

func NewService(
	userRepo HumanUserRepository,
	sessionRepo AuthSessionRepository,
	apiKeyRepo APIKeyRepository,
	clk clock.Clock,
	cfg Config,
	recorder AuditRecorder,
) (Service, error) {
	if userRepo == nil || sessionRepo == nil || apiKeyRepo == nil {
		return nil, fmt.Errorf("auth service requires non-nil repositories")
	}
	if clk == nil {
		clk = clock.Real{}
	}

	sessionTTL := cfg.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = defaultSessionTTL
	}
	lockDuration := cfg.LockDuration
	if lockDuration <= 0 {
		lockDuration = defaultLockDuration
	}
	magicLinkTTL := cfg.MagicLinkTTL
	if magicLinkTTL <= 0 {
		magicLinkTTL = defaultMagicLinkTTL
	}
	bcryptCost := cfg.BcryptCost
	if bcryptCost <= 0 {
		bcryptCost = defaultBcryptCost
		if strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")), "test") {
			bcryptCost = bcrypt.MinCost
		}
	}

	ipLimiter := cfg.IPRateLimiter
	if ipLimiter == nil {
		ipLimiter = ratelimit.NewWithClock(20, 15*time.Minute, clk)
	}

	return &service{
		userRepo:     userRepo,
		sessionRepo:  sessionRepo,
		apiKeyRepo:   apiKeyRepo,
		clock:        clk,
		recorder:     recorder,
		defaultOrgID: cfg.DefaultOrgID,
		authMode:     strings.ToLower(strings.TrimSpace(cfg.AuthMode)),
		sessionTTL:   sessionTTL,
		lockDuration: lockDuration,
		magicLinkTTL: magicLinkTTL,
		bcryptCost:   bcryptCost,
		ipLimiter:    ipLimiter,
		magicTokens:  map[string]magicLinkToken{},
	}, nil
}

func (s *service) Login(ctx context.Context, email, password, ipAddr, userAgent string) (*LoginResult, error) {
	orgID, err := s.orgIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if s.isLocalAuthMode() && isLoopback(ipAddr) && strings.TrimSpace(email) == "" && strings.TrimSpace(password) == "" {
		return s.localAutoLogin(ctx, orgID, ipAddr, userAgent)
	}

	if strings.TrimSpace(email) == "" || strings.TrimSpace(password) == "" {
		return nil, ErrInvalidCredentials
	}

	if !s.isLocalAuthMode() && !s.ipLimiter.Allow(strings.TrimSpace(ipAddr)) {
		return nil, ErrRateLimited
	}

	user, err := s.userRepo.GetByEmail(ctx, orgID, strings.TrimSpace(email))
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive || user.PasswordHash == nil {
		return nil, ErrInvalidCredentials
	}

	now := s.clock.Now().UTC()
	if user.FailedLoginAttempts >= 10 && user.LockedUntil != nil && user.LockedUntil.After(now) {
		return nil, ErrAccountLocked
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)); err != nil {
		updated, upErr := s.userRepo.IncrFailedAttempts(ctx, user.ID)
		if upErr != nil {
			return nil, upErr
		}
		if updated.FailedLoginAttempts >= 10 {
			lockedUntil := now.Add(s.lockDuration)
			if _, lockErr := s.userRepo.SetLockedUntil(ctx, user.ID, &lockedUntil); lockErr != nil {
				return nil, lockErr
			}
		}
		return nil, ErrInvalidCredentials
	}

	if _, err := s.userRepo.ResetFailedAttempts(ctx, user.ID); err != nil {
		return nil, err
	}

	token, err := newSessionToken()
	if err != nil {
		return nil, err
	}

	tokenHash := hashSHA256(token)
	expiresAt := now.Add(s.sessionTTL)
	created, err := s.sessionRepo.Create(ctx, repo.AuthSession{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
		UserAgent: pointerFromString(userAgent),
		IPAddress: pointerFromString(ipAddr),
	})
	if err != nil {
		return nil, err
	}

	return &LoginResult{
		SessionToken: token,
		Session:      toSessionInfo(created, orgID),
	}, nil
}

func (s *service) Logout(ctx context.Context, sessionToken string) error {
	tokenHash := hashSHA256(strings.TrimSpace(sessionToken))
	found, err := s.sessionRepo.GetByTokenHash(ctx, tokenHash)
	if errors.Is(err, repo.ErrNotFound) {
		return ErrInvalidSession
	}
	if err != nil {
		return err
	}
	return s.sessionRepo.Revoke(ctx, found.ID)
}

func (s *service) RefreshSession(ctx context.Context, sessionToken string) (*SessionInfo, error) {
	session, err := s.validateSessionToken(ctx, sessionToken)
	if err != nil {
		return nil, err
	}

	newExpiresAt := s.clock.Now().UTC().Add(s.sessionTTL)
	updated, err := s.sessionRepo.ExtendExpiry(ctx, session.ID, newExpiresAt)
	if err != nil {
		return nil, err
	}
	touched, err := s.sessionRepo.TouchLastUsed(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	updated.LastUsedAt = touched.LastUsedAt

	return toSessionInfo(updated, session.User.OrganizationID), nil
}

func (s *service) ValidateSession(ctx context.Context, sessionToken string) (*SessionInfo, error) {
	session, err := s.validateSessionToken(ctx, sessionToken)
	if err != nil {
		return nil, err
	}
	return toSessionInfo(session, session.User.OrganizationID), nil
}

func (s *service) ValidateAPIKey(ctx context.Context, rawKey string) (*APIKeyInfo, error) {
	if strings.TrimSpace(rawKey) == "" {
		return nil, ErrInvalidAPIKey
	}
	keyHash := hashSHA256(rawKey)
	found, err := s.apiKeyRepo.GetByKeyHash(ctx, keyHash)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidAPIKey
	}
	if err != nil {
		return nil, err
	}

	now := s.clock.Now().UTC()
	if found.RevokedAt != nil {
		return nil, ErrInvalidAPIKey
	}
	if found.ExpiresAt != nil && !found.ExpiresAt.After(now) {
		return nil, ErrInvalidAPIKey
	}

	touched, err := s.apiKeyRepo.TouchLastUsed(ctx, found.ID)
	if err != nil {
		return nil, err
	}
	return toAPIKeyInfo(touched), nil
}

func (s *service) IssueAPIKey(ctx context.Context, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*IssueResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("user id is required")
	}

	rawKey, err := newAPIKey()
	if err != nil {
		return nil, err
	}
	keyHash := hashSHA256(rawKey)
	prefix := rawKey
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}

	created, err := s.apiKeyRepo.Create(ctx, repo.APIKey{
		UserID:      userID,
		KeyHash:     keyHash,
		KeyPrefix:   prefix,
		DisplayName: strings.TrimSpace(displayName),
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return nil, err
	}

	return &IssueResult{
		RawKey: rawKey,
		APIKey: toAPIKeyInfo(created),
	}, nil
}

func (s *service) RevokeAPIKey(ctx context.Context, keyID, requestingUserID uuid.UUID) error {
	keys, err := s.apiKeyRepo.ListByUser(ctx, requestingUserID)
	if err != nil {
		return err
	}

	owned := false
	for _, key := range keys {
		if key.ID == keyID {
			owned = true
			break
		}
	}
	if !owned {
		return ErrInvalidAPIKey
	}

	if _, err := s.apiKeyRepo.Revoke(ctx, keyID); errors.Is(err, repo.ErrNotFound) {
		return ErrInvalidAPIKey
	} else {
		return err
	}
}

func (s *service) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]*APIKeyInfo, error) {
	keys, err := s.apiKeyRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	infos := make([]*APIKeyInfo, 0, len(keys))
	for _, key := range keys {
		infos = append(infos, toAPIKeyInfo(key))
	}
	return infos, nil
}

func (s *service) MagicLink(ctx context.Context, email string) (*MagicLinkResult, error) {
	if strings.TrimSpace(email) == "" {
		return nil, ErrEmailRequired
	}

	orgID, err := s.orgIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	user, err := s.userRepo.GetByEmail(ctx, orgID, strings.TrimSpace(email))
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	token, err := newMagicLinkToken()
	if err != nil {
		return nil, err
	}

	expiresAt := s.clock.Now().UTC().Add(s.magicLinkTTL)
	s.magicMu.Lock()
	s.magicTokens[token] = magicLinkToken{
		userID:    user.ID,
		expiresAt: expiresAt,
		used:      false,
	}
	s.magicMu.Unlock()

	return &MagicLinkResult{
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if strings.TrimSpace(newPassword) == "" {
		return ErrPasswordRequired
	}

	now := s.clock.Now().UTC()
	entry, err := s.consumeMagicToken(strings.TrimSpace(token), now)
	if err != nil {
		return err
	}

	hashBytes, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.bcryptCost)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	hash := string(hashBytes)

	user, err := s.userRepo.GetByID(ctx, entry.userID)
	if err != nil {
		return err
	}
	user.PasswordHash = &hash
	if _, err := s.userRepo.Update(ctx, user); err != nil {
		return err
	}

	_, err = s.userRepo.ResetFailedAttempts(ctx, entry.userID)
	return err
}

func (s *service) UnlockAccount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.userRepo.ResetFailedAttempts(ctx, userID)
	return err
}

func (s *service) validateSessionToken(ctx context.Context, sessionToken string) (repo.AuthSession, error) {
	tokenHash := hashSHA256(strings.TrimSpace(sessionToken))
	found, err := s.sessionRepo.GetByTokenHash(ctx, tokenHash)
	if errors.Is(err, repo.ErrNotFound) {
		return repo.AuthSession{}, ErrInvalidSession
	}
	if err != nil {
		return repo.AuthSession{}, err
	}

	now := s.clock.Now().UTC()
	if found.RevokedAt != nil {
		return repo.AuthSession{}, ErrSessionRevoked
	}
	if !found.ExpiresAt.After(now) {
		return repo.AuthSession{}, ErrSessionExpired
	}
	return found, nil
}

func (s *service) localAutoLogin(ctx context.Context, orgID uuid.UUID, ipAddr, userAgent string) (*LoginResult, error) {
	users, err := s.userRepo.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	var admin *repo.HumanUser
	for i := range users {
		if users[i].Role == "admin" && users[i].IsActive {
			admin = &users[i]
			break
		}
	}
	if admin == nil {
		return nil, ErrInvalidCredentials
	}

	token, err := newSessionToken()
	if err != nil {
		return nil, err
	}
	tokenHash := hashSHA256(token)
	now := s.clock.Now().UTC()
	created, err := s.sessionRepo.Create(ctx, repo.AuthSession{
		UserID:    admin.ID,
		TokenHash: tokenHash,
		ExpiresAt: now.Add(s.sessionTTL),
		UserAgent: pointerFromString(userAgent),
		IPAddress: pointerFromString(ipAddr),
	})
	if err != nil {
		return nil, err
	}

	return &LoginResult{
		SessionToken: token,
		Session:      toSessionInfo(created, orgID),
	}, nil
}

func (s *service) consumeMagicToken(token string, now time.Time) (magicLinkToken, error) {
	s.magicMu.Lock()
	defer s.magicMu.Unlock()

	entry, ok := s.magicTokens[token]
	if !ok {
		return magicLinkToken{}, ErrTokenExpired
	}
	if entry.used || !entry.expiresAt.After(now) {
		delete(s.magicTokens, token)
		return magicLinkToken{}, ErrTokenExpired
	}

	entry.used = true
	s.magicTokens[token] = entry
	return entry, nil
}

func (s *service) orgIDFromContext(ctx context.Context) (uuid.UUID, error) {
	if orgID, ok := OrgIDFromContext(ctx); ok {
		return orgID, nil
	}
	if s.defaultOrgID != uuid.Nil {
		return s.defaultOrgID, nil
	}
	return uuid.Nil, ErrOrgIDRequired
}

func (s *service) isLocalAuthMode() bool {
	return s.authMode == "local"
}

func toSessionInfo(session repo.AuthSession, organizationID uuid.UUID) *SessionInfo {
	return &SessionInfo{
		ID:             session.ID,
		UserID:         session.UserID,
		OrganizationID: organizationID,
		ExpiresAt:      session.ExpiresAt,
		CreatedAt:      session.CreatedAt,
		LastUsedAt:     session.LastUsedAt,
	}
}

func toAPIKeyInfo(apiKey repo.APIKey) *APIKeyInfo {
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

func hashSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newSessionToken() (string, error) {
	bytes, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func newAPIKey() (string, error) {
	randomPart, err := randomBase58(40)
	if err != nil {
		return "", err
	}
	return "otk_" + randomPart, nil
}

func newMagicLinkToken() (string, error) {
	randomPart, err := randomBase58(24)
	if err != nil {
		return "", err
	}
	return "mlk_" + randomPart, nil
}

func randomBytes(size int) ([]byte, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}
	return buffer, nil
}

func randomBase58(length int) (string, error) {
	if length <= 0 {
		return "", nil
	}

	out := make([]byte, length)
	max := byte((256 / len(base58Alphabet)) * len(base58Alphabet))
	for i := 0; i < length; i++ {
		for {
			b, err := randomBytes(1)
			if err != nil {
				return "", err
			}
			if b[0] >= max {
				continue
			}
			out[i] = base58Alphabet[int(b[0])%len(base58Alphabet)]
			break
		}
	}
	return string(out), nil
}

func pointerFromString(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isLoopback(ipAddr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipAddr))
	return ip != nil && ip.IsLoopback()
}
