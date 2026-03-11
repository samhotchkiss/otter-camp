package bootstrap

import (
	"strings"
	"testing"
)

func TestLoriPromptIncludesStaffingWorkflowRequirements(t *testing.T) {
	var loriPrompt string
	for _, seed := range defaultStarterTrio {
		if seed.displayName == "Lori" {
			loriPrompt = seed.systemPrompt
			break
		}
	}
	if loriPrompt == "" {
		t.Fatal("missing Lori starter trio prompt")
	}
	requiredSnippets := []string{
		"project.staffing_needed",
		"Hire and assign a real staff PM during bootstrap",
		"Never assign Frank, Lori, or Ellie to project PM/worker/reviewer roles",
		"no single executable task should usually exceed 30 minutes",
		"work stage, an internal review stage, and a completion/merge stage",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(loriPrompt, snippet) {
			t.Fatalf("Lori prompt missing %q", snippet)
		}
	}
}
