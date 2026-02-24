package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/auth"
)

type AuthOptions struct {
	Service auth.Service
	Logger  *slog.Logger
}

func Auth(opts AuthOptions) func(http.Handler) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	localAuthMode := strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_AUTH_MODE")), "local")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if opts.Service == nil {
				api.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "auth service unavailable")
				return
			}

			bearerToken, apiKey := extractCredentials(r)
			if bearerToken == "" && apiKey == "" {
				if localAuthMode && isLoopback(clientIP(r)) {
					login, err := opts.Service.Login(r.Context(), "", "", clientIP(r), r.UserAgent())
					if err != nil {
						status, code, msg := mapAuthError(err)
						api.Error(w, status, code, msg)
						return
					}

					ctx := withSessionPrincipal(r.Context(), login.Session, AuthMethodLocal)
					ctx = WithSessionToken(ctx, login.SessionToken)
					next.ServeHTTP(w, r.WithContext(ctx))
					refreshSessionAsync(opts.Service, logger, login.SessionToken, requestIDFromContext(r.Context()))
					return
				}

				api.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
				return
			}

			if bearerToken != "" {
				sessionInfo, err := opts.Service.ValidateSession(r.Context(), bearerToken)
				if err != nil {
					status, code, msg := mapAuthError(err)
					api.Error(w, status, code, msg)
					return
				}

				ctx := withSessionPrincipal(r.Context(), sessionInfo, AuthMethodSession)
				ctx = WithSessionToken(ctx, bearerToken)
				next.ServeHTTP(w, r.WithContext(ctx))
				refreshSessionAsync(opts.Service, logger, bearerToken, requestIDFromContext(r.Context()))
				return
			}

			keyInfo, err := opts.Service.ValidateAPIKey(r.Context(), apiKey)
			if err != nil {
				status, code, msg := mapAuthError(err)
				api.Error(w, status, code, msg)
				return
			}

			ctx := WithPrincipal(r.Context(), Principal{
				UserID:         keyInfo.UserID,
				OrganizationID: keyInfo.OrganizationID,
				Email:          keyInfo.Email,
				DisplayName:    keyInfo.DisplayName,
				Role:           keyInfo.Role,
				AuthMethod:     AuthMethodAPIKey,
				APIKey:         keyInfo,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func refreshSessionAsync(service auth.Service, logger *slog.Logger, sessionToken, requestID string) {
	sessionToken = strings.TrimSpace(sessionToken)
	if service == nil || sessionToken == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if strings.TrimSpace(requestID) != "" {
			ctx = api.WithRequestID(ctx, requestID)
		}
		if _, err := service.RefreshSession(ctx, sessionToken); err != nil {
			logger.Debug("session refresh failed", "request_id", requestID, "error", err)
		}
	}()
}

func withSessionPrincipal(ctx context.Context, info *auth.SessionInfo, authMethod string) context.Context {
	principal := Principal{AuthMethod: authMethod, Session: info}
	if info != nil {
		principal.UserID = info.UserID
		principal.OrganizationID = info.OrganizationID
		principal.Email = info.Email
		principal.DisplayName = info.DisplayName
		principal.Role = info.Role
	}
	return WithPrincipal(ctx, principal)
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := api.RequestIDFromContext(ctx)
	return requestID
}

func extractCredentials(r *http.Request) (bearerToken string, apiKey string) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		candidate := strings.TrimSpace(authHeader[len("Bearer "):])
		if candidate != "" {
			return candidate, ""
		}
	}

	if candidate := strings.TrimSpace(r.Header.Get("X-API-Key")); candidate != "" {
		return "", candidate
	}
	if candidate := strings.TrimSpace(r.URL.Query().Get("api_key")); candidate != "" {
		return "", candidate
	}
	return "", ""
}

func mapAuthError(err error) (status int, code, message string) {
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		return http.StatusTooManyRequests, api.ErrCodeRateLimited, "too many authentication attempts"
	case errors.Is(err, auth.ErrInvalidCredentials):
		return http.StatusUnauthorized, api.ErrCodeInvalidCredentials, "invalid credentials"
	case errors.Is(err, auth.ErrSessionExpired):
		return http.StatusUnauthorized, api.ErrCodeSessionExpired, "session expired"
	case errors.Is(err, auth.ErrSessionRevoked):
		return http.StatusUnauthorized, api.ErrCodeSessionRevoked, "session revoked"
	case errors.Is(err, auth.ErrInvalidAPIKey):
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "invalid api key"
	case errors.Is(err, auth.ErrInvalidSession):
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "invalid session"
	case errors.Is(err, auth.ErrAccountLocked):
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "account is locked"
	default:
		return http.StatusUnauthorized, api.ErrCodeUnauthorized, "unauthorized"
	}
}

func clientIP(r *http.Request) string {
	if forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		if len(parts) > 0 {
			candidate := strings.TrimSpace(parts[0])
			if candidate != "" {
				return candidate
			}
		}
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return strings.TrimSpace(host)
}

func isLoopback(candidate string) bool {
	parsed := net.ParseIP(strings.TrimSpace(candidate))
	return parsed != nil && parsed.IsLoopback()
}
