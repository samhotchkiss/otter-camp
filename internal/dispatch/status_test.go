package dispatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskStatusTracker_TransitionsEventsAndDurations(t *testing.T) {
	t0 := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	now := t0

	var events []TaskStatusChange
	tracker, err := NewTaskStatusTracker("task-1", TaskStatusQueued, TaskStatusTrackerOptions{
		Now: func() time.Time { return now },
		Emit: func(change TaskStatusChange) {
			events = append(events, change)
		},
	})
	require.NoError(t, err)
	require.Equal(t, TaskStatusQueued, tracker.Status())

	// Invalid transition should not change status or emit events.
	now = t0.Add(5 * time.Second)
	err = tracker.Transition(TaskStatusRunning)
	require.ErrorAs(t, err, new(InvalidTaskStatusTransitionError))
	require.Equal(t, TaskStatusQueued, tracker.Status())
	require.Len(t, events, 0)
	require.Equal(t, 5*time.Second, tracker.Duration(TaskStatusQueued))

	// queued -> dispatched
	now = t0.Add(10 * time.Second)
	require.NoError(t, tracker.Transition(TaskStatusDispatched))

	// dispatched -> running
	now = t0.Add(30 * time.Second)
	require.NoError(t, tracker.Transition(TaskStatusRunning))

	// running -> complete
	now = t0.Add(45 * time.Second)
	require.NoError(t, tracker.Transition(TaskStatusComplete))
	require.Equal(t, TaskStatusComplete, tracker.Status())

	require.Len(t, events, 3)
	require.Equal(t, TaskStatusQueued, events[0].From)
	require.Equal(t, TaskStatusDispatched, events[0].To)
	require.Equal(t, 10*time.Second, events[0].DurationInFrom)
	require.Equal(t, TaskStatusDispatched, events[1].From)
	require.Equal(t, TaskStatusRunning, events[1].To)
	require.Equal(t, 20*time.Second, events[1].DurationInFrom)
	require.Equal(t, TaskStatusRunning, events[2].From)
	require.Equal(t, TaskStatusComplete, events[2].To)
	require.Equal(t, 15*time.Second, events[2].DurationInFrom)

	// Duration snapshots should accumulate time in each status.
	durations := tracker.Durations()
	require.Equal(t, 10*time.Second, durations[TaskStatusQueued])
	require.Equal(t, 20*time.Second, durations[TaskStatusDispatched])
	require.Equal(t, 15*time.Second, durations[TaskStatusRunning])
	require.Equal(t, 0*time.Second, durations[TaskStatusComplete])
	require.NotContains(t, durations, TaskStatusFailed)

	// Current status duration should include time since entering the status.
	now = t0.Add(60 * time.Second)
	require.Equal(t, 15*time.Second, tracker.Duration(TaskStatusComplete))
	require.Equal(t, 15*time.Second, tracker.Durations()[TaskStatusComplete])
}

func TestTaskStatusTracker_NoOpTransitionDoesNotEmit(t *testing.T) {
	t0 := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	now := t0

	var events []TaskStatusChange
	tracker, err := NewTaskStatusTracker("task-1", TaskStatusQueued, TaskStatusTrackerOptions{
		Now: func() time.Time { return now },
		Emit: func(change TaskStatusChange) {
			events = append(events, change)
		},
	})
	require.NoError(t, err)

	now = t0.Add(10 * time.Second)
	require.NoError(t, tracker.Transition(TaskStatusQueued))
	require.Equal(t, 10*time.Second, tracker.Duration(TaskStatusQueued))
	require.Len(t, events, 0)
}

func TestTaskStatusTracker_RejectsUnknownStatuses(t *testing.T) {
	tracker, err := NewTaskStatusTracker("task-1", TaskStatus("nope"), TaskStatusTrackerOptions{})
	require.ErrorAs(t, err, new(UnknownTaskStatusError))
	require.Nil(t, tracker)

	t0 := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)
	now := t0
	tracker, err = NewTaskStatusTracker("task-1", TaskStatusQueued, TaskStatusTrackerOptions{
		Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	err = tracker.Transition(TaskStatus("nope"))
	require.ErrorAs(t, err, new(UnknownTaskStatusError))
	require.Equal(t, TaskStatusQueued, tracker.Status())
}
