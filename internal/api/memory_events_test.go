package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func newMemoryEventsTestRouter(handler *MemoryEventsHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Get("/api/memory/events", handler.List)
	return r
}

func TestMemoryEventsHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "memory-events-handler-org-a")
	orgB := insertMessageTestOrganization(t, db, "memory-events-handler-org-b")

	eventsStore := store.NewMemoryEventsStore(db)
	_, err := eventsStore.Publish(context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgA), store.PublishMemoryEventInput{
		EventType: store.MemoryEventTypeMemoryCreated,
		Payload:   []byte(`{"memory_id":"a1"}`),
	})
	require.NoError(t, err)
	_, err = eventsStore.Publish(context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgA), store.PublishMemoryEventInput{
		EventType: store.MemoryEventTypeCompactionDetected,
		Payload:   []byte(`{"session":"s1"}`),
	})
	require.NoError(t, err)
	_, err = eventsStore.Publish(context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgB), store.PublishMemoryEventInput{
		EventType: store.MemoryEventTypeCompactionDetected,
		Payload:   []byte(`{"session":"s2"}`),
	})
	require.NoError(t, err)

	handler := &MemoryEventsHandler{Store: eventsStore}
	router := newMemoryEventsTestRouter(handler)

	filterReq := httptest.NewRequest(
		http.MethodGet,
		"/api/memory/events?org_id="+orgA+"&types=compaction.detected&limit=10",
		nil,
	)
	filterRec := httptest.NewRecorder()
	router.ServeHTTP(filterRec, filterReq)
	require.Equal(t, http.StatusOK, filterRec.Code)

	var filterResp memoryEventsListResponse
	require.NoError(t, json.NewDecoder(filterRec.Body).Decode(&filterResp))
	require.Equal(t, 1, filterResp.Total)
	require.Equal(t, store.MemoryEventTypeCompactionDetected, filterResp.Items[0].EventType)
	require.Contains(t, string(filterResp.Items[0].Payload), "s1")

	invalidSinceReq := httptest.NewRequest(
		http.MethodGet,
		"/api/memory/events?org_id="+orgA+"&since=not-a-time",
		nil,
	)
	invalidSinceRec := httptest.NewRecorder()
	router.ServeHTTP(invalidSinceRec, invalidSinceReq)
	require.Equal(t, http.StatusBadRequest, invalidSinceRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodGet, "/api/memory/events", nil)
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, missingWorkspaceRec.Code)
}

func TestMemoryEventsHandlerCapsLimit(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "memory-events-handler-limit-cap")

	eventsStore := store.NewMemoryEventsStore(db)
	publishCtx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	for i := 0; i < 1105; i += 1 {
		_, err := eventsStore.Publish(publishCtx, store.PublishMemoryEventInput{
			EventType: store.MemoryEventTypeMemoryCreated,
			Payload:   []byte(fmt.Sprintf(`{"memory_id":"id-%d"}`, i)),
		})
		require.NoError(t, err)
	}

	handler := &MemoryEventsHandler{Store: eventsStore}
	router := newMemoryEventsTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/memory/events?org_id="+orgID+"&limit=999999",
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp memoryEventsListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Items, 1000)
	require.Equal(t, 1000, resp.Total)
}
