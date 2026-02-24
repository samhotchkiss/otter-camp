package storage

import (
	"errors"
	"testing"
)

func TestValidateKeyRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "absolute path", key: "/etc/passwd"},
		{name: "path traversal", key: "../../etc/passwd"},
		{name: "invalid characters", key: "orgs/acme/chat/file?.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateKey(tt.key)
			if err == nil {
				t.Fatalf("validateKey(%q) returned nil, want error", tt.key)
			}

			var keyErr *InvalidKeyError
			if !errors.As(err, &keyErr) {
				t.Fatalf("error = %T, want *InvalidKeyError", err)
			}
		})
	}
}

func TestValidateKeyAcceptsValidInput(t *testing.T) {
	if err := validateKey("orgs/acme/chat/session-1/file.txt"); err != nil {
		t.Fatalf("validateKey(valid) returned error: %v", err)
	}
}
