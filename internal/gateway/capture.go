package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

const captureWriteTimeout = 5 * time.Second

type organizationLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Organization, error)
}

type CaptureService struct {
	orgs   organizationLookup
	store  storage.Store
	logger *slog.Logger
	now    func() time.Time
}

func NewCaptureService(orgs organizationLookup, store storage.Store, logger *slog.Logger) *CaptureService {
	if logger == nil {
		logger = slog.Default()
	}
	return &CaptureService{
		orgs:   orgs,
		store:  store,
		logger: logger,
		now:    time.Now,
	}
}

func (s *CaptureService) Capture(ctx context.Context, invocation repo.ModelInvocation, response []byte) (promptKey, responseKey *string) {
	if s == nil || s.orgs == nil || s.store == nil {
		return nil, nil
	}

	org, err := s.orgs.GetByID(ctx, invocation.OrganizationID)
	if err != nil {
		s.logger.Warn("capture settings lookup failed", "organization_id", invocation.OrganizationID, "invocation_id", invocation.ID, "error", err)
		return nil, nil
	}

	captureSettings := org.Settings.ModelCapture
	if !captureSettings.CapturePrompts && !captureSettings.CaptureResponses {
		return nil, nil
	}

	captureTime := invocation.CreatedAt.UTC()
	if captureTime.IsZero() {
		captureTime = s.now().UTC()
	}

	if captureSettings.CapturePrompts {
		key := buildCaptureKey(invocation.OrganizationID.String(), captureTime, invocation.ID.String(), "prompt.json.gz")
		if s.writeJSONGzip(key, invocation.Metadata) {
			promptKey = &key
		}
	}

	if captureSettings.CaptureResponses {
		key := buildCaptureKey(invocation.OrganizationID.String(), captureTime, invocation.ID.String(), "response.json.gz")
		if s.writeJSONGzip(key, response) {
			responseKey = &key
		}
	}

	return promptKey, responseKey
}

func (s *CaptureService) writeJSONGzip(key string, raw []byte) bool {
	normalized := normalizeJSON(raw)
	compressed, err := gzipBytes(normalized)
	if err != nil {
		s.logger.Warn("capture compression failed", "key", key, "error", err)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), captureWriteTimeout)
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- s.store.Put(ctx, key, bytes.NewReader(compressed), storage.PutOptions{
			ContentType:   "application/json",
			ContentLength: int64(len(compressed)),
		})
	}()

	select {
	case err := <-result:
		if err != nil {
			s.logger.Warn("capture write failed", "key", key, "error", err)
			return false
		}
		return true
	case <-ctx.Done():
		s.logger.Warn("capture write timed out", "key", key, "error", ctx.Err())
		return false
	}
}

func buildCaptureKey(orgID string, ts time.Time, invocationID string, fileName string) string {
	return fmt.Sprintf("invocations/%s/%04d/%02d/%s/%s", orgID, ts.Year(), int(ts.Month()), invocationID, fileName)
}

func gzipBytes(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	zip := gzip.NewWriter(&buf)
	if _, err := io.Copy(zip, bytes.NewReader(raw)); err != nil {
		_ = zip.Close()
		return nil, err
	}
	if err := zip.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func normalizeJSON(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return []byte(`{}`)
	}
	if json.Valid(trimmed) {
		return trimmed
	}

	wrapped, err := json.Marshal(map[string]string{"raw": string(trimmed)})
	if err != nil {
		return []byte(`{"raw":"<unavailable>"}`)
	}
	return wrapped
}
