package dispatch

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// TaskStatus represents the lifecycle of a dispatched task.
type TaskStatus string

const (
	TaskStatusQueued     TaskStatus = "queued"
	TaskStatusDispatched TaskStatus = "dispatched"
	TaskStatusRunning    TaskStatus = "running"
	TaskStatusComplete   TaskStatus = "complete"
	TaskStatusFailed     TaskStatus = "failed"
)

var (
	// ErrInvalidTaskStatusTransition is returned when a transition is not allowed.
	ErrInvalidTaskStatusTransition = errors.New("invalid task status transition")
	// ErrUnknownTaskStatus is returned when a status is not recognized.
	ErrUnknownTaskStatus = errors.New("unknown task status")
)

// InvalidTaskStatusTransitionError provides details about a rejected transition.
type InvalidTaskStatusTransitionError struct {
	From TaskStatus
	To   TaskStatus
}

func (e InvalidTaskStatusTransitionError) Error() string {
	return fmt.Sprintf("%s: %s -> %s", ErrInvalidTaskStatusTransition, e.From, e.To)
}

func (e InvalidTaskStatusTransitionError) Unwrap() error {
	return ErrInvalidTaskStatusTransition
}

// UnknownTaskStatusError indicates an unexpected status value.
type UnknownTaskStatusError struct {
	Status TaskStatus
}

func (e UnknownTaskStatusError) Error() string {
	return fmt.Sprintf("%s: %q", ErrUnknownTaskStatus, e.Status)
}

func (e UnknownTaskStatusError) Unwrap() error {
	return ErrUnknownTaskStatus
}

var validTaskStatusTransitions = map[TaskStatus]map[TaskStatus]struct{}{
	TaskStatusQueued: {
		TaskStatusDispatched: {},
	},
	TaskStatusDispatched: {
		TaskStatusRunning: {},
	},
	TaskStatusRunning: {
		TaskStatusComplete: {},
		TaskStatusFailed:   {},
	},
	TaskStatusComplete: {},
	TaskStatusFailed:   {},
}

// TaskStatusChange describes a status update, including time spent in the previous status.
type TaskStatusChange struct {
	TaskID         string
	From           TaskStatus
	To             TaskStatus
	At             time.Time
	DurationInFrom time.Duration
	Durations      map[TaskStatus]time.Duration
}

// TaskStatusTrackerOptions configures a TaskStatusTracker.
type TaskStatusTrackerOptions struct {
	Now  func() time.Time
	Emit func(TaskStatusChange)
}

// TaskStatusTracker manages task status transitions, emits events, and tracks duration per status.
type TaskStatusTracker struct {
	mu        sync.Mutex
	taskID    string
	status    TaskStatus
	enteredAt time.Time
	durations map[TaskStatus]time.Duration
	now       func() time.Time
	emit      func(TaskStatusChange)
}

// NewTaskStatusTracker creates a new TaskStatusTracker starting in the provided initial status.
func NewTaskStatusTracker(taskID string, initial TaskStatus, opts TaskStatusTrackerOptions) (*TaskStatusTracker, error) {
	if !isKnownTaskStatus(initial) {
		return nil, UnknownTaskStatusError{Status: initial}
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &TaskStatusTracker{
		taskID:    taskID,
		status:    initial,
		enteredAt: now(),
		durations: make(map[TaskStatus]time.Duration),
		now:       now,
		emit:      opts.Emit,
	}, nil
}

// Status returns the current task status.
func (t *TaskStatusTracker) Status() TaskStatus {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// Duration returns the total time spent in a status, including time spent in the current status (if applicable).
func (t *TaskStatusTracker) Duration(status TaskStatus) time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.durationLocked(status, t.now())
}

// Durations returns a snapshot of total time spent in each visited status, including the current status.
func (t *TaskStatusTracker) Durations() map[TaskStatus]time.Duration {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.durationsSnapshotLocked(t.now())
}

// Transition attempts to move the task from its current status to the provided status.
// If the status is unchanged, Transition is a no-op.
func (t *TaskStatusTracker) Transition(to TaskStatus) error {
	if t == nil {
		return errors.New("task status tracker is nil")
	}
	if !isKnownTaskStatus(to) {
		return UnknownTaskStatusError{Status: to}
	}

	var (
		emitFn func(TaskStatusChange)
		change TaskStatusChange
	)

	t.mu.Lock()
	from := t.status
	if from == to {
		t.mu.Unlock()
		return nil
	}
	if !isValidTaskStatusTransition(from, to) {
		t.mu.Unlock()
		return InvalidTaskStatusTransitionError{From: from, To: to}
	}

	now := t.now()
	delta := now.Sub(t.enteredAt)
	if delta < 0 {
		delta = 0
	}
	t.durations[from] += delta
	t.status = to
	t.enteredAt = now

	emitFn = t.emit
	change = TaskStatusChange{
		TaskID:         t.taskID,
		From:           from,
		To:             to,
		At:             now,
		DurationInFrom: delta,
		Durations:      t.durationsSnapshotLocked(now),
	}
	t.mu.Unlock()

	if emitFn != nil {
		emitFn(change)
	}
	return nil
}

func isKnownTaskStatus(status TaskStatus) bool {
	_, ok := validTaskStatusTransitions[status]
	return ok
}

func isValidTaskStatusTransition(from, to TaskStatus) bool {
	if from == "" || to == "" {
		return false
	}
	if from == to {
		return true
	}

	allowed, ok := validTaskStatusTransitions[from]
	if !ok {
		return false
	}
	_, ok = allowed[to]
	return ok
}

func (t *TaskStatusTracker) durationLocked(status TaskStatus, now time.Time) time.Duration {
	total := t.durations[status]
	if status != t.status {
		return total
	}

	delta := now.Sub(t.enteredAt)
	if delta < 0 {
		delta = 0
	}
	return total + delta
}

func (t *TaskStatusTracker) durationsSnapshotLocked(now time.Time) map[TaskStatus]time.Duration {
	if len(t.durations) == 0 {
		if t.status == "" {
			return map[TaskStatus]time.Duration{}
		}
		return map[TaskStatus]time.Duration{
			t.status: t.durationLocked(t.status, now),
		}
	}

	snapshot := make(map[TaskStatus]time.Duration, len(t.durations)+1)
	for status, dur := range t.durations {
		snapshot[status] = dur
	}
	if t.status != "" {
		snapshot[t.status] = t.durationLocked(t.status, now)
	}
	return snapshot
}
