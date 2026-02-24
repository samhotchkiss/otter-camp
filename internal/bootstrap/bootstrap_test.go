package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRegisterStepOverridesStubAndPreservesOrder(t *testing.T) {
	calls := make([]string, 0, 3)
	b := &Bootstrapper{
		steps: []bootstrapStep{
			{number: 1, name: "first", fn: func(context.Context, *BootstrapState) error {
				calls = append(calls, "first")
				return nil
			}},
			{number: 2, name: "create-agents", fn: func(context.Context, *BootstrapState) error {
				calls = append(calls, "stub")
				return nil
			}},
			{number: 3, name: "last", fn: func(context.Context, *BootstrapState) error {
				calls = append(calls, "last")
				return nil
			}},
		},
	}

	b.RegisterStep("create-agents", func(context.Context, *BootstrapState) error {
		calls = append(calls, "override")
		return nil
	})

	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := strings.Join(calls, ",")
	want := "first,override,last"
	if got != want {
		t.Fatalf("call order = %q, want %q", got, want)
	}
}

func TestRunRecoversPanicAndReturnsError(t *testing.T) {
	b := &Bootstrapper{
		steps: []bootstrapStep{
			{number: 1, name: "panic-step", fn: func(context.Context, *BootstrapState) error {
				panic("boom")
			}},
		},
	}

	err := b.Run(context.Background())
	if err == nil {
		t.Fatal("expected panic to be returned as error")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("error = %v, want panic context", err)
	}
}

func TestOrganizationBySlugCheck(t *testing.T) {
	expected := repo.Organization{ID: uuid.New(), Slug: "default"}
	b := &Bootstrapper{organizations: &stubOrganizationStore{org: expected}}

	org, exists, err := b.organizationBySlug(context.Background(), "default")
	if err != nil {
		t.Fatalf("organizationBySlug: %v", err)
	}
	if !exists {
		t.Fatal("expected organization to exist")
	}
	if org.ID != expected.ID {
		t.Fatalf("org id = %s, want %s", org.ID, expected.ID)
	}

	b.organizations = &stubOrganizationStore{err: repo.ErrNotFound}
	_, exists, err = b.organizationBySlug(context.Background(), "missing")
	if err != nil {
		t.Fatalf("organizationBySlug missing: %v", err)
	}
	if exists {
		t.Fatal("expected missing organization")
	}
}

func TestAdminUserExistsCheck(t *testing.T) {
	orgID := uuid.New()
	admin := repo.HumanUser{ID: uuid.New(), OrganizationID: orgID, Role: "admin"}
	b := &Bootstrapper{users: &stubUserStore{users: []repo.HumanUser{{Role: "member"}, admin}}}

	user, exists, err := b.adminUserForOrg(context.Background(), orgID)
	if err != nil {
		t.Fatalf("adminUserForOrg: %v", err)
	}
	if !exists {
		t.Fatal("expected admin user to exist")
	}
	if user.ID != admin.ID {
		t.Fatalf("admin id = %s, want %s", user.ID, admin.ID)
	}

	b.users = &stubUserStore{users: []repo.HumanUser{{Role: "member"}}}
	_, exists, err = b.adminUserForOrg(context.Background(), orgID)
	if err != nil {
		t.Fatalf("adminUserForOrg without admin: %v", err)
	}
	if exists {
		t.Fatal("expected no admin user")
	}
}

func TestBootstrapAuditEventExistsCheck(t *testing.T) {
	orgID := uuid.New()
	b := &Bootstrapper{auditChecker: stubAuditChecker{exists: true}}

	exists, err := b.bootstrapAuditEventExists(context.Background(), orgID)
	if err != nil {
		t.Fatalf("bootstrapAuditEventExists: %v", err)
	}
	if !exists {
		t.Fatal("expected bootstrap event to exist")
	}

	b.auditChecker = stubAuditChecker{exists: false}
	exists, err = b.bootstrapAuditEventExists(context.Background(), orgID)
	if err != nil {
		t.Fatalf("bootstrapAuditEventExists false: %v", err)
	}
	if exists {
		t.Fatal("expected bootstrap event to be absent")
	}
}

func TestEnsureProviderIdempotencyCheck(t *testing.T) {
	existing := repo.ModelProvider{ID: uuid.New(), Slug: "anthropic"}
	b := &Bootstrapper{providers: &stubProviderStore{provider: existing}}

	got, err := b.ensureProvider(context.Background(), "anthropic", "Anthropic", "https://api.anthropic.com")
	if err != nil {
		t.Fatalf("ensureProvider existing: %v", err)
	}
	if got.ID != existing.ID {
		t.Fatalf("provider id = %s, want %s", got.ID, existing.ID)
	}

	created := repo.ModelProvider{ID: uuid.New(), Slug: "openai"}
	b.providers = &stubProviderStore{getErr: repo.ErrNotFound, createdProvider: created}
	got, err = b.ensureProvider(context.Background(), "openai", "OpenAI", "https://api.openai.com/v1")
	if err != nil {
		t.Fatalf("ensureProvider create: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("created provider id = %s, want %s", got.ID, created.ID)
	}
}

func TestEnsureProfileIdempotencyCheck(t *testing.T) {
	orgID := uuid.New()
	existing := repo.ModelProfile{ID: uuid.New(), LogicalProfileID: "standard"}
	b := &Bootstrapper{profiles: &stubProfileStore{profile: existing}}

	got, err := b.ensureProfile(context.Background(), orgID, repo.ModelProfile{LogicalProfileID: "standard"})
	if err != nil {
		t.Fatalf("ensureProfile existing: %v", err)
	}
	if got.ID != existing.ID {
		t.Fatalf("profile id = %s, want %s", got.ID, existing.ID)
	}

	created := repo.ModelProfile{ID: uuid.New(), LogicalProfileID: "haiku"}
	b.profiles = &stubProfileStore{getErr: repo.ErrNotFound, createdProfile: created}
	got, err = b.ensureProfile(context.Background(), orgID, repo.ModelProfile{LogicalProfileID: "haiku"})
	if err != nil {
		t.Fatalf("ensureProfile create: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("created profile id = %s, want %s", got.ID, created.ID)
	}
}

type stubOrganizationStore struct {
	org repo.Organization
	err error
}

func (s *stubOrganizationStore) GetBySlug(context.Context, string) (repo.Organization, error) {
	if s.err != nil {
		return repo.Organization{}, s.err
	}
	return s.org, nil
}

func (s *stubOrganizationStore) Create(context.Context, repo.Organization) (repo.Organization, error) {
	if s.err != nil {
		return repo.Organization{}, s.err
	}
	if s.org.ID == uuid.Nil {
		s.org.ID = uuid.New()
	}
	return s.org, nil
}

type stubUserStore struct {
	users []repo.HumanUser
	err   error
}

func (s *stubUserStore) List(context.Context, uuid.UUID) ([]repo.HumanUser, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]repo.HumanUser{}, s.users...), nil
}

func (s *stubUserStore) Create(context.Context, repo.HumanUser) (repo.HumanUser, error) {
	return repo.HumanUser{}, errors.New("not implemented")
}

type stubAuditChecker struct {
	exists bool
	err    error
}

func (s stubAuditChecker) ExistsBootstrapEvent(context.Context, uuid.UUID) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.exists, nil
}

type stubProviderStore struct {
	provider        repo.ModelProvider
	createdProvider repo.ModelProvider
	getErr          error
}

func (s *stubProviderStore) GetBySlug(context.Context, string) (repo.ModelProvider, error) {
	if s.getErr != nil {
		return repo.ModelProvider{}, s.getErr
	}
	return s.provider, nil
}

func (s *stubProviderStore) Create(context.Context, repo.ModelProvider) (repo.ModelProvider, error) {
	if s.createdProvider.ID == uuid.Nil {
		s.createdProvider.ID = uuid.New()
	}
	return s.createdProvider, nil
}

type stubProfileStore struct {
	profile        repo.ModelProfile
	createdProfile repo.ModelProfile
	getErr         error
}

func (s *stubProfileStore) GetCurrentByLogicalID(context.Context, uuid.UUID, string) (repo.ModelProfile, error) {
	if s.getErr != nil {
		return repo.ModelProfile{}, s.getErr
	}
	return s.profile, nil
}

func (s *stubProfileStore) Create(context.Context, repo.ModelProfile) (repo.ModelProfile, error) {
	if s.createdProfile.ID == uuid.Nil {
		s.createdProfile.ID = uuid.New()
	}
	return s.createdProfile, nil
}
