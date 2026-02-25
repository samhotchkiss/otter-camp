package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInputValidatorValidateRequest(t *testing.T) {
	validator := NewInputValidator()

	t.Run("body over 10mb", func(t *testing.T) {
		payload := strings.Repeat("a", int(DefaultMaxRequestBodyBytes)+1)
		req := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")

		err := validator.ValidateRequest(req)
		validationErr, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("error type = %T, want *ValidationError", err)
		}
		if validationErr.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", validationErr.StatusCode, http.StatusRequestEntityTooLarge)
		}
	})

	t.Run("null byte in json string", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(`{"value":"bad\u0000value"}`))
		req.Header.Set("Content-Type", "application/json")

		err := validator.ValidateRequest(req)
		if err == nil {
			t.Fatal("expected validation error")
		}
	})

	t.Run("clean request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(`{"value":"hello world"}`))
		req.Header.Set("Content-Type", "application/json")
		if err := validator.ValidateRequest(req); err != nil {
			t.Fatalf("ValidateRequest: %v", err)
		}
	})
}
