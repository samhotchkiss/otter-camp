package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type TaxonomyHandler struct {
	Store *store.EllieTaxonomyStore
}

type taxonomyNodeResponse struct {
	ID          string  `json:"id"`
	OrgID       string  `json:"org_id"`
	ParentID    *string `json:"parent_id,omitempty"`
	Slug        string  `json:"slug"`
	DisplayName string  `json:"display_name"`
	Description *string `json:"description,omitempty"`
	Depth       int     `json:"depth"`
}

type taxonomySubtreeMemoryResponse struct {
	MemoryID             string  `json:"memory_id"`
	Kind                 string  `json:"kind"`
	Title                string  `json:"title"`
	Content              string  `json:"content"`
	SourceConversationID *string `json:"source_conversation_id,omitempty"`
	SourceProjectID      *string `json:"source_project_id,omitempty"`
}

type taxonomyCreateNodeRequest struct {
	ParentID    *string `json:"parent_id"`
	Slug        string  `json:"slug"`
	DisplayName string  `json:"display_name"`
	Description *string `json:"description"`
}

func (h *TaxonomyHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	orgID, ok := taxonomyWorkspaceIDFromRequest(w, r)
	if !ok {
		return
	}
	if h == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "taxonomy store unavailable"})
		return
	}

	var parentID *string
	if raw := strings.TrimSpace(r.URL.Query().Get("parent_id")); raw != "" {
		parentID = &raw
	}

	nodes, err := h.Store.ListNodesByParent(r.Context(), orgID, parentID, 500)
	if err != nil {
		taxonomyStoreError(w, err, "failed to list taxonomy nodes")
		return
	}

	response := make([]taxonomyNodeResponse, 0, len(nodes))
	for _, node := range nodes {
		response = append(response, taxonomyNodeResponse{
			ID:          node.ID,
			OrgID:       node.OrgID,
			ParentID:    node.ParentID,
			Slug:        node.Slug,
			DisplayName: node.DisplayName,
			Description: node.Description,
			Depth:       node.Depth,
		})
	}
	sendJSON(w, http.StatusOK, map[string]any{"nodes": response})
}

func (h *TaxonomyHandler) CreateNode(w http.ResponseWriter, r *http.Request) {
	orgID, ok := taxonomyWorkspaceIDFromRequest(w, r)
	if !ok {
		return
	}
	if h == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "taxonomy store unavailable"})
		return
	}

	var req taxonomyCreateNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	node, err := h.Store.CreateNode(r.Context(), store.CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		ParentID:    req.ParentID,
		Slug:        req.Slug,
		DisplayName: req.DisplayName,
		Description: req.Description,
	})
	if err != nil {
		taxonomyStoreError(w, err, "failed to create taxonomy node")
		return
	}

	sendJSON(w, http.StatusCreated, taxonomyNodeResponse{
		ID:          node.ID,
		OrgID:       node.OrgID,
		ParentID:    node.ParentID,
		Slug:        node.Slug,
		DisplayName: node.DisplayName,
		Description: node.Description,
		Depth:       node.Depth,
	})
}

func (h *TaxonomyHandler) GetNode(w http.ResponseWriter, r *http.Request) {
	orgID, ok := taxonomyWorkspaceIDFromRequest(w, r)
	if !ok {
		return
	}
	if h == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "taxonomy store unavailable"})
		return
	}

	nodeID := strings.TrimSpace(chi.URLParam(r, "id"))
	node, err := h.Store.GetNodeByID(r.Context(), orgID, nodeID)
	if err != nil {
		taxonomyStoreError(w, err, "failed to get taxonomy node")
		return
	}

	sendJSON(w, http.StatusOK, taxonomyNodeResponse{
		ID:          node.ID,
		OrgID:       node.OrgID,
		ParentID:    node.ParentID,
		Slug:        node.Slug,
		DisplayName: node.DisplayName,
		Description: node.Description,
		Depth:       node.Depth,
	})
}

func (h *TaxonomyHandler) PatchNode(w http.ResponseWriter, r *http.Request) {
	orgID, ok := taxonomyWorkspaceIDFromRequest(w, r)
	if !ok {
		return
	}
	if h == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "taxonomy store unavailable"})
		return
	}

	nodeID := strings.TrimSpace(chi.URLParam(r, "id"))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawFields); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	_, hasParent := rawFields["parent_id"]
	_, hasDisplayName := rawFields["display_name"]
	_, hasDescription := rawFields["description"]
	if !hasParent && !hasDisplayName && !hasDescription {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "at least one field is required"})
		return
	}

	var parentID *string
	if hasParent {
		if err := json.Unmarshal(rawFields["parent_id"], &parentID); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid parent_id"})
			return
		}
	}

	var displayName *string
	if hasDisplayName {
		if err := json.Unmarshal(rawFields["display_name"], &displayName); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid display_name"})
			return
		}
	}

	var description *string
	if hasDescription {
		if err := json.Unmarshal(rawFields["description"], &description); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid description"})
			return
		}
	}

	if hasParent {
		if _, err := h.Store.ReparentNode(r.Context(), orgID, nodeID, parentID); err != nil {
			taxonomyStoreError(w, err, "failed to reparent taxonomy node")
			return
		}
	}

	if hasDisplayName || hasDescription {
		if _, err := h.Store.UpdateNodeDetails(r.Context(), orgID, nodeID, displayName, description, hasDescription); err != nil {
			taxonomyStoreError(w, err, "failed to update taxonomy node")
			return
		}
	}

	node, err := h.Store.GetNodeByID(r.Context(), orgID, nodeID)
	if err != nil {
		taxonomyStoreError(w, err, "failed to reload taxonomy node")
		return
	}

	sendJSON(w, http.StatusOK, taxonomyNodeResponse{
		ID:          node.ID,
		OrgID:       node.OrgID,
		ParentID:    node.ParentID,
		Slug:        node.Slug,
		DisplayName: node.DisplayName,
		Description: node.Description,
		Depth:       node.Depth,
	})
}

func (h *TaxonomyHandler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	orgID, ok := taxonomyWorkspaceIDFromRequest(w, r)
	if !ok {
		return
	}
	if h == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "taxonomy store unavailable"})
		return
	}

	nodeID := strings.TrimSpace(chi.URLParam(r, "id"))
	if err := h.Store.DeleteLeafNode(r.Context(), orgID, nodeID); err != nil {
		taxonomyStoreError(w, err, "failed to delete taxonomy node")
		return
	}

	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *TaxonomyHandler) ListSubtreeMemories(w http.ResponseWriter, r *http.Request) {
	orgID, ok := taxonomyWorkspaceIDFromRequest(w, r)
	if !ok {
		return
	}
	if h == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "taxonomy store unavailable"})
		return
	}

	nodeID := strings.TrimSpace(chi.URLParam(r, "id"))
	memories, err := h.Store.ListMemoriesBySubtree(r.Context(), orgID, nodeID, 200)
	if err != nil {
		taxonomyStoreError(w, err, "failed to list subtree memories")
		return
	}

	response := make([]taxonomySubtreeMemoryResponse, 0, len(memories))
	for _, memory := range memories {
		response = append(response, taxonomySubtreeMemoryResponse{
			MemoryID:             memory.MemoryID,
			Kind:                 memory.Kind,
			Title:                memory.Title,
			Content:              memory.Content,
			SourceConversationID: memory.SourceConversationID,
			SourceProjectID:      memory.SourceProjectID,
		})
	}

	sendJSON(w, http.StatusOK, map[string]any{"memories": response})
}

func taxonomyWorkspaceIDFromRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	orgID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return "", false
	}
	return orgID, true
}

func taxonomyStoreError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	case errors.Is(err, store.ErrConflict):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "conflict"})
	case strings.Contains(strings.ToLower(err.Error()), "invalid") || strings.Contains(strings.ToLower(err.Error()), "required"):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: fallback})
	}
}
