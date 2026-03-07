package taskdecomp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAnalyzeFlagsOversizedMultiDeliverableSpecs(t *testing.T) {
	description := strings.Join([]string{
		"1. Migrate all 36 posts from legacy markdown into the new CMS model including author mapping and canonical slug preservation.",
		"2. Rewrite media links and upload all referenced assets into object storage with stable paths and redirects.",
		"3. Rebuild taxonomy mapping for tags/categories and validate URL parity for existing inbound links.",
	}, "\n")

	plan := Analyze("Migrate legacy blog", &description)
	if !plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = false, want true")
	}
	if plan.PrimaryDeliverable == "" {
		t.Fatal("PrimaryDeliverable is empty, want non-empty")
	}
	if len(plan.ChildDeliverables) < 1 {
		t.Fatalf("ChildDeliverables len = %d, want >= 1", len(plan.ChildDeliverables))
	}
}

func TestAnalyzeSkipsSmallSingleDeliverableSpecs(t *testing.T) {
	description := "Implement pagination controls for the project list view."

	plan := Analyze("Add pagination", &description)
	if plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = true, want false")
	}
}

func TestParsePrimaryDeliverableFromMetadata(t *testing.T) {
	raw := json.RawMessage(`{"decomposition":{"primary_deliverable":"Migrate posts in canonical order"}}`)

	primary := ParsePrimaryDeliverable(raw)
	if primary != "Migrate posts in canonical order" {
		t.Fatalf("ParsePrimaryDeliverable = %q, want %q", primary, "Migrate posts in canonical order")
	}
}

func TestParsePrimaryDeliverableMissingOrMalformedReturnsEmpty(t *testing.T) {
	if got := ParsePrimaryDeliverable(json.RawMessage(`{"other":"value"}`)); got != "" {
		t.Fatalf("missing decomposition ParsePrimaryDeliverable = %q, want empty", got)
	}
	if got := ParsePrimaryDeliverable(json.RawMessage(`{"decomposition":`)); got != "" {
		t.Fatalf("malformed metadata ParsePrimaryDeliverable = %q, want empty", got)
	}
}

func TestApplyMetadataRoundTripPreservesExistingKeys(t *testing.T) {
	existing := ApplyQueueDecompositionMode(json.RawMessage(`{"preserve":"yes","count":2}`), QueueDecompositionModeParallelChildren)
	plan := Plan{
		RequiresDecomposition: true,
		PrimaryDeliverable:    "Primary deliverable",
		Deliverables:          []string{"Primary deliverable", "Secondary deliverable"},
	}
	childID := uuid.New()

	updated := ApplyMetadata(existing, plan, "  source description  ", []uuid.UUID{childID})

	var payload map[string]any
	if err := json.Unmarshal(updated, &payload); err != nil {
		t.Fatalf("unmarshal ApplyMetadata output: %v", err)
	}
	if payload["preserve"] != "yes" {
		t.Fatalf("preserve key = %v, want yes", payload["preserve"])
	}
	decomp, ok := payload["decomposition"].(map[string]any)
	if !ok {
		t.Fatalf("decomposition payload type = %T, want map[string]any", payload["decomposition"])
	}
	if decomp["primary_deliverable"] != "Primary deliverable" {
		t.Fatalf("primary_deliverable = %v, want Primary deliverable", decomp["primary_deliverable"])
	}
	if decomp["mode"] != QueueDecompositionModeParallelChildren {
		t.Fatalf("mode = %v, want %s", decomp["mode"], QueueDecompositionModeParallelChildren)
	}
	if decomp["source_description"] != "source description" {
		t.Fatalf("source_description = %v, want trimmed source description", decomp["source_description"])
	}
	childIDs, ok := decomp["child_task_ids"].([]any)
	if !ok || len(childIDs) != 1 || childIDs[0] != childID.String() {
		t.Fatalf("child_task_ids = %v, want [%s]", decomp["child_task_ids"], childID.String())
	}
}

func TestExtractDeliverablesSupportsSemicolonsSentenceSplitAndMixedFormats(t *testing.T) {
	semicolon := "Migrate legacy posts into the new CMS while preserving canonical slugs and author mapping; Rewrite media links and upload assets into object storage with redirect coverage; Rebuild taxonomy mappings and validate inbound URL parity."
	semicolonItems := extractDeliverables(semicolon)
	if len(semicolonItems) < 3 {
		t.Fatalf("semicolon extract len = %d, want >= 3", len(semicolonItems))
	}

	sentence := "Migrate legacy posts into the new CMS while preserving canonical slugs and author mapping. Rewrite media links and upload assets into object storage with redirect coverage. Rebuild taxonomy mappings and validate inbound URL parity."
	sentenceItems := extractDeliverables(sentence)
	if len(sentenceItems) < 3 {
		t.Fatalf("sentence extract len = %d, want >= 3", len(sentenceItems))
	}

	mixed := strings.Join([]string{
		"- Migrate legacy posts into the new CMS while preserving canonical slugs and author mapping",
		"* Rewrite media links and upload assets into object storage with redirect coverage",
		"3) Rebuild taxonomy mappings and validate inbound URL parity",
		"Rebuild taxonomy mappings and validate inbound URL parity",
	}, "\n")
	mixedItems := extractDeliverables(mixed)
	if len(mixedItems) != 3 {
		t.Fatalf("mixed extract len = %d, want 3 unique deliverables", len(mixedItems))
	}
}

func TestPrepareQueueDecompositionSkipsWhenAlreadyDecomposed(t *testing.T) {
	description := strings.Join([]string{
		"- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.",
		"- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.",
		"- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots.",
	}, "\n")

	result, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "Migration task",
		Description:  &description,
		Metadata:     json.RawMessage(`{"decomposition":{"mode":"parallel_children","primary_deliverable":"already set"}}`),
	})
	if err != nil {
		t.Fatalf("PrepareQueueDecomposition: %v", err)
	}
	if result.Applied {
		t.Fatal("Applied = true, want false when task already has decomposition metadata")
	}
	if len(result.ChildDrafts) != 0 {
		t.Fatalf("ChildDrafts len = %d, want 0", len(result.ChildDrafts))
	}
}

func TestPrepareQueueDecompositionSkipsWithoutExplicitMode(t *testing.T) {
	description := strings.Join([]string{
		"- Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping.",
		"- Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage.",
		"- Rebuild taxonomy/tag mappings and verify inbound URL parity against production analytics snapshots.",
	}, "\n")

	result, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "Migration task",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("PrepareQueueDecomposition: %v", err)
	}
	if result.Applied {
		t.Fatal("Applied = true, want false without explicit queue decomposition mode")
	}
	if len(result.ChildDrafts) != 0 {
		t.Fatalf("ChildDrafts len = %d, want 0", len(result.ChildDrafts))
	}
}

func TestApplyQueueDecompositionModeRoundTrip(t *testing.T) {
	metadata := ApplyQueueDecompositionMode(json.RawMessage(`{"preserve":"yes"}`), QueueDecompositionModeParallelChildren)

	if !QueueDecompositionRequested(metadata) {
		t.Fatal("QueueDecompositionRequested = false, want true")
	}
	if got := ParseQueueDecompositionMode(metadata); got != QueueDecompositionModeParallelChildren {
		t.Fatalf("ParseQueueDecompositionMode = %q, want %q", got, QueueDecompositionModeParallelChildren)
	}

	cleared := ApplyQueueDecompositionMode(metadata, "")
	if QueueDecompositionRequested(cleared) {
		t.Fatal("QueueDecompositionRequested = true after clearing mode, want false")
	}
}
