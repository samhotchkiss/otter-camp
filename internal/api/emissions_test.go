package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

func newEmissionsTestRouter(handler *EmissionsHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/emissions/recent", handler.Recent)
	router.With(middleware.OptionalWorkspace).Post("/api/emissions", handler.Ingest)
	return router
}

type fakeEmissionBroadcaster struct {
	orgBroadcasts   []string
	topicBroadcasts []string
	payloads        [][]byte
}

func (f *fakeEmissionBroadcaster) Broadcast(orgID string, payload []byte) {
	f.orgBroadcasts = append(f.orgBroadcasts, orgID)
	f.payloads = append(f.payloads, payload)
}

func (f *fakeEmissionBroadcaster) BroadcastTopic(orgID string, topic string, payload []byte) {
	f.topicBroadcasts = append(f.topicBroadcasts, orgID+":"+topic)
	f.payloads = append(f.payloads, payload)
}

func TestEmissionBufferPushRecentAndFilters(t *testing.T) {
	buffer := NewEmissionBuffer(3)
	projectA := "project-a"
	projectB := "project-b"
	issueA := "issue-a"

	buffer.Push(Emission{
		ID:         "1",
		SourceType: "agent",
		SourceID:   "agent-a",
		Kind:       "status",
		Summary:    "first",
		Timestamp:  time.Now().Add(-4 * time.Second),
	})
	buffer.Push(Emission{
		ID:         "2",
		SourceType: "agent",
		SourceID:   "agent-b",
		Kind:       "status",
		Summary:    "second",
		Scope:      &EmissionScope{ProjectID: &projectA, IssueID: &issueA},
		Timestamp:  time.Now().Add(-3 * time.Second),
	})
	buffer.Push(Emission{
		ID:         "3",
		SourceType: "bridge",
		SourceID:   "bridge-main",
		Kind:       "progress",
		Summary:    "third",
		Scope:      &EmissionScope{ProjectID: &projectB},
		Timestamp:  time.Now().Add(-2 * time.Second),
	})
	buffer.Push(Emission{
		ID:         "4",
		SourceType: "agent",
		SourceID:   "agent-a",
		Kind:       "milestone",
		Summary:    "fourth",
		Scope:      &EmissionScope{ProjectID: &projectA},
		Timestamp:  time.Now().Add(-1 * time.Second),
	})

	recent := buffer.Recent(10, EmissionFilter{})
	require.Len(t, recent, 3)
	require.Equal(t, "4", recent[0].ID)
	require.Equal(t, "3", recent[1].ID)
	require.Equal(t, "2", recent[2].ID)

	projectFiltered := buffer.Recent(10, EmissionFilter{ProjectID: projectA})
	require.Len(t, projectFiltered, 2)
	require.Equal(t, "4", projectFiltered[0].ID)
	require.Equal(t, "2", projectFiltered[1].ID)

	issueFiltered := buffer.Recent(10, EmissionFilter{IssueID: issueA})
	require.Len(t, issueFiltered, 1)
	require.Equal(t, "2", issueFiltered[0].ID)

	sourceFiltered := buffer.Recent(10, EmissionFilter{SourceID: "agent-a"})
	require.Len(t, sourceFiltered, 1)
	require.Equal(t, "4", sourceFiltered[0].ID)

	latest := buffer.LatestBySource("", "agent-a")
	require.NotNil(t, latest)
	require.Equal(t, "4", latest.ID)
}

func TestEmissionBufferConcurrentPushIsSafe(t *testing.T) {
	buffer := NewEmissionBuffer(500)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				normalized, err := normalizeEmission(Emission{
					SourceType: "agent",
					SourceID:   "agent-concurrent",
					Kind:       "log",
					Summary:    "concurrent",
					Timestamp:  time.Now().Add(time.Duration(worker*j) * time.Millisecond),
				})
				if err != nil {
					t.Errorf("normalizeEmission failed: %v", err)
					return
				}
				buffer.Push(normalized)
			}
		}(i)
	}
	wg.Wait()

	recent := buffer.Recent(500, EmissionFilter{})
	require.NotEmpty(t, recent)
	require.LessOrEqual(t, len(recent), 500)
	seenIDs := make(map[string]struct{}, len(recent))
	for _, emission := range recent {
		_, exists := seenIDs[emission.ID]
		require.False(t, exists, "duplicate emission id detected: %s", emission.ID)
		seenIDs[emission.ID] = struct{}{}
	}
	latest := buffer.LatestBySource("", "agent-concurrent")
	require.NotNil(t, latest)
}

func TestEmissionHandlerIngestAndRecent(t *testing.T) {
	buffer := NewEmissionBuffer(50)
	handler := &EmissionsHandler{Buffer: buffer}
	router := newEmissionsTestRouter(handler)
	orgID := "550e8400-e29b-41d4-a716-446655440000"

	projectID := "project-123"
	issueID := "issue-456"
	now := time.Now().UTC()
	ingestBody := map[string]any{
		"emissions": []map[string]any{
			{
				"id":          "em-1",
				"source_type": "agent",
				"source_id":   "agent-1",
				"kind":        "status",
				"summary":     "Started work",
				"timestamp":   now.Format(time.RFC3339),
				"scope": map[string]any{
					"project_id": projectID,
					"issue_id":   issueID,
				},
			},
			{
				"id":          "em-2",
				"source_type": "bridge",
				"source_id":   "bridge-main",
				"kind":        "progress",
				"summary":     "Sync complete",
				"timestamp":   now.Add(time.Second).Format(time.RFC3339),
				"progress": map[string]any{
					"current": 2,
					"total":   5,
					"unit":    "steps",
				},
			},
		},
	}
	rawBody, err := json.Marshal(ingestBody)
	require.NoError(t, err)

	ingestReq := httptest.NewRequest(http.MethodPost, "/api/emissions?org_id="+orgID, bytes.NewReader(rawBody))
	ingestRec := httptest.NewRecorder()
	router.ServeHTTP(ingestRec, ingestReq)
	require.Equal(t, http.StatusAccepted, ingestRec.Code)

	recentReq := httptest.NewRequest(http.MethodGet, "/api/emissions/recent?org_id="+orgID+"&limit=10", nil)
	recentRec := httptest.NewRecorder()
	router.ServeHTTP(recentRec, recentReq)
	require.Equal(t, http.StatusOK, recentRec.Code)

	var recentResp emissionListResponse
	require.NoError(t, json.NewDecoder(recentRec.Body).Decode(&recentResp))
	require.Len(t, recentResp.Items, 2)
	require.Equal(t, "em-2", recentResp.Items[0].ID)
	require.Equal(t, "em-1", recentResp.Items[1].ID)

	otherOrgReq := httptest.NewRequest(http.MethodGet, "/api/emissions/recent?org_id=660e8400-e29b-41d4-a716-446655440000&limit=10", nil)
	otherOrgRec := httptest.NewRecorder()
	router.ServeHTTP(otherOrgRec, otherOrgReq)
	require.Equal(t, http.StatusOK, otherOrgRec.Code)

	var otherOrgResp emissionListResponse
	require.NoError(t, json.NewDecoder(otherOrgRec.Body).Decode(&otherOrgResp))
	require.Len(t, otherOrgResp.Items, 0)

	scopedReq := httptest.NewRequest(http.MethodGet, "/api/emissions/recent?org_id="+orgID+"&project_id="+projectID+"&issue_id="+issueID, nil)
	scopedRec := httptest.NewRecorder()
	router.ServeHTTP(scopedRec, scopedReq)
	require.Equal(t, http.StatusOK, scopedRec.Code)

	var scopedResp emissionListResponse
	require.NoError(t, json.NewDecoder(scopedRec.Body).Decode(&scopedResp))
	require.Len(t, scopedResp.Items, 1)
	require.Equal(t, "em-1", scopedResp.Items[0].ID)

	invalidReq := httptest.NewRequest(http.MethodPost, "/api/emissions?org_id="+orgID, bytes.NewReader([]byte(`{"emissions":[{"source_type":"agent","source_id":"x","kind":"status","summary":"`+strings.Repeat("a", 201)+`"}]}`)))
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidReq)
	require.Equal(t, http.StatusBadRequest, invalidRec.Code)

	oversizedDetailReq := httptest.NewRequest(
		http.MethodPost,
		"/api/emissions?org_id="+orgID,
		bytes.NewReader([]byte(`{"emissions":[{"source_type":"agent","source_id":"agent-1","kind":"log","summary":"detail too long","detail":"`+strings.Repeat("d", 5001)+`"}]}`)),
	)
	oversizedDetailRec := httptest.NewRecorder()
	router.ServeHTTP(oversizedDetailRec, oversizedDetailReq)
	require.Equal(t, http.StatusBadRequest, oversizedDetailRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodGet, "/api/emissions/recent", nil)
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, missingWorkspaceRec.Code)
}

func TestEmissionWebsocketBroadcast(t *testing.T) {
	buffer := NewEmissionBuffer(10)
	broadcaster := &fakeEmissionBroadcaster{}
	handler := &EmissionsHandler{
		Buffer: buffer,
		Hub:    broadcaster,
	}
	router := newEmissionsTestRouter(handler)

	orgID := "550e8400-e29b-41d4-a716-446655440000"
	projectID := "project-live"
	issueID := "issue-live"
	now := time.Now().UTC().Format(time.RFC3339)
	body := `{
		"emissions":[
			{
				"id":"em-ws-1",
				"source_type":"agent",
				"source_id":"agent-1",
				"kind":"status",
				"summary":"Running tests",
				"timestamp":"` + now + `",
				"scope":{"project_id":"` + projectID + `","issue_id":"` + issueID + `"}
			}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/emissions?org_id="+orgID, bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Len(t, broadcaster.orgBroadcasts, 1)
	require.Equal(t, orgID, broadcaster.orgBroadcasts[0])
	require.Contains(t, broadcaster.topicBroadcasts, orgID+":project:"+projectID)
	require.Contains(t, broadcaster.topicBroadcasts, orgID+":issue:"+issueID)

	require.NotEmpty(t, broadcaster.payloads)
	var event emissionReceivedEvent
	require.NoError(t, json.Unmarshal(broadcaster.payloads[0], &event))
	require.Equal(t, ws.MessageEmissionReceived, event.Type)
	require.Equal(t, "em-ws-1", event.Emission.ID)
	require.Equal(t, "agent-1", event.Emission.SourceID)
}

func TestBroadcastEmissionEventMarshalErrorLogsWarning(t *testing.T) {
	broadcaster := &fakeEmissionBroadcaster{}
	orgID := "550e8400-e29b-41d4-a716-446655440000"
	emission := Emission{
		ID:         "em-marshal-1",
		OrgID:      orgID,
		SourceType: "agent",
		SourceID:   "agent-1",
		Kind:       "status",
		Summary:    "marshal failure path",
		Timestamp:  time.Now().UTC(),
	}

	originalMarshal := emissionEventJSONMarshal
	emissionEventJSONMarshal = func(v any) ([]byte, error) {
		return nil, errors.New("marshal boom")
	}
	t.Cleanup(func() {
		emissionEventJSONMarshal = originalMarshal
	})

	var logs bytes.Buffer
	originalLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(originalLogOutput)
	})

	broadcastEmissionEvent(broadcaster, orgID, emission)

	require.Empty(t, broadcaster.orgBroadcasts)
	require.Contains(t, logs.String(), "failed to marshal emission broadcast payload")
	require.Contains(t, logs.String(), "marshal boom")
}

func TestEmissionHandlerIngestBatchSizeLimit(t *testing.T) {
	buffer := NewEmissionBuffer(200)
	handler := &EmissionsHandler{Buffer: buffer}
	router := newEmissionsTestRouter(handler)
	orgID := "550e8400-e29b-41d4-a716-446655440000"

	buildPayload := func(count int) []byte {
		emissions := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			emissions = append(emissions, map[string]any{
				"id":          "em-limit-" + strconv.Itoa(i),
				"source_type": "agent",
				"source_id":   "agent-limit",
				"kind":        "status",
				"summary":     "limit test",
			})
		}
		body, err := json.Marshal(map[string]any{"emissions": emissions})
		require.NoError(t, err)
		return body
	}

	tooLargeReq := httptest.NewRequest(http.MethodPost, "/api/emissions?org_id="+orgID, bytes.NewReader(buildPayload(101)))
	tooLargeRec := httptest.NewRecorder()
	router.ServeHTTP(tooLargeRec, tooLargeReq)
	require.Equal(t, http.StatusBadRequest, tooLargeRec.Code)
	require.Contains(t, tooLargeRec.Body.String(), "too many emissions")

	maxReq := httptest.NewRequest(http.MethodPost, "/api/emissions?org_id="+orgID, bytes.NewReader(buildPayload(100)))
	maxRec := httptest.NewRecorder()
	router.ServeHTTP(maxRec, maxReq)
	require.Equal(t, http.StatusAccepted, maxRec.Code)
}

func TestEmissionHandlerRecentLimitClamp(t *testing.T) {
	buffer := NewEmissionBuffer(500)
	handler := &EmissionsHandler{Buffer: buffer}
	router := newEmissionsTestRouter(handler)
	orgID := "550e8400-e29b-41d4-a716-446655440000"

	for i := 0; i < 250; i++ {
		buffer.Push(Emission{
			ID:         "em-clamp-" + strconv.Itoa(i),
			OrgID:      orgID,
			SourceType: "agent",
			SourceID:   "agent-clamp",
			Kind:       "status",
			Summary:    "limit clamp test",
			Timestamp:  time.Now().Add(time.Duration(i) * time.Millisecond),
		})
	}

	req := httptest.NewRequest(http.MethodGet, "/api/emissions/recent?org_id="+orgID+"&limit=9999", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload emissionListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Len(t, payload.Items, 200)
	require.Equal(t, 200, payload.Total)
}

func TestEmissionBufferOrgIsolation(t *testing.T) {
	buffer := NewEmissionBuffer(10)
	buffer.Push(Emission{
		ID:         "org-a-1",
		OrgID:      "org-a",
		SourceType: "agent",
		SourceID:   "agent-main",
		Kind:       "status",
		Summary:    "org A update",
		Timestamp:  time.Now().Add(-2 * time.Second),
	})
	buffer.Push(Emission{
		ID:         "org-b-1",
		OrgID:      "org-b",
		SourceType: "agent",
		SourceID:   "agent-main",
		Kind:       "status",
		Summary:    "org B update",
		Timestamp:  time.Now().Add(-1 * time.Second),
	})

	orgARecent := buffer.Recent(10, EmissionFilter{OrgID: "org-a"})
	require.Len(t, orgARecent, 1)
	require.Equal(t, "org-a-1", orgARecent[0].ID)

	orgBRecent := buffer.Recent(10, EmissionFilter{OrgID: "org-b"})
	require.Len(t, orgBRecent, 1)
	require.Equal(t, "org-b-1", orgBRecent[0].ID)

	orgALatest := buffer.LatestBySource("org-a", "agent-main")
	require.NotNil(t, orgALatest)
	require.Equal(t, "org-a-1", orgALatest.ID)

	orgBLatest := buffer.LatestBySource("org-b", "agent-main")
	require.NotNil(t, orgBLatest)
	require.Equal(t, "org-b-1", orgBLatest.ID)
}
