package middleware

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/logging"
)

func RequestID(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := strings.TrimSpace(r.Header.Get(api.HeaderRequestID))
			if requestID == "" {
				requestID = ulid.Make().String()
			}

			w.Header().Set(api.HeaderRequestID, requestID)
			ctx := api.WithRequestID(r.Context(), requestID)
			ctx = logging.WithRequestID(ctx, requestID)
			requestLogger := logger.With(
				"request_id", requestID,
				"service", "ottercamp",
				"env", strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")),
			)

			started := time.Now()
			recorder := &requestLogRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			requestLogger.Info("request_started",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
			)

			next.ServeHTTP(recorder, r.WithContext(ctx))
			requestLogger.Info("request_completed",
				"status_code", recorder.statusCode,
				"duration_ms", time.Since(started).Milliseconds(),
				"bytes_written", recorder.bytesWritten,
			)
		})
	}
}

type requestLogRecorder struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (r *requestLogRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *requestLogRecorder) Write(payload []byte) (int, error) {
	written, err := r.ResponseWriter.Write(payload)
	r.bytesWritten += written
	return written, err
}

func (r *requestLogRecorder) Flush() {
	flusher, ok := r.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

func (r *requestLogRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (r *requestLogRecorder) Push(target string, opts *http.PushOptions) error {
	pusher, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}
