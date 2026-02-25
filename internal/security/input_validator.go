package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/api"
)

const DefaultMaxRequestBodyBytes int64 = 10 * 1024 * 1024

type ValidationError struct {
	StatusCode int
	Message    string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "validation error"
	}
	return e.Message
}

type InputValidator struct {
	MaxRequestBodyBytes int64
}

func NewInputValidator() *InputValidator {
	return &InputValidator{MaxRequestBodyBytes: DefaultMaxRequestBodyBytes}
}

func (v *InputValidator) ValidateRequest(r *http.Request) error {
	if r == nil {
		return &ValidationError{StatusCode: http.StatusBadRequest, Message: "request is required"}
	}

	if !requiresJSONValidation(r.Method) {
		return nil
	}
	if !strings.HasPrefix(strings.TrimSpace(r.URL.Path), "/v1/") {
		return nil
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		return &ValidationError{StatusCode: http.StatusUnsupportedMediaType, Message: "content-type must be application/json"}
	}

	limit := DefaultMaxRequestBodyBytes
	if v != nil && v.MaxRequestBodyBytes > 0 {
		limit = v.MaxRequestBodyBytes
	}
	if r.ContentLength > limit {
		return &ValidationError{StatusCode: http.StatusRequestEntityTooLarge, Message: "request body exceeds 10MB limit"}
	}
	if r.Body == nil {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return &ValidationError{StatusCode: http.StatusBadRequest, Message: "failed to read request body"}
	}
	if len(body) > int(limit) {
		return &ValidationError{StatusCode: http.StatusRequestEntityTooLarge, Message: "request body exceeds 10MB limit"}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	if len(body) == 0 {
		return nil
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	if err := validateStringFields(payload); err != nil {
		return &ValidationError{StatusCode: http.StatusUnprocessableEntity, Message: err.Error()}
	}
	return nil
}

func InputValidationMiddleware(validator *InputValidator) func(http.Handler) http.Handler {
	if validator == nil {
		validator = NewInputValidator()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := validator.ValidateRequest(r); err != nil {
				var validationErr *ValidationError
				if ok := asValidationError(err, &validationErr); ok {
					api.Error(w, validationErr.StatusCode, api.ErrCodeValidation, validationErr.Message)
					return
				}
				api.Error(w, http.StatusBadRequest, api.ErrCodeValidation, err.Error())
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func asValidationError(err error, target **ValidationError) bool {
	if err == nil {
		return false
	}
	validationErr, ok := err.(*ValidationError)
	if !ok {
		return false
	}
	*target = validationErr
	return true
}

func requiresJSONValidation(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

func isJSONContentType(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "application/json" || strings.HasPrefix(normalized, "application/json;") {
		return true
	}
	// File import endpoints use multipart form uploads.
	return strings.HasPrefix(normalized, "multipart/form-data;")
}

func validateStringFields(value any) error {
	switch typed := value.(type) {
	case string:
		if strings.ContainsRune(typed, '\x00') {
			return fmt.Errorf("request contains null byte")
		}
		for _, r := range typed {
			if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
				return fmt.Errorf("request contains disallowed control characters")
			}
		}
		return nil
	case []any:
		for _, item := range typed {
			if err := validateStringFields(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, item := range typed {
			if err := validateStringFields(item); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}
