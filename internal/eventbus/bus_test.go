package eventbus

import (
	"context"
	"errors"
	"testing"
)

func TestAdvanceOnHandlerResult(t *testing.T) {
	t.Run("success advances cursor", func(t *testing.T) {
		called := 0
		advanced, err := advanceOnHandlerResult(nil, func() error {
			called++
			return nil
		})
		if err != nil {
			t.Fatalf("advanceOnHandlerResult error = %v", err)
		}
		if !advanced {
			t.Fatal("expected advanced=true")
		}
		if called != 1 {
			t.Fatalf("advance callback count = %d, want 1", called)
		}
	})

	t.Run("handler error does not advance cursor", func(t *testing.T) {
		called := 0
		advanced, err := advanceOnHandlerResult(errors.New("boom"), func() error {
			called++
			return nil
		})
		if !errors.Is(err, errHandlerFailed) {
			t.Fatalf("error = %v, want errHandlerFailed", err)
		}
		if advanced {
			t.Fatal("expected advanced=false")
		}
		if called != 0 {
			t.Fatalf("advance callback count = %d, want 0", called)
		}
	})
}

func TestCallHandlerRecoversPanic(t *testing.T) {
	event := DomainEvent{}
	panicked, err := callHandler(context.Background(), func(context.Context, DomainEvent) error {
		panic("panic from handler")
	}, event)
	if !panicked {
		t.Fatal("expected panic to be recovered")
	}
	if err == nil {
		t.Fatal("expected panic error")
	}
}
