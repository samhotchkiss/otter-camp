package storage

import (
	"regexp"
	"strings"
)

var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9/_.-]+$`)

func validateKey(key string) error {
	if key == "" {
		return &InvalidKeyError{Key: key, Reason: "empty key"}
	}
	if strings.HasPrefix(key, "/") {
		return &InvalidKeyError{Key: key, Reason: "absolute paths are not allowed"}
	}
	if strings.HasSuffix(key, "/") {
		return &InvalidKeyError{Key: key, Reason: "key must not end with '/'"}
	}
	if strings.Contains(key, "..") {
		return &InvalidKeyError{Key: key, Reason: "must not contain '..'"}
	}
	if !keyPattern.MatchString(key) {
		return &InvalidKeyError{Key: key, Reason: "contains unsupported characters"}
	}
	return nil
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, "/") {
		return &InvalidKeyError{Key: prefix, Reason: "absolute paths are not allowed"}
	}
	if strings.Contains(prefix, "..") {
		return &InvalidKeyError{Key: prefix, Reason: "must not contain '..'"}
	}
	if !keyPattern.MatchString(prefix) {
		return &InvalidKeyError{Key: prefix, Reason: "contains unsupported characters"}
	}
	return nil
}
