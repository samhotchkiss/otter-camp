package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponderJSONEnvelopeUsesContextRequestID(t *testing.T) {
	rr := httptest.NewRecorder()
	responder := NewResponder(WithRequestID(context.Background(), "req-123"))

	responder.JSON(rr, http.StatusOK, map[string]string{"status": "ok"})

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", got, "application/json; charset=utf-8")
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	dataMap, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("data field missing or wrong shape: %+v", payload)
	}
	if got := dataMap["status"]; got != "ok" {
		t.Fatalf("data.status = %v, want %q", got, "ok")
	}

	metaMap, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta field missing or wrong shape: %+v", payload)
	}
	if got := metaMap["request_id"]; got != "req-123" {
		t.Fatalf("meta.request_id = %v, want %q", got, "req-123")
	}
}

func TestResponderErrorEnvelopeShape(t *testing.T) {
	rr := httptest.NewRecorder()
	responder := NewResponder(WithRequestID(context.Background(), "req-456"))

	responder.Error(rr, http.StatusUnauthorized, ErrCodeUnauthorized, "missing credentials")

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
	if got := errorMap["request_id"]; got != "req-456" {
		t.Fatalf("error.request_id = %v, want %q", got, "req-456")
	}
}

func TestAllErrorCodeConstantsEncodeValidJSON(t *testing.T) {
	for _, code := range ErrorCodes {
		t.Run(code, func(t *testing.T) {
			rr := httptest.NewRecorder()
			responder := NewResponder(WithRequestID(context.Background(), "req-constant"))

			responder.Error(rr, http.StatusBadRequest, code, "test")

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
