package memory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildEllieEntitySynthesisPromptIncludesAllFacts(t *testing.T) {
	prompt := BuildEllieEntitySynthesisPrompt(EllieEntitySynthesisPromptInput{
		EntityName: "ItsAlive",
		SourceMemories: []EllieEntitySynthesisPromptSourceMemory{
			{
				MemoryID: "mem-1",
				Title:    "Embedding defaults",
				Content:  "ItsAlive switched to text-embedding-3-small (1536d).",
			},
			{
				MemoryID: "mem-2",
				Title:    "Runtime wiring",
				Content:  "The worker now reads docs from docs/START-HERE.md and writes checkpoints to internal/import/migration_runner.go.",
			},
			{
				MemoryID: "mem-3",
				Title:    "Model config",
				Content:  "Extraction model is anthropic/claude-sonnet-4-20250514 for high-fidelity synthesis.",
			},
		},
	})

	require.Contains(t, prompt, "text-embedding-3-small (1536d)")
	require.Contains(t, prompt, "docs/START-HERE.md")
	require.Contains(t, prompt, "internal/import/migration_runner.go")
	require.Contains(t, prompt, "anthropic/claude-sonnet-4-20250514")
	require.Contains(t, prompt, "mem-1")
	require.Contains(t, prompt, "mem-2")
	require.Contains(t, prompt, "mem-3")
}

func TestBuildEllieEntitySynthesisPromptUsesRequiredSections(t *testing.T) {
	prompt := BuildEllieEntitySynthesisPrompt(EllieEntitySynthesisPromptInput{
		EntityName: "OtterCamp",
		SourceMemories: []EllieEntitySynthesisPromptSourceMemory{
			{
				MemoryID: "mem-1",
				Title:    "Overview",
				Content:  "OtterCamp coordinates AI + human delivery workflows.",
			},
		},
	})

	require.Contains(t, prompt, "What it is")
	require.Contains(t, prompt, "What it does")
	require.Contains(t, prompt, "Current status")
	require.Contains(t, prompt, "Key technical details")
	require.Contains(t, prompt, "kind=fact")
	require.Contains(t, prompt, "importance=4-5")
}
