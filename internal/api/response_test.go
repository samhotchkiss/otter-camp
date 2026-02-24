package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorEnvelopeShape(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set(HeaderRequestID, "req-123")

	Error(rr, http.StatusUnauthorized, ErrCodeUnauthorized, "missing credentials")

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	errorMap, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing or wrong shape: %+v", payload)
	}
	if got := errorMap["code"]; got != ErrCodeUnauthorized {
		t.Fatalf("error.code = %v, want %q", got, ErrCodeUnauthorized)
	}
	if got := errorMap["message"]; got != "missing credentials" {
		t.Fatalf("error.message = %v, want %q", got, "missing credentials")
	}

	metaMap, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta field missing or wrong shape: %+v", payload)
	}
	if got := metaMap["request_id"]; got != "req-123" {
		t.Fatalf("meta.request_id = %v, want %q", got, "req-123")
	}
}

func TestAllErrorCodeConstantsEncodeValidJSON(t *testing.T) {
	for _, code := range ErrorCodes {
		t.Run(code, func(t *testing.T) {
			rr := httptest.NewRecorder()
			rr.Header().Set(HeaderRequestID, "req-constant")

			Error(rr, http.StatusBadRequest, code, "test")

			var payload map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			errorMap, ok := payload["error"].(map[string]any)
			if !ok {
				t.Fatalf("error field missing or wrong shape: %+v", payload)
			}
			if got := errorMap["code"]; got != code {
				t.Fatalf("error.code = %v, want %q", got, code)
			}
		})
	}
}
