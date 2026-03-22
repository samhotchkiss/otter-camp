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
		"Do not tell the operator to go read docs",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(loriPrompt, snippet) {
			t.Fatalf("Lori prompt missing %q", snippet)
		}
	}
}

func TestFrankPromptAvoidsSameSessionHandoffLoops(t *testing.T) {
	var frankPrompt string
	for _, seed := range defaultStarterTrio {
		if seed.displayName == "Frank" {
			frankPrompt = seed.systemPrompt
			break
		}
	}
	if frankPrompt == "" {
		t.Fatal("missing Frank starter trio prompt")
	}
	requiredSnippets := []string{
		"Call message.send to the new project session with a handoff message",
		"do not use message.send to echo a handoff back into that same session",
		"Treat the existing project session as the handoff channel",
		"treat that as an in-progress bootstrap continuation",
		"Do not spend turns auditing git history",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(frankPrompt, snippet) {
			t.Fatalf("Frank prompt missing %q", snippet)
		}
	}
}
