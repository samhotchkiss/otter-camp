package task

import (
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
)

func TestNormalizeTaskDescriptionOutputPathsContentStrategy(t *testing.T) {
	description := "## File Output\n`strategy/content-strategy.md`\n`strategy/audience-personas.md`\n`strategy/editorial-calendar-framework.md`\n"

	normalized := NormalizeTaskDescriptionOutputPaths("Content Strategy — Editorial Framework for Sam.blog", description)
	if strings.Contains(normalized, "strategy/content-strategy.md") {
		t.Fatalf("normalized description still contains legacy strategy path: %q", normalized)
	}
	if !strings.Contains(normalized, "`docs/content-strategy.md`") {
		t.Fatalf("normalized description missing canonical docs path: %q", normalized)
	}
	if !strings.Contains(normalized, "`docs/audience-personas.md`") {
		t.Fatalf("normalized description missing audience personas docs path: %q", normalized)
	}
	if !strings.Contains(normalized, "`docs/editorial-calendar-framework.md`") {
		t.Fatalf("normalized description missing editorial calendar docs path: %q", normalized)
	}
}

func TestNormalizeTaskDescriptionOutputPathsBlogIdeas(t *testing.T) {
	description := "## File Output\n`content/blog-post-ideas.md`\n"

	normalized := NormalizeTaskDescriptionOutputPaths("New Blog Post Ideas — 20 Concepts", description)
	if strings.Contains(normalized, "content/blog-post-ideas.md") {
		t.Fatalf("normalized description still contains legacy content path: %q", normalized)
	}
	if !strings.Contains(normalized, "`docs/blog-post-ideas.md`") {
		t.Fatalf("normalized description missing canonical docs path: %q", normalized)
	}
}

func TestNormalizeRecoveryCheckpointForTaskCanonicalizesLegacyPaths(t *testing.T) {
	checkpoint := NormalizeRecoveryCheckpointForTask("Content Strategy — Editorial Framework for Sam.blog", taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   "strategy/content-strategy.md",
		ArtifactPath: ".ottercamp/recovery/strategy/content-strategy.md",
	})

	if checkpoint.TargetPath != "docs/content-strategy.md" {
		t.Fatalf("TargetPath = %q, want %q", checkpoint.TargetPath, "docs/content-strategy.md")
	}
	if checkpoint.ArtifactPath != ".ottercamp/recovery/docs/content-strategy.md" {
		t.Fatalf("ArtifactPath = %q, want %q", checkpoint.ArtifactPath, ".ottercamp/recovery/docs/content-strategy.md")
	}
}
