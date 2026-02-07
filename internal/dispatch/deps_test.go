package dispatch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanDispatchSatisfied(t *testing.T) {
	deps := Dependencies{
		"task-b": {"task-a", "task-c"},
	}
	status := map[string]DepStatus{
		"task-a": StatusDone,
		"task-c": StatusDone,
	}

	can, err := CanDispatch("task-b", deps, status)
	require.NoError(t, err)
	require.True(t, can)
}

func TestCanDispatchBlocked(t *testing.T) {
	deps := Dependencies{
		"task-b": {"task-a", "task-c"},
	}
	status := map[string]DepStatus{
		"task-a": StatusDone,
		"task-c": StatusInProgress,
	}

	can, err := CanDispatch("task-b", deps, status)
	require.NoError(t, err)
	require.False(t, can)
}

func TestCanDispatchMissingStatus(t *testing.T) {
	deps := Dependencies{
		"task-b": {"task-a"},
	}
	status := map[string]DepStatus{}

	_, err := CanDispatch("task-b", deps, status)
	require.Error(t, err)
	require.IsType(t, &MissingDependencyStatusError{}, err)
}

func TestDetectCircular(t *testing.T) {
	deps := Dependencies{
		"task-a": {"task-b"},
		"task-b": {"task-c"},
		"task-c": {"task-a"},
	}

	err := DetectCircular(deps)
	require.Error(t, err)
	require.IsType(t, &CircularDependencyError{}, err)
}

func TestOnComplete(t *testing.T) {
	deps := Dependencies{
		"task-b": {"task-a"},
		"task-c": {"task-a", "task-b"},
		"task-d": {"task-a"},
	}
	status := map[string]DepStatus{
		"task-a": StatusInProgress,
		"task-b": StatusQueued,
		"task-c": StatusQueued,
		"task-d": StatusQueued,
	}

	ready, err := OnComplete("task-a", deps, status)
	require.NoError(t, err)
	require.Equal(t, []string{"task-b", "task-d"}, ready)
}

func TestOnCompleteCircular(t *testing.T) {
	deps := Dependencies{
		"task-a": {"task-b"},
		"task-b": {"task-a"},
	}
	status := map[string]DepStatus{
		"task-a": StatusDone,
		"task-b": StatusQueued,
	}

	_, err := OnComplete("task-b", deps, status)
	require.Error(t, err)
	require.IsType(t, &CircularDependencyError{}, err)
}
