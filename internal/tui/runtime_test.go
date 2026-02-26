package tui

import "testing"

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
	})
}
