package server

import "testing"

func TestProjectAutomaticFailureRestartState(t *testing.T) {
	t.Run("active restart available", func(t *testing.T) {
		status := "active"
		got := projectAutomaticFailureRestartState(1, 3, &status)
		if got != "restart available 1/3" {
			t.Fatalf("restart state = %q, want %q", got, "restart available 1/3")
		}
	})

	t.Run("retry budget exhausted", func(t *testing.T) {
		got := projectAutomaticFailureRestartState(3, 3, nil)
		if got != "retry budget exhausted 3/3" {
			t.Fatalf("restart state = %q, want %q", got, "retry budget exhausted 3/3")
		}
	})

	t.Run("restart target inactive", func(t *testing.T) {
		status := "archived"
		got := projectAutomaticFailureRestartState(2, 3, &status)
		if got != "restart target inactive 2/3" {
			t.Fatalf("restart state = %q, want %q", got, "restart target inactive 2/3")
		}
	})
}
