package auth

import "errors"

var (
	ErrOrgRequired          = errors.New("auth: organization id required")
	ErrInvalidCredentials   = errors.New("auth: invalid credentials")
	ErrAccountLocked        = errors.New("auth: account locked")
	ErrRateLimited          = errors.New("auth: rate limited")
	ErrInvalidSession       = errors.New("auth: invalid session token")
	ErrSessionExpired       = errors.New("auth: session expired")
	ErrSessionRevoked       = errors.New("auth: session revoked")
	ErrInvalidAPIKey        = errors.New("auth: invalid api key")
	ErrForbidden            = errors.New("auth: forbidden")
	ErrTokenExpired         = errors.New("auth: token expired")
	ErrWeakPassword         = errors.New("auth: password must be at least 12 characters")
	ErrLocalAuthUnavailable = errors.New("auth: local auto-login unavailable")
)
