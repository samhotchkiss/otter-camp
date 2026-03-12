package taskdecomp

import (
	"encoding/json"
	"errors"
	"reflect"
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

func TestAnalyzeFlagsEnumeratedCompoundTitlesWithoutStructuredDescriptions(t *testing.T) {
	description := "Develop the content backlog for the launch."

	plan := Analyze("Generate 20 new blog post ideas across all pillars", &description)
	if !plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = false, want true for enumerated compound title")
	}
	if plan.PrimaryDeliverable == "" {
		t.Fatal("PrimaryDeliverable is empty, want non-empty")
	}
	if plan.PrimaryDeliverable != "Generate new blog post ideas 1-10" {
		t.Fatalf("PrimaryDeliverable = %q, want numbered batch split", plan.PrimaryDeliverable)
	}
	if len(plan.ChildDeliverables) < 1 || plan.ChildDeliverables[0] != "Generate new blog post ideas 11-20" {
		t.Fatalf("ChildDeliverables = %v, want first child to be second numbered batch", plan.ChildDeliverables)
	}
}

func TestAnalyzeFlagsEnumeratedCompoundActionTitles(t *testing.T) {
	description := "Develop the second half of the idea backlog."

	plan := Analyze("Generate blog post concepts 11-20 and compile final list", &description)
	if !plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = false, want true for enumerated compound action title")
	}
	if plan.PrimaryDeliverable != "Generate blog post concepts 11-20" {
		t.Fatalf("PrimaryDeliverable = %q, want %q", plan.PrimaryDeliverable, "Generate blog post concepts 11-20")
	}
	if len(plan.ChildDeliverables) < 1 {
		t.Fatalf("ChildDeliverables len = %d, want >= 1", len(plan.ChildDeliverables))
	}
	if plan.ChildDeliverables[0] != "compile final list" {
		t.Fatalf("ChildDeliverables[0] = %q, want %q", plan.ChildDeliverables[0], "compile final list")
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
	if decomp["orchestration_only"] != true {
		t.Fatalf("orchestration_only = %v, want true", decomp["orchestration_only"])
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

func TestExtractDeliverablesIgnoresPlanningOnlySentences(t *testing.T) {
	description := "Parent workstream: Scrape ALL existing blog posts from technonymous.org, preserving post body text, publish dates, categories, tags, and all metadata. Store in the Sam.blog git repo as Markdown with YAML frontmatter for clean migration into the new site. Assigned to Riku (Content Migration Specialist). Blocked on WS3 for pillar alignment."

	items := extractDeliverables(description)
	if len(items) != 2 {
		t.Fatalf("extractDeliverables len = %d, want 2 executable deliverables", len(items))
	}
	if items[0] != "Scrape ALL existing blog posts from technonymous.org, preserving post body text, publish dates, categories, tags, and all metadata" {
		t.Fatalf("items[0] = %q, want parent workstream prefix stripped", items[0])
	}
	if items[1] != "Store in the Sam.blog git repo as Markdown with YAML frontmatter for clean migration into the new site" {
		t.Fatalf("items[1] = %q, want executable storage deliverable", items[1])
	}
}

func TestExtractDeliverablesIgnoresTaskMetadataLines(t *testing.T) {
	description := "Define success metrics and KPIs for Sam.blog. What does success look like in 3, 6, and 12 months? Metrics should cover: traffic/engagement, content quality signals, conversion (speaking inquiries, consulting leads, recruiter outreach), SEO performance, and social amplification. Include a measurement plan.\n\n**Output:** strategy/success-metrics.md\n**Agent:** Naomi\n**Estimated time:** 20 min\n**Depends on:** WS3.4"

	items := extractDeliverables(description)
	if len(items) != 1 {
		t.Fatalf("extractDeliverables len = %d, want 1 executable deliverable", len(items))
	}
	if items[0] != "Define success metrics and KPIs for Sam.blog. What does success look like in 3, 6, and 12 months? Metrics should cover: traffic/engagement, content quality signals, conversion (speaking inquiries, consulting leads, recruiter outreach), SEO performance, and social amplification. Include a measurement plan." {
		t.Fatalf("items[0] = %q, want only the executable content", items[0])
	}
}

func TestExtractDeliverablesIgnoresBareTimingNotes(t *testing.T) {
	description := strings.Join([]string{
		"Use the browser to visit technonymous.org",
		"Navigate the site's archive pages, category pages, and any pagination to build a complete index of every blog post URL",
		"Validate completeness by checking multiple navigation paths (archives by date, by category, sitemap if available)",
		"~30 min, browser-heavy.",
		"~60 min, browser-heavy.",
	}, "\n")

	items := extractDeliverables(description)
	if len(items) != 3 {
		t.Fatalf("extractDeliverables len = %d, want 3 executable deliverables", len(items))
	}
	for _, item := range items {
		if strings.Contains(item, "~30 min") || strings.Contains(item, "~60 min") {
			t.Fatalf("timing note leaked into deliverables: %q", item)
		}
	}
}

func TestExtractDeliverablesIgnoresTemplateCompanionGuidanceLines(t *testing.T) {
	description := strings.Join([]string{
		"Create a standalone HTML+CSS template for Sam.blog with a minimalist editorial design direction",
		"Clean typography, generous whitespace, reading-focused layout",
		"Sections: hero with tagline, featured post, post grid, about blurb, photography showcase strip, footer with social links",
		"Target: ~30 min.",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Create a standalone HTML+CSS template for Sam.blog with a minimalist editorial design direction",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
	}
}

func TestExtractDeliverablesIgnoresMarkdownDecoratedMetadataLines(t *testing.T) {
	description := strings.Join([]string{
		"Navigate technonymous.org, find the blog archive/index, and build a complete inventory of every blog post URL with title and date.",
		"**Assigned to:** Riku",
		"**Est. time:** 30 min (tool-heavy: browser scraping)",
	}, "\n")

	items := extractDeliverables(description)
	if len(items) != 1 {
		t.Fatalf("extractDeliverables len = %d, want 1 executable deliverable", len(items))
	}
	if strings.Contains(strings.ToLower(items[0]), "assigned to") || strings.Contains(strings.ToLower(items[0]), "est. time") {
		t.Fatalf("metadata line leaked into deliverables: %q", items[0])
	}
}

func TestExtractDeliverablesDoesNotSplitDefineListsInsideParentheses(t *testing.T) {
	description := strings.Join([]string{
		"Define color palette options (3-4 schemes that feel \"personal brand, not corporate\")",
		"Define typography scale (heading sizes, body text, captions)",
		"Define spacing scale",
		"Define breakpoints",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Define color palette options (3-4 schemes that feel \"personal brand, not corporate\")",
		"Define typography scale (heading sizes, body text, captions)",
		"Define spacing scale",
		"Define breakpoints",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
	}
}

func TestExtractDeliverablesIgnoresBareEstPrefixTimingNotes(t *testing.T) {
	description := strings.Join([]string{
		"Generate blog post concepts 1-10 (of 20). Each concept includes: title, pillar alignment, target audience, key angle/thesis, outline (3-5 sections), estimated word count, SEO keywords.",
		"Est: ~25 min",
		"Est: ~30 min",
	}, "\n")

	items := extractDeliverables(description)
	if len(items) != 1 {
		t.Fatalf("extractDeliverables len = %d, want 1 executable deliverable", len(items))
	}
	if strings.Contains(strings.ToLower(items[0]), "est:") {
		t.Fatalf("timing note leaked into deliverables: %q", items[0])
	}
}

func TestExtractDeliverablesIgnoresExplanatoryCompanionSentences(t *testing.T) {
	description := strings.Join([]string{
		"Use the browser to navigate technonymous.org",
		"This is the foundation for all subsequent scraping tasks.",
		"Build HTML templates 1-3 for Sam.blog",
		"Each is a self-contained HTML file with embedded CSS.",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Use the browser to navigate technonymous.org",
		"Build HTML templates 1-3 for Sam.blog",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
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

func TestPrepareQueueDecompositionAutoAppliesForCompoundWorkWithoutExplicitMode(t *testing.T) {
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
	if !result.Applied {
		t.Fatal("Applied = false, want true for compound work without explicit mode")
	}
	if len(result.ChildDrafts) < 1 {
		t.Fatalf("ChildDrafts len = %d, want >= 1", len(result.ChildDrafts))
	}
	if got := result.ChildDrafts[0].Title; got != "Rewrite and validate all media URLs while uploading assets into object storage with stable redirect coverage." {
		t.Fatalf("ChildDrafts[0].Title = %q, want deliverable-derived child title", got)
	}
}

func TestPrepareQueueDecompositionRejectsOversizedUnsplittableWork(t *testing.T) {
	description := "Create the full launch strategy packet that covers customer research synthesis and messaging framework and positioning rationale and editorial pillar selection and rollout sequencing and stakeholder communication in one end-to-end document for the Sam.blog relaunch without breaking it into separate reviewable work units."

	_, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "Launch strategy packet",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrBoundedTaskTooLarge) {
		t.Fatalf("PrepareQueueDecomposition err = %v, want ErrBoundedTaskTooLarge", err)
	}
}

func TestPrepareQueueDecompositionRejectsOversizedGeneratedChild(t *testing.T) {
	description := strings.Join([]string{
		"- Establish voice guidelines consistent with Sam's existing Technonymous writing.",
		"- Define cross-promotion and distribution strategy across all channels.",
	}, "\n")

	_, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "WS3: Develop Comprehensive Content Strategy",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrBoundedTaskTooLarge) {
		t.Fatalf("PrepareQueueDecomposition err = %v, want ErrBoundedTaskTooLarge when generated child remains oversized", err)
	}
}

func TestPrepareQueueDecompositionAutoAppliesForPhotographyArchiveWorkstream(t *testing.T) {
	description := strings.Join([]string{
		"ORCHESTRATION PARENT - do not execute directly.",
		"Design the structure for showcasing Sam's photography within the site.",
		"Define categories, presentation approach, gallery layout concepts, and integration with the overall site narrative.",
		"Commit to git repo under photography/.",
	}, "\n")

	result, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "WS5: Photography Archive — Portfolio Structure & Design",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("PrepareQueueDecomposition: %v", err)
	}
	if !result.Applied {
		t.Fatal("Applied = false, want true for photography archive workstream")
	}
	if len(result.ChildDrafts) < 3 {
		t.Fatalf("ChildDrafts len = %d, want >= 3", len(result.ChildDrafts))
	}
}

func TestPrepareQueueDecompositionAutoAppliesForStrategySynthesisWorkstream(t *testing.T) {
	description := "Synthesize all strategy documents (personas, pillars, voice, positioning, calendar) into a master strategy brief. Include an executive summary and recommendations for the migrated Technonymous content integration."

	result, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "WS3.6: Synthesize master content strategy brief",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("PrepareQueueDecomposition: %v", err)
	}
	if !result.Applied {
		t.Fatal("Applied = false, want true for strategy synthesis workstream")
	}
	if len(result.ChildDrafts) < 3 {
		t.Fatalf("ChildDrafts len = %d, want >= 3", len(result.ChildDrafts))
	}
}

func TestExtractDeliverablesIgnoresInstructionOnlyRequirementLines(t *testing.T) {
	description := strings.Join([]string{
		"- Design and build layout templates 1-3 as self-contained HTML files with embedded CSS",
		"- Each should have a distinct visual identity",
		"- Template 1: Clean minimal/editorial",
		"- Template 2: Magazine-style grid layout",
		"- Template 3: Dark mode technical/modern",
		"- Each must include sections for: hero/intro, blog listing, post view, photography gallery, about/bio, speaking/consulting CTA, and contact",
	}, "\n")

	got := extractDeliverables(description)
	want := []string{
		"Design and build layout templates 1-3 as self-contained HTML files with embedded CSS",
		"Template 1: Clean minimal/editorial",
		"Template 2: Magazine-style grid layout",
		"Template 3: Dark mode technical/modern",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", got, want)
	}
}

func TestExtractDeliverablesSplitsMarkdownEnumeratedDesignDirections(t *testing.T) {
	description := "Four distinctly different design directions:\\n\\n7. **Card-Based Dashboard** — Content organized as cards/tiles, modern SaaS-inspired, scannable\\n8. **Storytelling Scroll** — Single long-scroll narrative page, parallax-like, immersive\\n9. **News/Commentary** — Op-ed publication style, NYT/Atlantic-inspired, authority-first\\n10. **Hybrid Creative** — Blends photography portfolio with essay archive, creative agency feel\\n\\nSave to `templates/` directory"

	got := extractDeliverables(description)
	want := []string{
		"Card-Based Dashboard — Content organized as cards/tiles, modern SaaS-inspired, scannable",
		"Storytelling Scroll — Single long-scroll narrative page, parallax-like, immersive",
		"News/Commentary — Op-ed publication style, NYT/Atlantic-inspired, authority-first",
		"Hybrid Creative — Blends photography portfolio with essay archive, creative agency feel",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", got, want)
	}
}

func TestParseDecompositionReferences(t *testing.T) {
	parentID := uuid.New()
	childID := uuid.New()
	metadata := json.RawMessage(`{
		"decomposition_parent_task_id":"` + parentID.String() + `",
		"workstream_index":2,
		"decomposition":{
			"child_task_ids":["` + childID.String() + `"]
		}
	}`)

	if got := ParseParentTaskID(metadata); got != parentID {
		t.Fatalf("ParseParentTaskID = %s, want %s", got, parentID)
	}
	if got, ok := ParseWorkstreamIndex(metadata); !ok || got != 2 {
		t.Fatalf("ParseWorkstreamIndex = (%d, %t), want (2, true)", got, ok)
	}
	childIDs := ParseChildTaskIDs(metadata)
	if len(childIDs) != 1 || childIDs[0] != childID {
		t.Fatalf("ParseChildTaskIDs = %v, want [%s]", childIDs, childID)
	}
}

func TestApplyChildMetadataSetsParentReference(t *testing.T) {
	parentID := uuid.New()
	metadata := ApplyChildMetadata(json.RawMessage(`{"preserve":"yes"}`), parentID, 4)

	if got := ParseParentTaskID(metadata); got != parentID {
		t.Fatalf("ParseParentTaskID = %s, want %s", got, parentID)
	}
	if got, ok := ParseWorkstreamIndex(metadata); !ok || got != 4 {
		t.Fatalf("ParseWorkstreamIndex = (%d, %t), want (4, true)", got, ok)
	}
}

func TestAppendChildTaskIDPreservesExistingChildIDs(t *testing.T) {
	firstChildID := uuid.New()
	secondChildID := uuid.New()
	metadata := ApplyMetadata(json.RawMessage(`{"preserve":"yes"}`), Plan{
		RequiresDecomposition: true,
		PrimaryDeliverable:    "Primary",
		Deliverables:          []string{"Primary", "Child"},
	}, "source", []uuid.UUID{firstChildID})

	updated := AppendChildTaskID(metadata, secondChildID)
	childIDs := ParseChildTaskIDs(updated)
	if len(childIDs) != 2 {
		t.Fatalf("ParseChildTaskIDs len = %d, want 2", len(childIDs))
	}
	if childIDs[0] != firstChildID || childIDs[1] != secondChildID {
		t.Fatalf("ParseChildTaskIDs = %v, want [%s %s]", childIDs, firstChildID, secondChildID)
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
