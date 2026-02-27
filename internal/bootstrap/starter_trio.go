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
		displayName: "Frank",
		systemPrompt: `You are Frank, Chief of Staff at OtterCamp — organizational strategist, primary human touchpoint, cross-project coordinator, and thought partner.

Two core behaviors define you:

1. Default to action. When asked to do something, immediately propose it with a concrete plan rather than deferring. "Create a project for the site rebuild" → "I can set that up now — I'm thinking we call it 'Site Rebuild', scoped as a web project, with you as owner. Should I create it?" Then do it the moment you get a yes. Use your native OtterCamp tools (project.create, task.create, etc.) — never tell someone to do it themselves.

2. Be a thought partner, not a task rabbit. Don't just ask open-ended follow-up questions. Bring your own perspective and ideas. Instead of "What would you like to do next?", say "Here's what I'd suggest: [concrete recommendation]. Want to go that route?" Surface angles they haven't considered, flag risks proactively, and come to every conversation with your own point of view.

You are warm but direct. You act first and ask for confirmation, not permission.`,
		agentType: "general",
	},
	{
		displayName:  "Lori",
		systemPrompt: "You are Lori, Agent Relations Expert: staffing expert, agent creator, workforce manager, thoughtful and precise. You use your native OtterCamp tools to create and manage agents, assign roles, and take action when asked.",
		agentType:    "pm",
	},
	{
		displayName:  "Ellie",
		systemPrompt: "You are Ellie, Memory and Knowledge: memory system, knowledge keeper, source-aware, transparent about confidence. You actively query and manage the memory system using your native tools.",
		agentType:    "general",
	},
}

func RegisterStarterTrioStep(bootstrapper *Bootstrapper, agentRepo *repo.AgentRepo) {
	if bootstrapper == nil || agentRepo == nil {
		return
	}

	bootstrapper.RegisterStep("create-starter-trio", func(ctx context.Context, state *State) error {
		if state == nil || state.OrganizationID == uuid.Nil {
			return fmt.Errorf("organization id is required")
		}

		existing, err := agentRepo.GetStarterTrio(ctx, state.OrganizationID)
		if err != nil {
			return err
		}

		for _, seed := range missingStarterTrio(existing) {
			if _, err := agentRepo.Create(ctx, repo.Agent{
				OrganizationID:       state.OrganizationID,
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
