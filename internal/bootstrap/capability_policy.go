package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type defaultCapabilityPolicySeed struct {
	PolicyLayer string
	Capability  string
	Effect      string
}

var defaultCapabilityPolicySeeds = []defaultCapabilityPolicySeed{
	{PolicyLayer: "instance", Capability: "system.bootstrap.instance", Effect: "allow"},
	{PolicyLayer: "org", Capability: "system.bootstrap.org", Effect: "allow"},
}

func RegisterCapabilityPolicyStep(bootstrapper *Bootstrapper, policyRepo *repo.CapabilityPolicyRepo) {
	if bootstrapper == nil || policyRepo == nil {
		return
	}

	bootstrapper.RegisterStep("seed-capability-policy", func(ctx context.Context, state *State) error {
		if state == nil || state.OrganizationID == uuid.Nil {
			return fmt.Errorf("organization id is required")
		}

		for _, seed := range defaultCapabilityPolicySeeds {
			organizationID := (*uuid.UUID)(nil)
			if seed.PolicyLayer == "org" {
				organizationID = &state.OrganizationID
			}

			existing, err := policyRepo.ListByLayer(ctx, seed.PolicyLayer, seed.Capability, organizationID, nil, nil)
			if err != nil {
				return err
			}
			if len(existing) > 0 {
				continue
			}

			if _, err := policyRepo.Create(ctx, repo.CapabilityPolicy{
				PolicyLayer:    seed.PolicyLayer,
				OrganizationID: organizationID,
				Capability:     seed.Capability,
				Effect:         seed.Effect,
				Conditions:     json.RawMessage(`{}`),
				Priority:       100,
				CreatedByType:  systemActorType,
				CreatedByID:    &systemActorID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
