//go:build integration

package bootstrap

import (
	"context"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestRegisterCapabilityPolicyStepSeedsPoliciesIdempotently(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "bootstrap-policy", DisplayName: "Bootstrap Policy"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	policies := repo.NewCapabilityPolicyRepo(pool)
	bootstrapper := NewBootstrapper(Options{DisableDefaultStep: true})
	RegisterCapabilityPolicyStep(bootstrapper, policies)

	state := &State{OrganizationID: org.ID}
	if err := bootstrapper.RunWithState(ctx, state); err != nil {
		t.Fatalf("first bootstrap run: %v", err)
	}
	if err := bootstrapper.RunWithState(ctx, state); err != nil {
		t.Fatalf("second bootstrap run: %v", err)
	}

	instance, err := policies.ListByLayer(ctx, "instance", "system.bootstrap.instance", nil, nil, nil)
	if err != nil {
		t.Fatalf("list instance policies: %v", err)
	}
	if len(instance) != 1 {
		t.Fatalf("instance policy count=%d want=1", len(instance))
	}

	orgPolicies, err := policies.ListByLayer(ctx, "org", "system.bootstrap.org", &org.ID, nil, nil)
	if err != nil {
		t.Fatalf("list org policies: %v", err)
	}
	if len(orgPolicies) != 1 {
		t.Fatalf("org policy count=%d want=1", len(orgPolicies))
	}
}
