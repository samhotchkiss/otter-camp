package jobqueue

import (
	"fmt"
	"strings"
	"time"
)

// DeferredJobError asks the worker to release the current claim and
// reschedule the same job for a future time without consuming an attempt.
type DeferredJobError struct {
	RunAfter time.Time
	Reason   string
}

func NewDeferredJobError(runAfter time.Time, reason string) error {
	return &DeferredJobError{
		RunAfter: runAfter.UTC(),
		Reason:   strings.TrimSpace(reason),
	}
}

func (e *DeferredJobError) Error() string {
	if e == nil {
		return "job deferred"
	}
	reason := strings.TrimSpace(e.Reason)
	if reason != "" {
		return reason
	}
	if e.RunAfter.IsZero() {
		return "job deferred"
	}
	return fmt.Sprintf("job deferred until %s", e.RunAfter.UTC().Format(time.RFC3339Nano))
}
