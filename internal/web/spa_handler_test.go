package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShouldServeSPAFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{name: "root", method: http.MethodGet, path: "/", want: true},
		{name: "spa path", method: http.MethodGet, path: "/dashboard/projects", want: true},
		{name: "head request", method: http.MethodHead, path: "/dashboard/projects", want: true},
		{name: "api path", method: http.MethodGet, path: "/v1/projects", want: false},
		{name: "assets path", method: http.MethodGet, path: "/assets/main.js", want: false},
		{name: "favicon path", method: http.MethodGet, path: "/favicon.ico", want: false},
		{name: "metrics path", method: http.MethodGet, path: "/metrics", want: false},
		{name: "health path", method: http.MethodGet, path: "/health/live", want: false},
		{name: "test path", method: http.MethodGet, path: "/test/reset", want: false},
		{name: "non-read method", method: http.MethodPost, path: "/dashboard/projects", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if got := ShouldServeSPAFallback(req); got != tc.want {
				t.Fatalf("ShouldServeSPAFallback(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}
