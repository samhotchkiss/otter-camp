package taskdecomp

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
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

func TestAnalyzePrefersEnumeratedFetchBatchesOverProceduralToolSteps(t *testing.T) {
	description := strings.Join([]string{
		"Read the URL list from content/technonymous-index.json (already verified: 35 post URLs).",
		"",
		"For each URL:",
		"1. Use web_fetch to retrieve the page content as plain text",
		"2. Extract the post title, publication date (if available), and body content",
		"3. Format as a clean Markdown file with YAML front matter (title, date, source_url, slug)",
		"4. Save to content/posts/{slug}.md where slug is derived from the URL path segment (the last path component after /p/)",
		"",
		"Use cli_execute with shell scripting or iterate in the agent loop. Process all 35 posts.",
		"",
		"Expected output: 35 markdown files in content/posts/, each containing the full post content with front matter.",
		"",
		"IMPORTANT: Use ONLY web_fetch and file_write/cli_execute tools. Do NOT use browser_navigate, browser_click, or browser_extract_text.",
	}, "\n")

	plan := Analyze("Fetch all 35 technonymous.org posts via web_fetch and save as markdown files under content/posts/", &description)
	if !plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = false, want true")
	}
	want := []string{
		"Fetch technonymous.org posts via web_fetch and save as markdown files under content/posts/ 1-17",
		"Fetch technonymous.org posts via web_fetch and save as markdown files under content/posts/ 18-35",
	}
	if !reflect.DeepEqual(plan.Deliverables, want) {
		t.Fatalf("Deliverables = %v, want %v", plan.Deliverables, want)
	}
	for _, item := range plan.Deliverables {
		if strings.Contains(item, "Use web_fetch") || strings.Contains(item, "Use cli_execute") {
			t.Fatalf("procedural tool instruction leaked into deliverables: %q", item)
		}
	}
}

func TestTaskLooksProceduralInstructionArtifact(t *testing.T) {
	description := "Use cli_execute with shell scripting or iterate in the agent loop. Process all 35 posts."
	if !TaskLooksProceduralInstructionArtifact("Use cli_execute with shell scripting or iterate in the agent loop. Process all 35 posts.", &description) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for tool-procedure child task")
	}
	browserDescription := "Use browser tools to navigate to https://technonymous.org"
	if !TaskLooksProceduralInstructionArtifact("Use browser tools to navigate to https://technonymous.org", &browserDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for browser-navigation-only procedural child task")
	}
	realDescription := "Fetch posts 1-17 via web_fetch and save each result as markdown under content/posts/."
	if TaskLooksProceduralInstructionArtifact("Fetch technonymous.org posts 1-17 via web_fetch and save as markdown files under content/posts/", &realDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = true, want false for bounded batch child task")
	}
	boundedBrowserDescription := "Use browser tools to navigate technonymous.org, discover the site structure, identify all blog post URLs"
	if TaskLooksProceduralInstructionArtifact("Use browser tools to navigate technonymous.org, discover the site structure, identify all blog post URLs", &boundedBrowserDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = true, want false for bounded crawl deliverable")
	}
	referenceDescription := "Reference planning/sambot-feature-spec.md for feature requirements. Be specific and opinionated — recommend concrete tools/services, not just categories."
	if !TaskLooksProceduralInstructionArtifact("Reference planning/sambot-feature-spec.md for feature requirements. Be specific and opinionated — recommend concrete tools/services, not just categories.", &referenceDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for reference-only instruction child task")
	}
	supportDescription := "Use Sam's voice and opinions as established in the SamBot feature spec at planning/sambot-feature-spec.md and the scraped blog content in content/posts/."
	if !TaskLooksProceduralInstructionArtifact("Use Sam's voice and opinions as established in the SamBot feature spec at planning/sambot-feature-spec.md and the scraped blog content in content/posts/", &supportDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for context-only support child task")
	}
	requirementDescription := "No persistent server required — serverless is sufficient for MVP traffic."
	if !TaskLooksProceduralInstructionArtifact("No persistent server required — serverless is sufficient for MVP traffic", &requirementDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for support-only requirement fragment child task")
	}
	voiceDescription := "Opinionated, direct, technically rigorous — reflects Sam's writing voice."
	if !TaskLooksProceduralInstructionArtifact("Opinionated, direct, technically rigorous — reflects Sam's writing voice", &voiceDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for voice-only support fragment child task")
	}
	boundedSupportDescription := "Use content/technonymous-index.json and write the first 12 posts as markdown files under content/posts/."
	if TaskLooksProceduralInstructionArtifact("Use content/technonymous-index.json and write the first 12 posts as markdown files under content/posts/", &boundedSupportDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = true, want false for bounded deliverable task that names source artifacts")
	}

	fileConstraintDescription := "The file should be ~60-80 lines. No POST /api/sambot/chat yet - that is a sibling task."
	if !TaskLooksProceduralInstructionArtifact("The file should be ~60-80 lines. No POST /api/sambot/chat yet - that is a sibling task.", &fileConstraintDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for single-file checklist sizing fragment")
	}

	storeResultDescription := "Store result in a module-level STATIC_CONTEXT constant."
	if !TaskLooksProceduralInstructionArtifact("Store result in a module-level STATIC_CONTEXT constant.", &storeResultDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for single-file checklist storage fragment")
	}

	requireStatementsDescription := "Require statements: express, fs, path, and a placeholder const Anthropic = require('@anthropic-ai/sdk');"
	if !TaskLooksProceduralInstructionArtifact("Require statements: express, fs, path, and a placeholder const Anthropic = require('@anthropic-ai/sdk');", &requireStatementsDescription) {
		t.Fatal("TaskLooksProceduralInstructionArtifact = false, want true for single-file checklist require fragment")
	}
}

func TestExtractDeliverablesIgnoresReferenceOnlyInstructionLines(t *testing.T) {
	description := strings.Join([]string{
		"Write a standalone architecture and tech stack recommendation document for the \"Chat with SamBot\" feature.",
		"",
		"Deliverable: planning/sambot-architecture.md",
		"",
		"Cover the following sections:",
		"1. **Embedding Strategy** — How to generate embeddings from scraped blog posts in content/posts/, chunk sizing, metadata tagging",
		"2. **RAG Pipeline Design** — Retrieval-augmented generation flow: query → retrieval → context assembly → LLM prompt → response",
		"",
		"Reference the SamBot feature spec in planning/sambot-feature-spec.md for requirements context.",
	}, "\n")

	items := extractDeliverables(description)
	if len(items) < 2 {
		t.Fatalf("extractDeliverables len = %d, want >= 2 section deliverables: %v", len(items), items)
	}
	for _, expected := range []string{
		"Embedding Strategy — How to generate embeddings from scraped blog posts in content/posts/, chunk sizing, metadata tagging",
		"RAG Pipeline Design — Retrieval-augmented generation flow: query → retrieval → context assembly → LLM prompt → response",
	} {
		if !slices.Contains(items, expected) {
			t.Fatalf("extractDeliverables = %v, want numbered section deliverable %q", items, expected)
		}
	}
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item), "reference planning/sambot-feature-spec.md") {
			t.Fatalf("reference-only instruction leaked into deliverables: %q", item)
		}
	}
}

func TestValidateExecutableTaskContractRejectsProceduralInstructionArtifact(t *testing.T) {
	description := "Use browser_extract_text to get the page content and identify post links."
	err := ValidateExecutableTaskContract("Use browser tools to navigate to https://technonymous.org", &description)
	if !errors.Is(err, ErrExecutableTaskContractRequired) {
		t.Fatalf("ValidateExecutableTaskContract err = %v, want ErrExecutableTaskContractRequired", err)
	}
	if err == nil || !strings.Contains(err.Error(), "deliverable-focused bounded task") {
		t.Fatalf("error = %v, want deliverable-focused rewrite guidance", err)
	}
}

func TestValidateExecutableTaskContractAllowsBoundedDeliverableTask(t *testing.T) {
	description := "Fetch posts 1-12 from content/technonymous-index.json and save each as markdown under content/posts/ with valid frontmatter."
	if err := ValidateExecutableTaskContract("Fetch posts 1-12 from content/technonymous-index.json and save as markdown in content/posts/", &description); err != nil {
		t.Fatalf("ValidateExecutableTaskContract err = %v, want nil", err)
	}
}

func TestAnalyzePrefersLabelledEnumeratedDeliverables(t *testing.T) {
	description := "Create one persona per target reader segment."

	plan := Analyze("Write 3 audience personas for Sam.blog: AI/tech peers, parenting readers, curious generalists", &description)
	if !plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = false, want true")
	}
	want := []string{
		"Write audience persona for Sam.blog: AI/tech peers",
		"Write audience persona for Sam.blog: parenting readers",
		"Write audience persona for Sam.blog: curious generalists",
	}
	if !reflect.DeepEqual(plan.Deliverables, want) {
		t.Fatalf("Deliverables = %v, want %v", plan.Deliverables, want)
	}
}

func TestAnalyzeSplitsAmpersandLabelledEnumeratedDeliverables(t *testing.T) {
	description := "Create two targeted personas."

	plan := Analyze("Create 2 audience personas: speakers & consultants", &description)
	if !plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = false, want true")
	}
	want := []string{
		"Create audience persona: speakers",
		"Create audience persona: consultants",
	}
	if !reflect.DeepEqual(plan.Deliverables, want) {
		t.Fatalf("Deliverables = %v, want %v", plan.Deliverables, want)
	}
}

func TestAnalyzeSplitsUncountedAmpersandLabelledDeliverables(t *testing.T) {
	description := "Create targeted personas."

	plan := Analyze("Define audience personas: speakers & consultants", &description)
	if !plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = false, want true")
	}
	want := []string{
		"Define audience persona: speakers",
		"Define audience persona: consultants",
	}
	if !reflect.DeepEqual(plan.Deliverables, want) {
		t.Fatalf("Deliverables = %v, want %v", plan.Deliverables, want)
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

func TestExtractDeliverablesIgnoresDeferredQueueNotes(t *testing.T) {
	description := strings.Join([]string{
		"Produce final validation report with pass/fail determination, risk summary, and recommendations",
		"Deferred task-queued after all test scenarios complete.",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Produce final validation report with pass/fail determination, risk summary, and recommendations",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
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

func TestExtractDeliverablesIgnoresCompanionInstructionAndSizingLines(t *testing.T) {
	description := strings.Join([]string{
		"Use browser tools to navigate technonymous.org, discover the site structure, identify all blog post URLs",
		"Commit to repo.",
		"Tool-heavy browser work — up to 60 min.",
		"Research 5-7 exemplary personal brand sites (speakers, consultants, thought leaders)",
		"Document layout patterns, navigation structures, CTA placement, typography choices, and what makes each effective",
		"Save as design-templates/template-01.html through template-03.html",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Use browser tools to navigate technonymous.org, discover the site structure, identify all blog post URLs",
		"Research 5-7 exemplary personal brand sites (speakers, consultants, thought leaders)",
		"Document layout patterns, navigation structures, CTA placement, typography choices, and what makes each effective",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
	}
}

func TestExtractDeliverablesCollapsesSingleFileChecklistToPrimaryDeliverable(t *testing.T) {
	description := strings.Join([]string{
		"Create the file `sambot/api.js` with exactly these components:",
		"1. Require statements: express, fs, path, and a placeholder `const Anthropic = require('@anthropic-ai/sdk');`.",
		"2. Store result in a module-level `STATIC_CONTEXT` constant.",
		"3. The file should be ~60-80 lines. No POST /api/sambot/chat yet — that is a sibling task.",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Create the file sambot/api.js with exactly these components",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
	}
}

func TestExtractDeliverablesIgnoresIncludeFieldLists(t *testing.T) {
	description := strings.Join([]string{
		"Write blog post archive migration records in Markdown.",
		"Include title, date, full content or excerpt, post URL.",
		"Save as content/import-manifest.md",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Write blog post archive migration records in Markdown.",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
	}
}

func TestExtractDeliverablesIgnoresIncludeColonGuidance(t *testing.T) {
	description := strings.Join([]string{
		"Write the About page copy for Sam.",
		"Include: who Sam is, what the blog covers, why readers should care.",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Write the About page copy for Sam.",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
	}
}

func TestExtractDeliverablesIgnoresDesignGuidanceAndProcessCompanionLines(t *testing.T) {
	description := strings.Join([]string{
		"Create a complete self-contained HTML template at templates/option-04-photo-forward/index.html",
		"Visual-first aesthetic where photography drives the layout",
		"Large image areas, gallery-style sections",
		"Embedded CSS, responsive",
		"Must include all standard sections with photography given prominence.",
		"Save each as markdown with YAML frontmatter at content/migrated-posts/YYYY-MM-DD-slug.md",
		"Commit in batches of 5-10 posts",
		"Build migration manifest and validate completeness",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{
		"Create a complete self-contained HTML template at templates/option-04-photo-forward/index.html",
		"Build migration manifest and validate completeness",
	}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
	}
}

func TestExtractDeliverablesIgnoresProceduralStepsAndImportantSections(t *testing.T) {
	description := strings.Join([]string{
		"## Objective",
		"Crawl technonymous.org and produce a JSON index file at `content/technonymous-index.json`.",
		"",
		"## Deliverable",
		"A single file: `content/technonymous-index.json`",
		"",
		"## Steps",
		"1. Use browser tools to navigate to https://technonymous.org",
		"2. Extract all blog post URLs, titles, dates, and any summary/excerpt available from the site's archive/index pages",
		"3. If the site has pagination, follow all pages to get the complete list",
		"4. Write the result as a JSON array to `content/technonymous-index.json`",
		"5. Commit the file",
		"",
		"## Important",
		"- This is a CONCRETE DELIVERABLE task. The output is a JSON file, not a planning document.",
		"- Use browser_navigate, browser_extract_text, browser_click to crawl the site",
		"- Use cli_execute with python3 to write the file if file_write is intercepted",
		"- Do NOT produce planning artifacts — produce the JSON file directly",
		"- The file MUST be at exactly `content/technonymous-index.json`",
	}, "\n")

	items := extractDeliverables(description)
	if len(items) >= 2 {
		t.Fatalf("extractDeliverables len = %d, want < 2 so procedural sections do not trigger decomposition: %v", len(items), items)
	}
	for _, unexpected := range []string{
		"Use browser tools to navigate to https://technonymous.org",
		"Use browser_navigate, browser_extract_text, browser_click to crawl the site",
		"Use cli_execute with python3 to write the file if file_write is intercepted",
		"Do NOT produce planning artifacts — produce the JSON file directly",
	} {
		for _, item := range items {
			if item == unexpected {
				t.Fatalf("procedural guidance leaked into deliverables: %q", item)
			}
		}
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

func TestPrepareQueueDecompositionSplitsEnumeratedFetchTaskInsteadOfProceduralSteps(t *testing.T) {
	description := strings.Join([]string{
		"Read the URL list from content/technonymous-index.json (already verified: 35 post URLs).",
		"",
		"For each URL:",
		"1. Use web_fetch to retrieve the page content as plain text",
		"2. Extract the post title, publication date (if available), and body content",
		"3. Format as a clean Markdown file with YAML front matter (title, date, source_url, slug)",
		"4. Save to content/posts/{slug}.md where slug is derived from the URL path segment (the last path component after /p/)",
		"",
		"Use cli_execute with shell scripting or iterate in the agent loop. Process all 35 posts.",
		"",
		"Expected output: 35 markdown files in content/posts/, each containing the full post content with front matter.",
	}, "\n")

	result, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "Fetch all 35 technonymous.org posts via web_fetch and save as markdown files under content/posts/",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("PrepareQueueDecomposition: %v", err)
	}
	if !result.Applied {
		t.Fatal("Applied = false, want true")
	}
	if len(result.ChildDrafts) != 2 {
		t.Fatalf("ChildDrafts len = %d, want 2", len(result.ChildDrafts))
	}
	wantTitles := []string{
		"Fetch technonymous.org posts via web_fetch and save as markdown files under content/posts/ 1-17",
		"Fetch technonymous.org posts via web_fetch and save as markdown files under content/posts/ 18-35",
	}
	gotTitles := []string{result.ChildDrafts[0].Title, result.ChildDrafts[1].Title}
	if !reflect.DeepEqual(gotTitles, wantTitles) {
		t.Fatalf("ChildDraft titles = %v, want %v", gotTitles, wantTitles)
	}
	for _, child := range result.ChildDrafts {
		if strings.Contains(child.Title, "Use web_fetch") || strings.Contains(child.Title, "Use cli_execute") {
			t.Fatalf("procedural tool instruction leaked into child title: %q", child.Title)
		}
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
	if got := result.ChildDrafts[0].Title; got != "Migrate all legacy markdown posts into the new CMS schema with canonical slug preservation and author mapping." {
		t.Fatalf("ChildDrafts[0].Title = %q, want first validated persisted child title", got)
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

func TestPrepareQueueDecompositionRejectsGeneratedChildThatStillNeedsDecomposition(t *testing.T) {
	description := strings.Join([]string{
		"- Validate output/delivery stage: verify final output format, delivery mechanism, and consumer data completeness.",
		"- Produce validation-output.md.",
	}, "\n")

	_, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "WS2: Stage-by-Stage Pipeline Validation",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrBoundedTaskTooLarge) {
		t.Fatalf("PrepareQueueDecomposition err = %v, want ErrBoundedTaskTooLarge when generated child still requires decomposition", err)
	}
}

func TestValidateBoundedTaskSizeRejectsEnumeratedStrategyDefinitionTask(t *testing.T) {
	title := "Define the 5 content pillars with detailed descriptions: (1) Ethics & the Internet, (2) Parenting in a Digital World, (3) AI/Orchestration & Technical Deep-Dives, (4) Thought Leadership / Industry Commentary, (5) Photography Archive"
	description := title

	err := validateBoundedTaskSize(title, &description, false)
	if !errors.Is(err, ErrBoundedTaskTooLarge) {
		t.Fatalf("validateBoundedTaskSize err = %v, want ErrBoundedTaskTooLarge", err)
	}
}

func TestValidateBoundedTaskSizeRejectsBrandNarrativeStrategyTask(t *testing.T) {
	title := "Define the overarching brand narrative that ties the content pillars together and positions Sam.blog for speaking invitations, consulting inquiries, and premium job offers"
	description := title

	err := validateBoundedTaskSize(title, &description, false)
	if !errors.Is(err, ErrBoundedTaskTooLarge) {
		t.Fatalf("validateBoundedTaskSize err = %v, want ErrBoundedTaskTooLarge", err)
	}
}

func TestValidateBoundedTaskSizeAllowsParentScopedSinglePillarSlice(t *testing.T) {
	title := "Draft content pillar summary"
	description := "Write the one-page summary for the Ethics & the Internet pillar only."

	if err := validateBoundedTaskSize(title, &description, true); err != nil {
		t.Fatalf("validateBoundedTaskSize err = %v, want nil for bounded parent-scoped slice", err)
	}
}

func TestValidateBoundedTaskSizeAllowsSingleConcreteTemplateWithRequirements(t *testing.T) {
	title := "Build a single HTML layout template (template 8 of 10) for Sam.blog — replacement for blocked OC-38"
	description := strings.Join([]string{
		"Create a single self-contained HTML file at `templates/template-08-replace.html` for Sam.blog. This is template 8 of 10, replacing the blocked OC-38.",
		"",
		"Requirements:",
		"- Single HTML file, no JavaScript interactivity or build tooling required",
		"- Designed as a professional personal hub for Sam Hotchkiss",
		"- Unique visual identity distinct from templates 1-7 and 9-10",
		"- Sections: hero/intro, about, blog listing, photography, speaking/consulting CTA, SamBot chat placeholder, contact",
		"- Mobile responsive via CSS media queries",
		"- Deliverable: templates/template-08-replace.html",
	}, "\n")
	if !looksLikeSingleConcreteFileDeliverable(title, description, extractDeliverables(description)) {
		t.Fatalf(
			"looksLikeSingleConcreteFileDeliverable = false, titleSuggestsCompoundBoundedWork=%t broadScope=%t toolHeavy=%t external=%t paths=%v deliverables=%v",
			titleSuggestsCompoundBoundedWork(title),
			containsAny(strings.ToLower(strings.TrimSpace(strings.Join([]string{title, description}, " "))), broadScopeSignals),
			containsStandaloneSignal(strings.ToLower(strings.TrimSpace(strings.Join([]string{title, description}, " "))), toolHeavySignals),
			containsStandaloneSignal(strings.ToLower(strings.TrimSpace(strings.Join([]string{title, description}, " "))), externalBoundSignals),
			extractWorkspaceFilePaths(title+"\n"+description),
			extractDeliverables(description),
		)
	}

	if err := validateBoundedTaskSize(title, &description, false); err != nil {
		t.Fatalf("validateBoundedTaskSize err = %v, want nil for bounded single-file template task", err)
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

func TestPrepareQueueDecompositionSkipsConcreteDeliverableWithProceduralSections(t *testing.T) {
	description := strings.Join([]string{
		"## Objective",
		"Crawl technonymous.org and produce a JSON index file at `content/technonymous-index.json`.",
		"",
		"## Deliverable",
		"A single file: `content/technonymous-index.json`",
		"",
		"## Steps",
		"1. Use browser tools to navigate to https://technonymous.org",
		"2. Extract all blog post URLs, titles, dates, and any summary/excerpt available from the site's archive/index pages",
		"3. If the site has pagination, follow all pages to get the complete list",
		"4. Write the result as a JSON array to `content/technonymous-index.json` with this structure:",
		"5. Commit the file",
		"",
		"## Important",
		"- This is a CONCRETE DELIVERABLE task. The output is a JSON file, not a planning document.",
		"- Use browser_navigate, browser_extract_text, browser_click to crawl the site",
		"- Use cli_execute with python3 to write the file if file_write is intercepted",
		"- Do NOT produce planning artifacts — produce the JSON file directly",
		"- The file MUST be at exactly `content/technonymous-index.json`",
	}, "\n")

	result, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "Produce content/technonymous-index.json by crawling technonymous.org",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("PrepareQueueDecomposition: %v", err)
	}
	if result.Applied {
		t.Fatalf("Applied = true, want false for single concrete deliverable with procedural sections: %+v", result)
	}
	if len(result.ChildDrafts) != 0 {
		t.Fatalf("ChildDrafts len = %d, want 0", len(result.ChildDrafts))
	}
}

func TestPrepareQueueDecompositionSkipsSingleConcreteTemplateWithRequirements(t *testing.T) {
	description := strings.Join([]string{
		"Create a single self-contained HTML file at `templates/template-08-replace.html` for Sam.blog. This is template 8 of 10, replacing the blocked OC-38.",
		"",
		"Requirements:",
		"- Single HTML file, no JavaScript interactivity or build tooling required",
		"- Designed as a professional personal hub for Sam Hotchkiss",
		"- Unique visual identity distinct from templates 1-7 and 9-10",
		"- Sections: hero/intro, about, blog listing, photography, speaking/consulting CTA, SamBot chat placeholder, contact",
		"- Mobile responsive via CSS media queries",
		"- Deliverable: templates/template-08-replace.html",
	}, "\n")

	result, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "Build a single HTML layout template (template 8 of 10) for Sam.blog — replacement for blocked OC-38",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("PrepareQueueDecomposition: %v", err)
	}
	if result.Applied {
		t.Fatalf("Applied = true, want false for single concrete template deliverable: %+v", result)
	}
	if len(result.ChildDrafts) != 0 {
		t.Fatalf("ChildDrafts len = %d, want 0", len(result.ChildDrafts))
	}
}

func TestExtractDeliverablesIgnoresExactStepsCriticalRulesAndCodeFence(t *testing.T) {
	description := strings.Join([]string{
		"## Objective",
		"Produce the file `content/technonymous-index.json` containing an array of all blog post URLs found on technonymous.org.",
		"",
		"## Exact Steps",
		"1. Use `browser_navigate` to go to `https://technonymous.org`",
		"2. Use `browser_extract_text` to get the page content and identify post links",
		"3. If there is pagination or archive pages, follow those links to find all posts",
		"4. Collect every unique blog post URL (not category/tag/about pages — only actual posts)",
		"5. Write the file `content/technonymous-index.json` using `cli_execute` with python3:",
		"```",
		"python3 -c \"",
		"import os, json",
		"os.makedirs('content', exist_ok=True)",
		"urls = [... all discovered URLs ...]",
		"with open('content/technonymous-index.json', 'w') as f:",
		"    json.dump(urls, f, indent=2)",
		"print('Written', len(urls), 'URLs')",
		"\"",
		"```",
		"6. Verify the file exists with `cli_execute` running `cat content/technonymous-index.json`",
		"7. Commit and advance.",
		"",
		"## Critical Rules",
		"- Do NOT produce planning artifacts. The ONLY deliverable is the JSON file.",
		"- If `file_write` fails or gets redirected, use `cli_execute` with python3 as shown above.",
		"- The JSON file must be an array of URL strings, e.g.: `[\"https://technonymous.org/post-1\", \"https://technonymous.org/post-2\", ...]`",
		"- Do NOT decompose this task into subtasks. Execute it directly in a single session.",
	}, "\n")

	items := extractDeliverables(description)
	want := []string{"Produce the file content/technonymous-index.json containing an array of all blog post URLs found on technonymous.org."}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("extractDeliverables() = %v, want %v", items, want)
	}
}

func TestPrepareQueueDecompositionSkipsConcreteDeliverableWithExactStepsAndNoDecompose(t *testing.T) {
	description := strings.Join([]string{
		"## Objective",
		"Produce the file `content/technonymous-index.json` containing an array of all blog post URLs found on technonymous.org.",
		"",
		"## Exact Steps",
		"1. Use `browser_navigate` to go to `https://technonymous.org`",
		"2. Use `browser_extract_text` to get the page content and identify post links",
		"3. If there is pagination or archive pages, follow those links to find all posts",
		"4. Collect every unique blog post URL (not category/tag/about pages — only actual posts)",
		"5. Write the file `content/technonymous-index.json` using `cli_execute` with python3:",
		"```",
		"python3 -c \"",
		"import os, json",
		"os.makedirs('content', exist_ok=True)",
		"urls = [... all discovered URLs ...]",
		"with open('content/technonymous-index.json', 'w') as f:",
		"    json.dump(urls, f, indent=2)",
		"print('Written', len(urls), 'URLs')",
		"\"",
		"```",
		"6. Verify the file exists with `cli_execute` running `cat content/technonymous-index.json`",
		"7. Commit and advance.",
		"",
		"## Critical Rules",
		"- Do NOT produce planning artifacts. The ONLY deliverable is the JSON file.",
		"- If `file_write` fails or gets redirected, use `cli_execute` with python3 as shown above.",
		"- The JSON file must be an array of URL strings, e.g.: `[\"https://technonymous.org/post-1\", \"https://technonymous.org/post-2\", ...]`",
		"- Do NOT decompose this task into subtasks. Execute it directly in a single session.",
	}, "\n")

	result, err := PrepareQueueDecomposition(QueueDecompositionInput{
		ParentTaskID: uuid.New(),
		Title:        "Crawl technonymous.org homepage and write content/technonymous-index.json with all post URLs",
		Description:  &description,
		Metadata:     json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("PrepareQueueDecomposition: %v", err)
	}
	if result.Applied {
		t.Fatalf("Applied = true, want false for explicit no-decompose concrete deliverable: %+v", result)
	}
	if len(result.ChildDrafts) != 0 {
		t.Fatalf("ChildDrafts len = %d, want 0", len(result.ChildDrafts))
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
