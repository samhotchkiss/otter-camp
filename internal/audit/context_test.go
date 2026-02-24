package audit

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPrincipalFromContext(t *testing.T) {
	t.Run("returns principal injected in context", func(t *testing.T) {
		principalID := uuid.New()
		ctx := WithPrincipal(context.Background(), " HUMAN ", principalID)

		principalType, gotID := PrincipalFromContext(ctx)
		if principalType != "human" {
			t.Fatalf("principalType = %q, want %q", principalType, "human")
		}
		if gotID != principalID {
			t.Fatalf("principalID = %s, want %s", gotID, principalID)
		}
	})

	t.Run("empty context returns zero values", func(t *testing.T) {
		principalType, principalID := PrincipalFromContext(context.Background())
		if principalType != "" {
			t.Fatalf("principalType = %q, want empty", principalType)
		}
		if principalID != uuid.Nil {
			t.Fatalf("principalID = %s, want nil uuid", principalID)
		}
	})

	t.Run("nil context returns zero values", func(t *testing.T) {
		principalType, principalID := PrincipalFromContext(nil)
		if principalType != "" {
			t.Fatalf("principalType = %q, want empty", principalType)
		}
		if principalID != uuid.Nil {
			t.Fatalf("principalID = %s, want nil uuid", principalID)
		}
	})
}
