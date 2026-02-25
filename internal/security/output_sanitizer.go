package security

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

type OutputSanitizer struct {
	scrubber *SecretScrubber
}

func NewOutputSanitizer(scrubber *SecretScrubber) *OutputSanitizer {
	if scrubber == nil {
		scrubber = NewSecretScrubber()
	}
	return &OutputSanitizer{scrubber: scrubber}
}

func (s *OutputSanitizer) SanitizeResponse(body []byte) []byte {
	if s == nil || len(body) == 0 {
		return body
	}
	scrubbed := []byte(s.scrubber.Scrub(string(body)))

	var envelope map[string]any
	if err := json.Unmarshal(scrubbed, &envelope); err != nil {
		return scrubbed
	}
	errorObj, ok := envelope["error"].(map[string]any)
	if !ok {
		return scrubbed
	}
	if details, exists := errorObj["details"]; exists {
		switch typed := details.(type) {
		case string:
			lower := strings.ToLower(typed)
			if strings.Contains(lower, "stack") || strings.Contains(lower, "trace") {
				delete(errorObj, "details")
			}
		case map[string]any:
			if _, hasStack := typed["stack"]; hasStack {
				delete(errorObj, "details")
			}
		}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return scrubbed
	}
	return encoded
}

func OutputSanitizerMiddleware(sanitizer *OutputSanitizer) func(http.Handler) http.Handler {
	if sanitizer == nil {
		sanitizer = NewOutputSanitizer(nil)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isStreamingRequest(r) {
				next.ServeHTTP(w, r)
				return
			}

			capture := newResponseCapture()
			next.ServeHTTP(capture, r)

			for key, values := range capture.header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}

			body := capture.body.Bytes()
			contentType := strings.ToLower(strings.TrimSpace(capture.header.Get("Content-Type")))
			if strings.Contains(contentType, "json") || json.Valid(body) {
				body = sanitizer.SanitizeResponse(body)
			}
			w.Header().Set("Content-Length", strconvItoa(len(body)))

			status := capture.status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			_, _ = w.Write(body)
		})
	}
}

func isStreamingRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	path := strings.TrimSpace(r.URL.Path)
	if strings.HasPrefix(path, "/v1/realtime") || strings.Contains(path, "/events") {
		return true
	}
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	if strings.Contains(accept, "text/event-stream") {
		return true
	}
	upgrade := strings.ToLower(strings.TrimSpace(r.Header.Get("Upgrade")))
	if strings.Contains(upgrade, "websocket") {
		return true
	}
	return strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key")) != ""
}

type responseCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newResponseCapture() *responseCapture {
	return &responseCapture{header: make(http.Header)}
}

func (w *responseCapture) Header() http.Header {
	return w.header
}

func (w *responseCapture) WriteHeader(statusCode int) {
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *responseCapture) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buf := [32]byte{}
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + (value % 10))
		value /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
