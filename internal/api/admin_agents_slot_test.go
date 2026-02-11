package api

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentSlotFromDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		displayName string
		expected    string
	}{
		{name: "simple", displayName: "Riley", expected: "riley"},
		{name: "spacing and punctuation", displayName: "  Riley Ops!!  ", expected: "riley-ops"},
		{name: "collapses separators", displayName: "Riley___Ops---Team", expected: "riley-ops-team"},
		{name: "fallback when no alnum", displayName: "***", expected: "agent"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, agentSlotFromDisplayName(tc.displayName))
		})
	}
}

func TestResolveAvailableAgentSlot(t *testing.T) {
	t.Parallel()

	t.Run("returns base when free", func(t *testing.T) {
		t.Parallel()
		slot, err := resolveAvailableAgentSlot("riley", func(slot string) (bool, error) {
			return false, nil
		})
		require.NoError(t, err)
		require.Equal(t, "riley", slot)
	})

	t.Run("adds numeric suffix when occupied", func(t *testing.T) {
		t.Parallel()
		existing := map[string]bool{
			"riley":   true,
			"riley-2": true,
		}
		slot, err := resolveAvailableAgentSlot("riley", func(slot string) (bool, error) {
			return existing[slot], nil
		})
		require.NoError(t, err)
		require.Equal(t, "riley-3", slot)
	})

	t.Run("propagates store lookup errors", func(t *testing.T) {
		t.Parallel()
		expectedErr := errors.New("boom")
		_, err := resolveAvailableAgentSlot("riley", func(string) (bool, error) {
			return false, expectedErr
		})
		require.ErrorIs(t, err, expectedErr)
	})
}
