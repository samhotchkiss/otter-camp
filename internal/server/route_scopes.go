package server

import (
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

func requireReadScope(resource string) func(http.Handler) http.Handler {
	return middleware.RequireAnyScope(append(append(scopeAliases("read", resource), scopeAliases("write", resource)...), append(scopeAliases("admin", resource), "admin:*")...)...)
}

func requireWriteScope(resource string) func(http.Handler) http.Handler {
	return middleware.RequireAnyScope(append(append(scopeAliases("write", resource), scopeAliases("admin", resource)...), "admin:*")...)
}

func requireAdminScope(resource string) func(http.Handler) http.Handler {
	return middleware.RequireAnyScope(append(scopeAliases("admin", resource), "admin:*")...)
}

func scopeAliases(action, resource string) []string {
	action = strings.ToLower(strings.TrimSpace(action))
	resource = strings.ToLower(strings.TrimSpace(resource))
	if action == "" || resource == "" {
		return nil
	}
	return []string{
		action + ":" + resource,
		resource + ":" + action,
	}
}
