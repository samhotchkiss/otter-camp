package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRouterFeedEndpointUsesV2Handler(t *testing.T) {
	router := NewRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/feed?demo=true", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		ActionItems []map[string]interface{} `json:"actionItems"`
		FeedItems   []map[string]interface{} `json:"feedItems"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.NotEmpty(t, payload.ActionItems)
	require.NotEmpty(t, payload.FeedItems)
}

