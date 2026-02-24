package bootstrap

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type starterTrioSeed struct {
	displayName  string
	systemPrompt string
	agentType    string
}

var defaultStarterTrio = []starterTrioSeed{
	{
		displayName:  "Frank",
		systemPrompt: "You are Frank, Chief of Staff: organizational strategist, primary human touchpoint, cross-project coordinator, escalation handler, warm but direct.",
		agentType:    "general",
	},
	{
		displayName:  "Lori",
		systemPrompt: "You are Lori, Agent Relations Expert: staffing expert, agent creator, workforce manager, thoughtful and precise.",
		agentType:    "general",
	},
	{
		displayName:  "Ellie",
		systemPrompt: "You are Ellie, Memory and Knowledge: memory system, knowledge keeper, source-aware, transparent about confidence.",
		agentType:    "general",
	},
}

func RegisterStarterTrioStep(bootstrapper *Bootstrapper, agentRepo *repo.AgentRepo) {
	if bootstrapper == nil || agentRepo == nil {
		return
	}

	bootstrapper.RegisterStep("create-agents", func(ctx context.Context, state *BootstrapState) error {
		if state == nil || state.Organization.ID == uuid.Nil {
			return fmt.Errorf("organization id is required")
		}

		existing, err := agentRepo.GetStarterTrio(ctx, state.Organization.ID)
		if err != nil {
			return err
		}

		for _, seed := range missingStarterTrio(existing) {
			if _, err := agentRepo.Create(ctx, repo.Agent{
				OrganizationID:       state.Organization.ID,
				DisplayName:          seed.displayName,
				AgentClass:           "staff",
				LifecycleStatus:      "active",
				SystemPrompt:         seed.systemPrompt,
				OperatorInstructions: "",
				AgentType:            seed.agentType,
				IsStarterTrio:        true,
				PrivateMemory:        false,
				MemoryReadScopes:     []string{"org", "project", "agent"},
				ToolAllowList:        []string{},
				ToolDenyList:         []string{},
				CreatedByType:        "system",
				CreatedByID:          uuid.Nil,
			}); err != nil {
				return err
			}
		}

		return nil
	})
}

func missingStarterTrio(existing []repo.Agent) []starterTrioSeed {
	present := make(map[string]struct{}, len(existing))
	for _, agent := range existing {
		if agent.IsStarterTrio {
			present[agent.DisplayName] = struct{}{}
		}
	}

	missing := make([]starterTrioSeed, 0, len(defaultStarterTrio))
	for _, candidate := range defaultStarterTrio {
		if _, ok := present[candidate.displayName]; ok {
			continue
		}
		missing = append(missing, candidate)
	}

	return missing
}
