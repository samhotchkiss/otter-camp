package gateway

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

type stubOrgLookup struct {
	org repo.Organization
}

func (s stubOrgLookup) GetByID(_ context.Context, _ uuid.UUID) (repo.Organization, error) {
	return s.org, nil
}

type stubStore struct {
	putCalls int
	lastKey  string
}

func (s *stubStore) Put(_ context.Context, key string, r io.Reader, _ storage.PutOptions) error {
	s.putCalls++
	s.lastKey = key
	_, _ = io.Copy(io.Discard, r)
	return nil
}

func (*stubStore) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (*stubStore) Delete(context.Context, string) error         { return nil }
func (*stubStore) Exists(context.Context, string) (bool, error) { return false, nil }
func (*stubStore) List(context.Context, string) ([]storage.ObjectMeta, error) {
	return nil, nil
}

func TestCaptureSkipsPromptWriteWhenCapturePromptsDisabled(t *testing.T) {
	store := &stubStore{}
	service := NewCaptureService(
		stubOrgLookup{org: repo.Organization{
			Settings: repo.OrganizationSettings{
				ModelCapture: repo.OrganizationModelCaptureSettings{
					CapturePrompts: false,
				},
			},
		}},
		store,
		nil,
	)

	invocation := repo.ModelInvocation{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		CreatedAt:      time.Now().UTC(),
		Metadata:       []byte(`{"prompt":"hello"}`),
	}
	promptKey, _ := service.Capture(context.Background(), invocation, []byte(`{"ok":true}`))
	if promptKey != nil {
		t.Fatalf("prompt key = %v, want nil", *promptKey)
	}
	if store.putCalls != 0 {
		t.Fatalf("Put calls = %d, want 0", store.putCalls)
	}
}

func TestCaptureWritesPromptWhenEnabled(t *testing.T) {
	store := &stubStore{}
	service := NewCaptureService(
		stubOrgLookup{org: repo.Organization{
			Settings: repo.OrganizationSettings{
				ModelCapture: repo.OrganizationModelCaptureSettings{
					CapturePrompts: true,
				},
			},
		}},
		store,
		nil,
	)

	invocation := repo.ModelInvocation{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		CreatedAt:      time.Now().UTC(),
		Metadata:       []byte(`{"prompt":"hello"}`),
	}
	promptKey, _ := service.Capture(context.Background(), invocation, []byte(`{"ok":true}`))
	if promptKey == nil {
		t.Fatal("prompt key is nil, want value")
	}
	if !strings.HasPrefix(*promptKey, "invocations/"+invocation.OrganizationID.String()+"/") {
		t.Fatalf("prompt key = %q, missing expected prefix", *promptKey)
	}
	if store.putCalls != 1 {
		t.Fatalf("Put calls = %d, want 1", store.putCalls)
	}
}
