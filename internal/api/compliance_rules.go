package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type ComplianceRulesHandler struct {
	Store *store.ComplianceRuleStore
}

type complianceRulePayload struct {
	ID                   string  `json:"id"`
	OrgID                string  `json:"org_id"`
	ProjectID            *string `json:"project_id,omitempty"`
	Title                string  `json:"title"`
	Description          string  `json:"description"`
	CheckInstruction     string  `json:"check_instruction"`
	Category             string  `json:"category"`
	Severity             string  `json:"severity"`
	Enabled              bool    `json:"enabled"`
	SourceConversationID *string `json:"source_conversation_id,omitempty"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

type complianceRuleListResponse struct {
	Items []complianceRulePayload `json:"items"`
	Total int                     `json:"total"`
}

func (h *ComplianceRulesHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}

	var projectID *string
	if raw := strings.TrimSpace(r.URL.Query().Get("project_id")); raw != "" {
		projectID = &raw
	}

	rules, err := h.Store.ListApplicableRules(r.Context(), orgID, projectID)
	if err != nil {
		handleComplianceRuleStoreError(w, err)
		return
	}

	payloadItems := make([]complianceRulePayload, 0, len(rules))
	for _, rule := range rules {
		payloadItems = append(payloadItems, toComplianceRulePayload(rule))
	}

	sendJSON(w, http.StatusOK, complianceRuleListResponse{
		Items: payloadItems,
		Total: len(payloadItems),
	})
}

func (h *ComplianceRulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}

	var req struct {
		ProjectID            *string `json:"project_id"`
		Title                string  `json:"title"`
		Description          string  `json:"description"`
		CheckInstruction     string  `json:"check_instruction"`
		Category             string  `json:"category"`
		Severity             string  `json:"severity"`
		SourceConversationID *string `json:"source_conversation_id"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	created, err := h.Store.Create(r.Context(), store.CreateComplianceRuleInput{
		OrgID:                orgID,
		ProjectID:            req.ProjectID,
		Title:                req.Title,
		Description:          req.Description,
		CheckInstruction:     req.CheckInstruction,
		Category:             req.Category,
		Severity:             req.Severity,
		SourceConversationID: req.SourceConversationID,
	})
	if err != nil {
		handleComplianceRuleStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusCreated, toComplianceRulePayload(*created))
}

func (h *ComplianceRulesHandler) Patch(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}

	ruleID := strings.TrimSpace(chi.URLParam(r, "id"))
	if ruleID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "rule id is required"})
		return
	}

	var req struct {
		Title            *string `json:"title"`
		Description      *string `json:"description"`
		CheckInstruction *string `json:"check_instruction"`
		Category         *string `json:"category"`
		Severity         *string `json:"severity"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	updated, err := h.Store.Update(r.Context(), orgID, ruleID, store.UpdateComplianceRuleInput{
		Title:            req.Title,
		Description:      req.Description,
		CheckInstruction: req.CheckInstruction,
		Category:         req.Category,
		Severity:         req.Severity,
	})
	if err != nil {
		handleComplianceRuleStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, toComplianceRulePayload(*updated))
}

func (h *ComplianceRulesHandler) Disable(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}

	ruleID := strings.TrimSpace(chi.URLParam(r, "id"))
	if ruleID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "rule id is required"})
		return
	}

	if err := h.Store.SetEnabled(r.Context(), orgID, ruleID, false); err != nil {
		handleComplianceRuleStoreError(w, err)
		return
	}

	updated, err := h.Store.GetByID(r.Context(), orgID, ruleID)
	if err != nil {
		handleComplianceRuleStoreError(w, err)
		return
	}
	sendJSON(w, http.StatusOK, toComplianceRulePayload(*updated))
}

func toComplianceRulePayload(rule store.ComplianceRule) complianceRulePayload {
	return complianceRulePayload{
		ID:                   rule.ID,
		OrgID:                rule.OrgID,
		ProjectID:            rule.ProjectID,
		Title:                strings.TrimSpace(rule.Title),
		Description:          strings.TrimSpace(rule.Description),
		CheckInstruction:     strings.TrimSpace(rule.CheckInstruction),
		Category:             strings.TrimSpace(rule.Category),
		Severity:             strings.TrimSpace(rule.Severity),
		Enabled:              rule.Enabled,
		SourceConversationID: rule.SourceConversationID,
		CreatedAt:            rule.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:            rule.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func handleComplianceRuleStoreError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}

	switch {
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}

	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(lower, "invalid ") ||
		strings.Contains(lower, "required") ||
		strings.Contains(lower, "does not belong to org") ||
		strings.Contains(lower, "at least one field") {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
}
