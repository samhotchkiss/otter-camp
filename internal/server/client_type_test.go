package server

import "testing"

func TestDetectClientType(t *testing.T) {
	cases := []struct {
		name      string
		userAgent string
		want      string
	}{
		{name: "ios", userAgent: "OtterCamp-iOS/2.1", want: "mobile"},
		{name: "android", userAgent: "OtterCamp-Android/1.0", want: "mobile"},
		{name: "tui", userAgent: "OtterCamp-TUI/0.8", want: "tui"},
		{name: "web", userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X)", want: "web"},
		{name: "empty", userAgent: "", want: "api"},
		{name: "other", userAgent: "curl/8.0.1", want: "api"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectClientType(tc.userAgent); got != tc.want {
				t.Fatalf("detectClientType(%q) = %q, want %q", tc.userAgent, got, tc.want)
			}
		})
	}
}
