package memory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type EllieDedupMergeDecision struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

type EllieDedupDecision struct {
	Keep      string                   `json:"keep"`
	Deprecate []string                 `json:"deprecate"`
	Merge     *EllieDedupMergeDecision `json:"merge,omitempty"`
}

type EllieDedupReviewMemory struct {
	MemoryID string
	Title    string
	Content  string
}

func BuildEllieDedupReviewPrompt(memories []EllieDedupReviewMemory) string {
	var builder strings.Builder
	builder.WriteString("You are reviewing potential duplicate memories.\\n")
	builder.WriteString("Decide whether the memories represent the same fact or related-but-distinct facts.\\n")
	builder.WriteString("Return JSON with fields: keep, deprecate[], merge(optional {title,content}).\\n")
	builder.WriteString("Rules:\\n")
	builder.WriteString("- keep must be an existing memory id or empty when merge is used.\\n")
	builder.WriteString("- deprecate must only contain existing memory ids.\\n")
	builder.WriteString("- Never delete memories; deprecate only.\\n")
	builder.WriteString("- merge is optional and should only be used when combining same-fact variants improves fidelity.\\n\\n")
	builder.WriteString("Cluster memories:\\n")
	for _, memory := range memories {
		id := strings.TrimSpace(memory.MemoryID)
		if id == "" {
			continue
		}
		title := strings.TrimSpace(memory.Title)
		if title == "" {
			title = "Untitled"
		}
		content := strings.TrimSpace(memory.Content)
		if content == "" {
			content = "(empty)"
		}
		builder.WriteString(fmt.Sprintf("- [%s] %s\\n", id, title))
		builder.WriteString(fmt.Sprintf("  %s\\n", content))
	}
	return builder.String()
}

func ParseAndValidateEllieDedupDecision(clusterMemoryIDs []string, raw string) (EllieDedupDecision, error) {
	var decision EllieDedupDecision
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &decision); err != nil {
		return EllieDedupDecision{}, fmt.Errorf("invalid dedup decision json: %w", err)
	}
	if err := ValidateEllieDedupDecision(clusterMemoryIDs, decision); err != nil {
		return EllieDedupDecision{}, err
	}
	return decision, nil
}

func ValidateEllieDedupDecision(clusterMemoryIDs []string, decision EllieDedupDecision) error {
	allowed := make(map[string]struct{}, len(clusterMemoryIDs))
	for _, memoryID := range clusterMemoryIDs {
		trimmed := strings.TrimSpace(memoryID)
		if trimmed == "" {
			continue
		}
		allowed[trimmed] = struct{}{}
	}
	if len(allowed) == 0 {
		return fmt.Errorf("cluster must contain at least one memory")
	}

	keep := strings.TrimSpace(decision.Keep)
	if keep != "" {
		if _, ok := allowed[keep]; !ok {
			return fmt.Errorf("keep memory id %q is not in cluster", keep)
		}
	}

	deprecate := make([]string, 0, len(decision.Deprecate))
	seenDeprecate := make(map[string]struct{}, len(decision.Deprecate))
	for _, memoryID := range decision.Deprecate {
		trimmed := strings.TrimSpace(memoryID)
		if trimmed == "" {
			continue
		}
		if _, ok := allowed[trimmed]; !ok {
			return fmt.Errorf("deprecate memory id %q is not in cluster", trimmed)
		}
		if _, exists := seenDeprecate[trimmed]; exists {
			return fmt.Errorf("duplicate deprecate memory id %q", trimmed)
		}
		seenDeprecate[trimmed] = struct{}{}
		deprecate = append(deprecate, trimmed)
	}

	if keep != "" {
		if _, ok := seenDeprecate[keep]; ok {
			return fmt.Errorf("keep memory id %q cannot also be deprecated", keep)
		}
	}

	if decision.Merge != nil {
		mergeTitle := strings.TrimSpace(decision.Merge.Title)
		mergeContent := strings.TrimSpace(decision.Merge.Content)
		if mergeTitle == "" || mergeContent == "" {
			return fmt.Errorf("merge title and content are required when merge is set")
		}
	} else if keep == "" {
		return fmt.Errorf("decision requires keep when merge is not set")
	}

	if keep == "" && decision.Merge == nil && len(deprecate) == len(allowed) {
		return fmt.Errorf("decision cannot deprecate entire cluster without merge")
	}

	sort.Strings(deprecate)
	decision.Deprecate = deprecate
	return nil
}
