package memory

import (
	"fmt"
	"strings"
)

type EllieEntitySynthesisPromptSourceMemory struct {
	MemoryID string
	Title    string
	Content  string
}

type EllieEntitySynthesisPromptInput struct {
	EntityName     string
	SourceMemories []EllieEntitySynthesisPromptSourceMemory
}

func BuildEllieEntitySynthesisPrompt(input EllieEntitySynthesisPromptInput) string {
	entityName := strings.TrimSpace(input.EntityName)
	if entityName == "" {
		entityName = "Unknown Entity"
	}

	var builder strings.Builder
	builder.WriteString("You are Ellie, generating a high-fidelity entity definition memory.\\n")
	builder.WriteString("Do not drop specific facts. Preserve concrete details like version numbers, model names, file paths, IDs, and timestamps when present.\\n\\n")
	builder.WriteString(fmt.Sprintf("Entity: %s\\n\\n", entityName))
	builder.WriteString("Output contract:\\n")
	builder.WriteString("- Return exactly one memory object with kind=fact and importance=4-5.\\n")
	builder.WriteString("- Structure the content using these headings in order:\\n")
	builder.WriteString("  1. What it is\\n")
	builder.WriteString("  2. What it does\\n")
	builder.WriteString("  3. Current status\\n")
	builder.WriteString("  4. Key technical details\\n")
	builder.WriteString("- Include all known facts from source memories.\\n")
	builder.WriteString("- If facts conflict, note the conflict explicitly without deleting either fact.\\n\\n")
	builder.WriteString("Source memories:\\n")
	for _, source := range input.SourceMemories {
		memoryID := strings.TrimSpace(source.MemoryID)
		if memoryID == "" {
			memoryID = "unknown"
		}
		title := strings.TrimSpace(source.Title)
		if title == "" {
			title = "Untitled"
		}
		content := strings.TrimSpace(source.Content)
		if content == "" {
			content = "(no content provided)"
		}

		builder.WriteString(fmt.Sprintf("- [%s] %s\\n", memoryID, title))
		builder.WriteString(fmt.Sprintf("  Facts: %s\\n", content))
	}

	builder.WriteString("\\nRespond with JSON containing title and content fields only.\\n")
	return builder.String()
}
