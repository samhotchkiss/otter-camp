package ottercli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunReleaseGateReturnsPayloadOnFailureStatus(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody = nil
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{
			"ok": false,
			"checks": [
				{"category":"migration","status":"fail","message":"legacy import report not found"}
			]
		}`))
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	payload, status, err := client.RunReleaseGate()
	if err == nil {
		t.Fatalf("RunReleaseGate() expected error on non-2xx status")
	}
	if status != http.StatusPreconditionFailed {
		t.Fatalf("RunReleaseGate() status = %d, want %d", status, http.StatusPreconditionFailed)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/admin/config/release-gate" {
		t.Fatalf("RunReleaseGate request = %s %s", gotMethod, gotPath)
	}
	if gotBody["confirm"] != true {
		t.Fatalf("RunReleaseGate payload confirm = %v, want true", gotBody["confirm"])
	}
	if payload["ok"] != false {
		t.Fatalf("RunReleaseGate payload ok = %v, want false", payload["ok"])
	}
}

func TestRunReleaseGateSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"checks": [
				{"category":"migration","status":"pass","message":"ok"}
			]
		}`))
	}))
	defer srv.Close()

	client := &Client{
		BaseURL: srv.URL,
		Token:   "token-1",
		OrgID:   "org-1",
		HTTP:    srv.Client(),
	}

	payload, status, err := client.RunReleaseGate()
	if err != nil {
		t.Fatalf("RunReleaseGate() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("RunReleaseGate() status = %d, want %d", status, http.StatusOK)
	}
	if payload["ok"] != true {
		t.Fatalf("RunReleaseGate payload ok = %v, want true", payload["ok"])
	}
}
