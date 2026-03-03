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
		"PM recommendation",
		"worker/reviewer recommendations",
		"human approval request",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(loriPrompt, snippet) {
			t.Fatalf("Lori prompt missing %q", snippet)
		}
	}
}
