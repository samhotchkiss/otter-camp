package task

import (
	"testing"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRecoveryCheckpointFromSessionMessagesPrefersTaskDeliverableOverContextReads(t *testing.T) {
	taskRecord := repo.ProjectTask{
		ID:          uuid.New(),
		WorkStatus:  "blocked",
		Description: pointerString("## Deliverable\n`docs/blog-post-ideas.md`\n\nRead the content strategy before revising."),
	}
	messages := []repo.ChatMessage{
		{
			Role:    "tool_result",
			Content: `{"tool_name":"file.read","output":{"path":"content/writing/the-year-the-phone-started-talking-back/index.md"}}`,
		},
	}

	checkpoint, ok := recoveryCheckpointFromSessionMessages(taskRecord, messages, "content_required")
	if !ok {
		t.Fatal("expected checkpoint reconstruction to succeed")
	}
	if checkpoint.TargetPath != "docs/blog-post-ideas.md" {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, "docs/blog-post-ideas.md")
	}
}
