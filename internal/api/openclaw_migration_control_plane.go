package api

import (
	"database/sql"
	"net/http"
)

type OpenClawMigrationControlPlaneHandler struct {
	DB *sql.DB
}

func NewOpenClawMigrationControlPlaneHandler(db *sql.DB) *OpenClawMigrationControlPlaneHandler {
	return &OpenClawMigrationControlPlaneHandler{DB: db}
}

func (h *OpenClawMigrationControlPlaneHandler) Status(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}
	handleMigrationStatus(h.DB)(w, r)
}

func (h *OpenClawMigrationControlPlaneHandler) Run(w http.ResponseWriter, _ *http.Request) {
	sendJSON(w, http.StatusNotImplemented, errorResponse{Error: "not implemented"})
}

func (h *OpenClawMigrationControlPlaneHandler) Pause(w http.ResponseWriter, _ *http.Request) {
	sendJSON(w, http.StatusNotImplemented, errorResponse{Error: "not implemented"})
}

func (h *OpenClawMigrationControlPlaneHandler) Resume(w http.ResponseWriter, _ *http.Request) {
	sendJSON(w, http.StatusNotImplemented, errorResponse{Error: "not implemented"})
}
