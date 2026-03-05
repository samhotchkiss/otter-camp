package repo

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestModelInvocationRepoClearProjectReferencesUsesProjectAndTaskReferences(t *testing.T) {
	projectID := uuid.New()

	var (
		capturedSQL  string
		capturedArgs []any
	)

	repo := &ModelInvocationRepo{
		db: &fakeChatExecutor{
			execFn: func(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
				capturedSQL = sql
				capturedArgs = arguments
				return pgconn.CommandTag{}, nil
			},
		},
	}

	if err := repo.ClearProjectReferences(context.Background(), projectID); err != nil {
		t.Fatalf("ClearProjectReferences: %v", err)
	}

	if !strings.Contains(capturedSQL, "UPDATE model_invocation") {
		t.Fatalf("sql = %q, want UPDATE model_invocation", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "project_id = NULL") {
		t.Fatalf("sql = %q, want project_id cleanup", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "project_task_id = NULL") {
		t.Fatalf("sql = %q, want project_task_id cleanup", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "project_task_id IN") {
		t.Fatalf("sql = %q, want project task subquery", capturedSQL)
	}
	if len(capturedArgs) != 1 {
		t.Fatalf("args len = %d, want 1", len(capturedArgs))
	}
	if arg, ok := capturedArgs[0].(uuid.UUID); !ok || arg != projectID {
		t.Fatalf("project id arg = %#v, want %s", capturedArgs[0], projectID)
	}
}
