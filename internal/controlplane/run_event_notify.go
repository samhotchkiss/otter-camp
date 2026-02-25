package controlplane

import (
	"strings"

	"github.com/google/uuid"
)

// RunEventsChannel returns the LISTEN/NOTIFY channel name used for a run_event stream.
func RunEventsChannel(runID uuid.UUID) string {
	return "run_events_" + strings.ToLower(strings.TrimSpace(runID.String()))
}
