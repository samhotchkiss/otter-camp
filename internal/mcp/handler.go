package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type HTTPHandler struct {
	server *Server
	auth   Authenticator
}

func NewHTTPHandler(server *Server, auth Authenticator) http.Handler {
	if server == nil {
		server = NewServer()
	}
	return &HTTPHandler{server: server, auth: auth}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"name":   "otter-camp-mcp",
		})
		return
	case http.MethodPost:
		// continue below
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req rpcRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(&req); err != nil {
		writeJSONRPCError(w, http.StatusBadRequest, nil, -32700, "parse error")
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeJSONRPCError(w, http.StatusBadRequest, req.ID, -32600, "invalid request")
		return
	}

	identity, err := h.authenticate(r)
	if err != nil {
		writeAuthError(w, req.ID, err)
		return
	}

	resp := h.server.Handle(r.Context(), identity, req)
	status := http.StatusOK
	if resp.Error != nil && resp.Error.Code == -32601 {
		status = http.StatusNotImplemented
	}
	writeJSON(w, status, resp)
}

func (h *HTTPHandler) authenticate(r *http.Request) (Identity, error) {
	if h.auth == nil {
		return Identity{}, errors.New("authenticator not configured")
	}
	return h.auth.Authenticate(r.Context(), r)
}

func writeAuthError(w http.ResponseWriter, id json.RawMessage, err error) {
	msg := "authentication error"
	switch {
	case errors.Is(err, ErrMissingAuth), errors.Is(err, ErrInvalidAuth):
		msg = err.Error()
	default:
		if err != nil {
			msg = err.Error()
		}
	}
	writeJSONRPCError(w, http.StatusUnauthorized, id, -32001, msg)
}

func writeJSONRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
	writeJSON(w, status, resp)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
