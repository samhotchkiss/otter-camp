package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type ConversationTokenHandler struct {
	Store *store.ConversationTokenStore
}

type roomTokenResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	TotalTokens int64  `json:"total_tokens"`
}

type conversationTokenResponse struct {
	ID          string `json:"id"`
	RoomID      string `json:"room_id"`
	Topic       string `json:"topic"`
	TotalTokens int64  `json:"total_tokens"`
}

type roomTokenSenderResponse struct {
	SenderID    string `json:"sender_id"`
	SenderType  string `json:"sender_type"`
	TotalTokens int64  `json:"total_tokens"`
}

type roomTokenStatsResponse struct {
	RoomID                   string                    `json:"room_id"`
	TotalTokens              int64                     `json:"total_tokens"`
	ConversationCount        int                       `json:"conversation_count"`
	AvgTokensPerConversation int64                     `json:"avg_tokens_per_conversation"`
	Last7DaysTokens          int64                     `json:"last_7_days_tokens"`
	TokensBySender           []roomTokenSenderResponse `json:"tokens_by_sender"`
}

func (h *ConversationTokenHandler) GetRoom(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	roomID := strings.TrimSpace(chi.URLParam(r, "id"))
	summary, err := h.Store.GetRoomTokenSummary(r.Context(), roomID)
	if err != nil {
		handleConversationTokenStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, roomTokenResponse{
		ID:          summary.ID,
		Name:        summary.Name,
		Type:        summary.Type,
		TotalTokens: summary.TotalTokens,
	})
}

func (h *ConversationTokenHandler) GetConversation(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	conversationID := strings.TrimSpace(chi.URLParam(r, "id"))
	summary, err := h.Store.GetConversationTokenSummary(r.Context(), conversationID)
	if err != nil {
		handleConversationTokenStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, conversationTokenResponse{
		ID:          summary.ID,
		RoomID:      summary.RoomID,
		Topic:       summary.Topic,
		TotalTokens: summary.TotalTokens,
	})
}

func (h *ConversationTokenHandler) GetRoomStats(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	roomID := strings.TrimSpace(chi.URLParam(r, "id"))
	stats, err := h.Store.GetRoomTokenStats(r.Context(), roomID)
	if err != nil {
		handleConversationTokenStoreError(w, err)
		return
	}

	senderStats := make([]roomTokenSenderResponse, 0, len(stats.TokensBySender))
	for _, sender := range stats.TokensBySender {
		senderStats = append(senderStats, roomTokenSenderResponse{
			SenderID:    sender.SenderID,
			SenderType:  sender.SenderType,
			TotalTokens: sender.TotalTokens,
		})
	}

	sendJSON(w, http.StatusOK, roomTokenStatsResponse{
		RoomID:                   stats.RoomID,
		TotalTokens:              stats.TotalTokens,
		ConversationCount:        stats.ConversationCount,
		AvgTokensPerConversation: stats.AvgTokensPerConversation,
		Last7DaysTokens:          stats.Last7DaysTokens,
		TokensBySender:           senderStats,
	})
}

func handleConversationTokenStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(err.Error())), "invalid "):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "token lookup failed"})
	}
}
