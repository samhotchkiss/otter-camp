package tui

import (
	"testing"
	"time"
)

func TestDetectRuntimeHints(t *testing.T) {
	t.Run("tmux env", func(t *testing.T) {
		hints := DetectRuntimeHints(func(key string) string {
			if key == "TMUX" {
				return "/tmp/tmux-1000/default,123,0"
			}
			return ""
		})
		if !hints.ModifierReliabilityUncertain {
			t.Fatal("ModifierReliabilityUncertain = false, want true")
		}
	})

	t.Run("term screen fallback", func(t *testing.T) {
		hints := DetectRuntimeHints(func(key string) string {
			if key == "TERM" {
				return "screen-256color"
			}
			return ""
		})
		if !hints.ModifierReliabilityUncertain {
			t.Fatal("ModifierReliabilityUncertain = false, want true")
		}
	})

	t.Run("native terminal", func(t *testing.T) {
		hints := DetectRuntimeHints(func(string) string { return "" })
		if hints.ModifierReliabilityUncertain {
			t.Fatal("ModifierReliabilityUncertain = true, want false")
		}
		if hints.coldOpenDuration() <= 0 || hints.coldOpenDuration() > 1200*time.Millisecond {
			t.Fatalf("coldOpenDuration = %v, want 1ms..1200ms", hints.coldOpenDuration())
		}
		if hints.tourDuration() != 2*time.Minute {
			t.Fatalf("tourDuration = %v, want 2m", hints.tourDuration())
		}
		if hints.memoryBoundBytes() == 0 {
			t.Fatal("memoryBoundBytes() = 0, want > 0")
		}
	})

	t.Run("memory bound override", func(t *testing.T) {
		hints := DetectRuntimeHints(func(key string) string {
			if key == "OTTERCAMP_TUI_MEMORY_BOUND_MB" {
				return "96"
			}
			return ""
		})
		if got, want := hints.memoryBoundBytes(), uint64(96*1024*1024); got != want {
			t.Fatalf("memoryBoundBytes() = %d, want %d", got, want)
		}
	})

	t.Run("cold open clamp", func(t *testing.T) {
		hints := RuntimeHints{ColdOpenDuration: 5 * time.Second}
		if got, want := hints.coldOpenDuration(), 1200*time.Millisecond; got != want {
			t.Fatalf("coldOpenDuration() = %v, want %v", got, want)
		}
	})
}
