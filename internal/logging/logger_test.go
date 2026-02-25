package logging

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestWithContextValues(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-1")
	ctx = WithOrgID(ctx, uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	logger := FromContext(ctx)
	if logger == nil {
		t.Fatal("expected logger")
	}
}
