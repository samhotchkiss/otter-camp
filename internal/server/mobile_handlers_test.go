package server

import (
	"context"
	"testing"
	"time"
)

func TestRunDashboardQueriesParallel(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	done := make(chan error, 1)

	query := func(name string) func(context.Context) error {
		return func(context.Context) error {
			started <- name
			<-release
			return nil
		}
	}

	go func() {
		done <- runDashboardQueriesParallel(context.Background(), query("inbox"), query("projects"), query("sessions"))
	}()

	seen := make(map[string]struct{}, 3)
	for len(seen) < 3 {
		select {
		case name := <-started:
			seen[name] = struct{}{}
		case <-time.After(300 * time.Millisecond):
			t.Fatalf("queries did not start in parallel; started=%v", seen)
		}
	}

	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDashboardQueriesParallel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("parallel query runner did not complete")
	}
}
