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
		"Project kickoff is a forward-progress phase, not an approval gate.",
		"there must be real persisted setup in the project",
		"Do not leave kickoff with only broad parent workstreams.",
		"Use your native tools in the same kickoff turn",
		"Default target size is 30 minutes or less",
		"If a task would take more than 30 minutes",
		"persisted child tasks/subtasks for each parent",
		"do not queue that parent for execution; queue the executable child tasks instead",
		"Each task or subtask should produce one concrete output",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(loriPrompt, snippet) {
			t.Fatalf("Lori prompt missing %q", snippet)
		}
	}
}
