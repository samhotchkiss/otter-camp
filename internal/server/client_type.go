package server

import "strings"

func detectClientType(userAgent string) string {
	normalized := strings.ToLower(strings.TrimSpace(userAgent))
	switch {
	case strings.HasPrefix(normalized, "ottercamp-ios/"), strings.HasPrefix(normalized, "ottercamp-android/"):
		return "mobile"
	case strings.HasPrefix(normalized, "ottercamp-tui/"):
		return "tui"
	case strings.HasPrefix(normalized, "mozilla/"):
		return "web"
	default:
		return "api"
	}
}

func isMobileUserAgent(userAgent string) bool {
	return detectClientType(userAgent) == "mobile"
}
