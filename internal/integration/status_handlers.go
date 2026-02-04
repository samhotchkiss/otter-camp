// Package integration provides adapters for external integrations like OpenClaw.
package integration

import (
	"context"
	"database/sql"

	"github.com/samhotchkiss/otter-camp/internal/webhook"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

// OpenClawStatusHandler processes status updates coming from OpenClaw.
// It updates task state in the database and emits notifications over WebSocket.
type OpenClawStatusHandler struct {
	handler *webhook.StatusHandler
}

// NewOpenClawStatusHandler creates a new OpenClaw status handler.
func NewOpenClawStatusHandler(db *sql.DB, hub *ws.Hub) *OpenClawStatusHandler {
	return &OpenClawStatusHandler{
		handler: webhook.NewStatusHandler(db, hub),
	}
}

// HandleEvent processes a StatusEvent from OpenClaw.
func (h *OpenClawStatusHandler) HandleEvent(ctx context.Context, event webhook.StatusEvent) error {
	if h == nil || h.handler == nil {
		return nil
	}
	return h.handler.HandleEvent(ctx, event)
}
