package memory

import "strings"

func TrustTierCapForSource(sourceType, authorType string) float64 {
	switch strings.TrimSpace(sourceType) {
	case "explicit":
		return 1.0
	case "chat_message":
		if strings.TrimSpace(authorType) == "agent" {
			return 0.8
		}
		return 0.9
	case "memory_import":
		return 0.6
	case "event", "file":
		return 0.7
	default:
		return 0.8
	}
}

func ApplyTrustTierCap(extractedConfidence, cap float64) float64 {
	conf := clampFloat(extractedConfidence, 0, 1)
	cap = clampFloat(cap, 0, 1)
	if cap == 0 {
		return conf
	}
	if conf > cap {
		return cap
	}
	return conf
}
