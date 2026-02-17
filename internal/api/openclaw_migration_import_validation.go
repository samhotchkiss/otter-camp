package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	importer "github.com/samhotchkiss/otter-camp/internal/import"
)

const (
	maxOpenClawMigrationImportBodyBytes       int64 = 4 * 1024 * 1024
	maxOpenClawMigrationImportAgentIdentities       = 1000
	maxOpenClawMigrationImportHistoryEvents         = 5000
)

type openClawMigrationImportAgentIdentityPayload struct {
	ID          string            `json:"id,omitempty"`
	Slug        string            `json:"slug,omitempty"`
	Name        string            `json:"name,omitempty"`
	DisplayName string            `json:"display_name,omitempty"`
	Workspace   string            `json:"workspace,omitempty"`
	Soul        string            `json:"soul,omitempty"`
	Identity    string            `json:"identity,omitempty"`
	Memory      string            `json:"memory,omitempty"`
	Tools       string            `json:"tools,omitempty"`
	SourceFiles map[string]string `json:"source_files,omitempty"`
}

type openClawMigrationImportAgentsRequest struct {
	Identities []openClawMigrationImportAgentIdentityPayload `json:"identities"`
}

type openClawMigrationImportBatchPayload struct {
	ID    string `json:"id"`
	Index int    `json:"index"`
	Total int    `json:"total"`
}

type openClawMigrationImportHistoryEventPayload struct {
	AgentSlug   string    `json:"agent_slug"`
	SessionID   string    `json:"session_id,omitempty"`
	SessionPath string    `json:"session_path,omitempty"`
	EventID     string    `json:"event_id,omitempty"`
	ParentID    string    `json:"parent_id,omitempty"`
	Role        string    `json:"role"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"created_at"`
	Line        int       `json:"line,omitempty"`
}

type openClawMigrationImportHistoryBatchRequest struct {
	UserID string                                       `json:"user_id"`
	Batch  openClawMigrationImportBatchPayload          `json:"batch"`
	Events []openClawMigrationImportHistoryEventPayload `json:"events"`
}

func decodeOpenClawMigrationImportRequest(w http.ResponseWriter, r *http.Request, target interface{}) (int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxOpenClawMigrationImportBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return http.StatusRequestEntityTooLarge, err
		}
		return http.StatusBadRequest, err
	}
	return 0, nil
}

func validateOpenClawMigrationImportAgentsRequest(req openClawMigrationImportAgentsRequest) error {
	if len(req.Identities) == 0 {
		return fmt.Errorf("identities must include at least one item")
	}
	if len(req.Identities) > maxOpenClawMigrationImportAgentIdentities {
		return fmt.Errorf("identities exceeds max items (%d)", maxOpenClawMigrationImportAgentIdentities)
	}
	for idx, identity := range req.Identities {
		id := firstNonEmpty(strings.TrimSpace(identity.ID), strings.TrimSpace(identity.Slug))
		if id == "" {
			return fmt.Errorf("identities[%d].id is required", idx)
		}
	}
	return nil
}

func validateOpenClawMigrationImportHistoryBatchRequest(req openClawMigrationImportHistoryBatchRequest) error {
	userID := strings.TrimSpace(req.UserID)
	if !uuidRegex.MatchString(userID) {
		return fmt.Errorf("user_id must be a UUID")
	}
	if strings.TrimSpace(req.Batch.ID) == "" {
		return fmt.Errorf("batch.id is required")
	}
	if req.Batch.Index <= 0 {
		return fmt.Errorf("batch.index must be >= 1")
	}
	if req.Batch.Total <= 0 {
		return fmt.Errorf("batch.total must be >= 1")
	}
	if req.Batch.Index > req.Batch.Total {
		return fmt.Errorf("batch.index must be <= batch.total")
	}
	if len(req.Events) == 0 {
		return fmt.Errorf("events must include at least one item")
	}
	if len(req.Events) > maxOpenClawMigrationImportHistoryEvents {
		return fmt.Errorf("events exceeds max items (%d)", maxOpenClawMigrationImportHistoryEvents)
	}
	for idx, event := range req.Events {
		if strings.TrimSpace(event.AgentSlug) == "" {
			return fmt.Errorf("events[%d].agent_slug is required", idx)
		}
		if strings.TrimSpace(event.Role) == "" {
			return fmt.Errorf("events[%d].role is required", idx)
		}
		if strings.TrimSpace(event.Body) == "" {
			return fmt.Errorf("events[%d].body is required", idx)
		}
		if event.CreatedAt.IsZero() {
			return fmt.Errorf("events[%d].created_at is required", idx)
		}
	}
	return nil
}

func mapOpenClawMigrationImportIdentities(
	identities []openClawMigrationImportAgentIdentityPayload,
) []importer.ImportedAgentIdentity {
	out := make([]importer.ImportedAgentIdentity, 0, len(identities))
	for _, identity := range identities {
		id := firstNonEmpty(strings.TrimSpace(identity.ID), strings.TrimSpace(identity.Slug))
		name := firstNonEmpty(strings.TrimSpace(identity.DisplayName), strings.TrimSpace(identity.Name))
		out = append(out, importer.ImportedAgentIdentity{
			ID:           id,
			Name:         name,
			WorkspaceDir: strings.TrimSpace(identity.Workspace),
			Soul:         identity.Soul,
			Identity:     identity.Identity,
			Memory:       identity.Memory,
			Tools:        identity.Tools,
			SourceFiles:  identity.SourceFiles,
		})
	}
	return out
}

func mapOpenClawMigrationImportEvents(
	events []openClawMigrationImportHistoryEventPayload,
) []importer.OpenClawSessionEvent {
	out := make([]importer.OpenClawSessionEvent, 0, len(events))
	for _, event := range events {
		out = append(out, importer.OpenClawSessionEvent{
			AgentSlug:   strings.TrimSpace(event.AgentSlug),
			SessionID:   strings.TrimSpace(event.SessionID),
			SessionPath: strings.TrimSpace(event.SessionPath),
			EventID:     strings.TrimSpace(event.EventID),
			ParentID:    strings.TrimSpace(event.ParentID),
			Role:        strings.TrimSpace(event.Role),
			Body:        event.Body,
			CreatedAt:   event.CreatedAt,
			Line:        event.Line,
		})
	}
	return out
}
