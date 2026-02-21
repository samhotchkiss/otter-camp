package store

import (
	"fmt"
	"strings"
)

const (
	ellieSensitivityNormal    = "normal"
	ellieSensitivitySensitive = "sensitive"
)

var validEllieSensitivities = map[string]struct{}{
	ellieSensitivityNormal:    {},
	ellieSensitivitySensitive: {},
}

func normalizeEllieSensitivity(raw string) (string, error) {
	sensitivity := strings.TrimSpace(strings.ToLower(raw))
	if sensitivity == "" {
		return ellieSensitivityNormal, nil
	}
	if _, ok := validEllieSensitivities[sensitivity]; !ok {
		return "", fmt.Errorf("invalid sensitivity")
	}
	return sensitivity, nil
}
