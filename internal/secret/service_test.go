package secret

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	occrypto "github.com/samhotchkiss/otter-camp/internal/crypto"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestResolveRefHandlesPrefixAndPassthrough(t *testing.T) {
	orgID := uuid.New()
	key := mustMasterKey(t)

	mock := &mockRepository{
		getBySlugFn: func(_ context.Context, gotOrgID uuid.UUID, gotSlug string) (repo.Secret, error) {
			if gotOrgID != orgID {
				t.Fatalf("org id = %s, want %s", gotOrgID, orgID)
			}
			if gotSlug != "my-secret" {
				t.Fatalf("slug = %q, want %q", gotSlug, "my-secret")
			}
			ciphertext, nonce, err := occrypto.Encrypt(key, []byte("decrypted-value"))
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}
			return repo.Secret{
				ID:             uuid.New(),
				OrganizationID: gotOrgID,
				Slug:           gotSlug,
				DisplayName:    "My Secret",
				Ciphertext:     ciphertext,
				Nonce:          nonce,
				KeyVersion:     1,
				CreatedAt:      time.Now(),
				UpdatedAt:      time.Now(),
			}, nil
		},
	}

	service := NewServiceWithKeyLoader(mock, func(_ int) (occrypto.MasterKey, error) {
		return key, nil
	})

	resolved, err := service.ResolveRef(context.Background(), orgID, "ref:my-secret")
	if err != nil {
		t.Fatalf("ResolveRef ref failed: %v", err)
	}
	if resolved != "decrypted-value" {
		t.Fatalf("ResolveRef ref = %q, want %q", resolved, "decrypted-value")
	}

	passthrough, err := service.ResolveRef(context.Background(), orgID, "not-a-ref")
	if err != nil {
		t.Fatalf("ResolveRef passthrough failed: %v", err)
	}
	if passthrough != "not-a-ref" {
		t.Fatalf("ResolveRef passthrough = %q, want %q", passthrough, "not-a-ref")
	}
}

func TestSecretInfoDoesNotExposeSecretFields(t *testing.T) {
	typ := reflect.TypeOf(SecretInfo{})
	for i := 0; i < typ.NumField(); i++ {
		fieldName := typ.Field(i).Name
		if fieldName == "Value" || fieldName == "Plaintext" || fieldName == "Ciphertext" {
			t.Fatalf("SecretInfo field %q must not be present", fieldName)
		}
	}
}

func TestCheckDeleteSafetyAggregatesReferences(t *testing.T) {
	orgID := uuid.New()
	mock := &mockRepository{
		findTransportConfigReferencesFn: func(_ context.Context, gotOrgID uuid.UUID, gotSlug string) ([]string, error) {
			if gotOrgID != orgID {
				t.Fatalf("org id = %s, want %s", gotOrgID, orgID)
			}
			if gotSlug != "my-secret" {
				t.Fatalf("slug = %q, want %q", gotSlug, "my-secret")
			}
			return []string{"mcp_connection 'conn-a' transport_config"}, nil
		},
		findSecretBindingReferencesFn: func(_ context.Context, gotOrgID uuid.UUID, gotSlug string) ([]string, error) {
			if gotOrgID != orgID {
				t.Fatalf("org id = %s, want %s", gotOrgID, orgID)
			}
			if gotSlug != "my-secret" {
				t.Fatalf("slug = %q, want %q", gotSlug, "my-secret")
			}
			return []string{"mcp_secret_binding 'conn-b' env_var_name 'API_KEY'"}, nil
		},
	}
	service := NewServiceWithKeyLoader(mock, func(_ int) (occrypto.MasterKey, error) {
		return mustMasterKey(t), nil
	})

	references, err := service.CheckDeleteSafety(context.Background(), orgID, "my-secret")
	if err != nil {
		t.Fatalf("CheckDeleteSafety failed: %v", err)
	}
	if len(references) != 2 {
		t.Fatalf("len(references) = %d, want 2", len(references))
	}
}

func TestDeleteReturnsErrSecretInUseWhenReferencesExist(t *testing.T) {
	orgID := uuid.New()
	mock := &mockRepository{
		findTransportConfigReferencesFn: func(context.Context, uuid.UUID, string) ([]string, error) {
			return []string{"mcp_connection 'conn-a' transport_config"}, nil
		},
	}
	service := NewServiceWithKeyLoader(mock, func(_ int) (occrypto.MasterKey, error) {
		return mustMasterKey(t), nil
	})

	err := service.Delete(context.Background(), orgID, "api-key", Principal{Type: "human"})
	if !errors.Is(err, ErrSecretInUse) {
		t.Fatalf("Delete error = %v, want ErrSecretInUse", err)
	}
}

func TestSetValidatesInputs(t *testing.T) {
	service := NewServiceWithKeyLoader(&mockRepository{}, func(_ int) (occrypto.MasterKey, error) {
		return mustMasterKey(t), nil
	})

	err := service.Set(context.Background(), uuid.New(), "Bad Slug", "Display", "", "value", Principal{Type: "human"})
	if !errors.Is(err, ErrInvalidSlug) {
		t.Fatalf("Set invalid slug error = %v, want ErrInvalidSlug", err)
	}

	err = service.Set(context.Background(), uuid.New(), "valid-slug", "", "", "value", Principal{Type: "human"})
	if !errors.Is(err, ErrDisplayNameRequired) {
		t.Fatalf("Set missing display name error = %v, want ErrDisplayNameRequired", err)
	}

	err = service.Set(context.Background(), uuid.New(), "valid-slug", "Display", "", "", Principal{Type: "human"})
	if !errors.Is(err, ErrSecretValueRequired) {
		t.Fatalf("Set missing value error = %v, want ErrSecretValueRequired", err)
	}

	err = service.Set(context.Background(), uuid.New(), "valid-slug", "Display", "", "value", Principal{Type: "robot"})
	if !errors.Is(err, ErrInvalidPrincipalType) {
		t.Fatalf("Set invalid principal type error = %v, want ErrInvalidPrincipalType", err)
	}
}

func mustMasterKey(t *testing.T) occrypto.MasterKey {
	t.Helper()

	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	key, err := occrypto.NewMasterKey(1, raw)
	if err != nil {
		t.Fatalf("NewMasterKey failed: %v", err)
	}
	return key
}

type mockRepository struct {
	createFn                        func(ctx context.Context, secret repo.Secret) (repo.Secret, error)
	getBySlugFn                     func(ctx context.Context, organizationID uuid.UUID, slug string) (repo.Secret, error)
	listFn                          func(ctx context.Context, organizationID uuid.UUID) ([]repo.Secret, error)
	updateFn                        func(ctx context.Context, secret repo.Secret) (repo.Secret, error)
	deleteFn                        func(ctx context.Context, organizationID uuid.UUID, slug string) error
	findTransportConfigReferencesFn func(ctx context.Context, organizationID uuid.UUID, slug string) ([]string, error)
	findSecretBindingReferencesFn   func(ctx context.Context, organizationID uuid.UUID, slug string) ([]string, error)
}

func (m *mockRepository) Create(ctx context.Context, secret repo.Secret) (repo.Secret, error) {
	if m.createFn == nil {
		secret.ID = uuid.New()
		return secret, nil
	}
	return m.createFn(ctx, secret)
}

func (m *mockRepository) GetBySlug(ctx context.Context, organizationID uuid.UUID, slug string) (repo.Secret, error) {
	if m.getBySlugFn == nil {
		return repo.Secret{}, repo.ErrNotFound
	}
	return m.getBySlugFn(ctx, organizationID, slug)
}

func (m *mockRepository) List(ctx context.Context, organizationID uuid.UUID) ([]repo.Secret, error) {
	if m.listFn == nil {
		return nil, nil
	}
	return m.listFn(ctx, organizationID)
}

func (m *mockRepository) Update(ctx context.Context, secret repo.Secret) (repo.Secret, error) {
	if m.updateFn == nil {
		return repo.Secret{}, repo.ErrNotFound
	}
	return m.updateFn(ctx, secret)
}

func (m *mockRepository) Delete(ctx context.Context, organizationID uuid.UUID, slug string) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(ctx, organizationID, slug)
}

func (m *mockRepository) FindTransportConfigReferences(ctx context.Context, organizationID uuid.UUID, slug string) ([]string, error) {
	if m.findTransportConfigReferencesFn == nil {
		return nil, nil
	}
	return m.findTransportConfigReferencesFn(ctx, organizationID, slug)
}

func (m *mockRepository) FindSecretBindingReferences(ctx context.Context, organizationID uuid.UUID, slug string) ([]string, error) {
	if m.findSecretBindingReferencesFn == nil {
		return nil, nil
	}
	return m.findSecretBindingReferencesFn(ctx, organizationID, slug)
}

func TestCheckDeleteSafetyReturnsErrorOnInvalidSlug(t *testing.T) {
	service := NewServiceWithKeyLoader(&mockRepository{}, func(_ int) (occrypto.MasterKey, error) {
		return mustMasterKey(t), nil
	})

	_, err := service.CheckDeleteSafety(context.Background(), uuid.New(), "Bad Slug")
	if !errors.Is(err, ErrInvalidSlug) {
		t.Fatalf("CheckDeleteSafety invalid slug error = %v, want ErrInvalidSlug", err)
	}
	if !strings.Contains(err.Error(), "Bad Slug") {
		t.Fatalf("invalid slug error %q should include input slug", err)
	}
}
