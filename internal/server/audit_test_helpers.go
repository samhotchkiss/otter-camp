package server

import (
	"context"
	"sync"

	"github.com/samhotchkiss/otter-camp/internal/audit"
)

type capturedAuditRecorder struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *capturedAuditRecorder) Record(_ context.Context, event audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, cloneAuditEvent(event))
	return nil
}

func (r *capturedAuditRecorder) RecordAsync(_ context.Context, event audit.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, cloneAuditEvent(event))
}

func (r *capturedAuditRecorder) Events() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make([]audit.Event, len(r.events))
	copy(events, r.events)
	return events
}

func cloneAuditEvent(event audit.Event) audit.Event {
	cloned := event
	if len(event.Metadata) == 0 {
		cloned.Metadata = map[string]any{}
		return cloned
	}
	cloned.Metadata = make(map[string]any, len(event.Metadata))
	for key, value := range event.Metadata {
		cloned.Metadata[key] = value
	}
	return cloned
}
