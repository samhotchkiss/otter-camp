package assignmentrole

import "strings"

// Normalize returns the canonical project assignment role, or empty when invalid.
func Normalize(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pm", "project_manager":
		return "project_manager"
	case "worker", "reviewer", "observer":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func IsProjectManager(value string) bool {
	return Normalize(value) == "project_manager"
}
