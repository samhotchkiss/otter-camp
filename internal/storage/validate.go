package storage

import (
	"regexp"
	"strings"
)

var validKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9/_.-]+$`)

func validateKey(key string) error {
	key = strings.TrimSpace(key)
	switch {
	case key == "":
		return &InvalidKeyError{Key: key, Reason: "key is empty"}
	case strings.HasPrefix(key, "/"):
		return &InvalidKeyError{Key: key, Reason: "absolute paths are not allowed"}
	case strings.Contains(key, ".."):
		return &InvalidKeyError{Key: key, Reason: "path traversal is not allowed"}
	case !validKeyPattern.MatchString(key):
		return &InvalidKeyError{Key: key, Reason: "contains unsupported characters"}
	default:
		return nil
	}
}

func validatePrefix(prefix string) error {
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	return validateKey(prefix)
}
