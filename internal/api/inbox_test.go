package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInboxHandler_DemoMode(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/inbox?demo=true", nil)
	rec := httptest.NewRecorder()

	HandleInbox(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp InboxResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Items)
}

func TestInboxHandler_MissingWorkspace(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/inbox", nil)
	rec := httptest.NewRecorder()

	HandleInbox(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp errorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "missing or invalid workspace", resp.Error)
}
