package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestOpenClawHandlerRejectsWhenSecretMissing(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "")
	handler := NewOpenClawHandler(NewHub(), nil)

	req := httptest.NewRequest(http.MethodGet, "/ws/openclaw", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestOpenClawHandlerRejectsInvalidToken(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "expected-secret")
	handler := NewOpenClawHandler(NewHub(), nil)

	req := httptest.NewRequest(http.MethodGet, "/ws/openclaw?token=wrong-secret", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestOpenClawHandlerAcceptsValidToken(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "valid-secret")
	hub := NewHub()
	go hub.Run()

	handler := NewOpenClawHandler(hub, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=valid-secret"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, message, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(message), "\"type\":\"connected\"")
}

func TestOpenClawHandlerRejectsSecondConnectionWhileActive(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "valid-secret")
	hub := NewHub()
	go hub.Run()

	handler := NewOpenClawHandler(hub, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=valid-secret"
	firstConn, firstResp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	require.NotNil(t, firstResp)
	defer firstConn.Close()

	_ = firstConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, message, err := firstConn.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(message), "\"type\":\"connected\"")

	secondConn, secondResp, secondErr := websocket.DefaultDialer.Dial(wsURL, nil)
	if secondConn != nil {
		_ = secondConn.Close()
	}
	require.Error(t, secondErr)
	require.NotNil(t, secondResp)
	require.Equal(t, http.StatusConflict, secondResp.StatusCode)

	// The active bridge connection should remain open after rejecting duplicates.
	_ = firstConn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	_, _, readErr := firstConn.ReadMessage()
	require.Error(t, readErr)
	var netErr net.Error
	require.True(t, errors.As(readErr, &netErr) && netErr.Timeout(), "expected timeout while connection remains open; got: %v", readErr)
}

func TestOpenClawHandlerRequestRoundTrip(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "valid-secret")
	hub := NewHub()
	go hub.Run()

	handler := NewOpenClawHandler(hub, nil)
	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=valid-secret"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage() // connected event
	require.NoError(t, err)

	responseCh := make(chan json.RawMessage, 1)
	requestErrCh := make(chan error, 1)
	go func() {
		payload, requestErr := handler.Request(
			context.Background(),
			"memory.extract.request",
			"org-1",
			map[string]any{"args": []string{"gateway", "call", "agent"}},
		)
		if requestErr != nil {
			requestErrCh <- requestErr
			return
		}
		responseCh <- payload
	}()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, requestMessage, err := conn.ReadMessage()
	require.NoError(t, err)
	var request struct {
		Type string `json:"type"`
		Data struct {
			RequestID string   `json:"request_id"`
			Args      []string `json:"args"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(requestMessage, &request))
	require.Equal(t, "memory.extract.request", request.Type)
	require.NotEmpty(t, request.Data.RequestID)
	require.Equal(t, []string{"gateway", "call", "agent"}, request.Data.Args)

	reply := map[string]any{
		"type":   "memory.extract.response",
		"org_id": "org-1",
		"data": map[string]any{
			"request_id": request.Data.RequestID,
			"ok":         true,
			"output":     `{"runId":"trace-1","status":"ok"}`,
		},
	}
	replyRaw, marshalErr := json.Marshal(reply)
	require.NoError(t, marshalErr)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, replyRaw))

	select {
	case requestErr := <-requestErrCh:
		require.NoError(t, requestErr)
	case payload := <-responseCh:
		var response struct {
			RequestID string `json:"request_id"`
			OK        bool   `json:"ok"`
			Output    string `json:"output"`
		}
		require.NoError(t, json.Unmarshal(payload, &response))
		require.Equal(t, request.Data.RequestID, response.RequestID)
		require.True(t, response.OK)
		require.Equal(t, `{"runId":"trace-1","status":"ok"}`, response.Output)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request response")
	}
}

func TestOpenClawHandlerRequestReturnsNotConnectedError(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "valid-secret")
	handler := NewOpenClawHandler(NewHub(), nil)

	_, err := handler.Request(
		context.Background(),
		"memory.extract.request",
		"org-1",
		map[string]any{"args": []string{"gateway", "call", "agent"}},
	)
	require.ErrorIs(t, err, ErrOpenClawNotConnected)
}

func TestOpenClawHandlerRequestWaitsForConnection(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "valid-secret")
	hub := NewHub()
	go hub.Run()

	handler := NewOpenClawHandler(hub, nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "?token=valid-secret"

	resultCh := make(chan json.RawMessage, 1)
	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		payload, requestErr := handler.Request(
			ctx,
			"memory.extract.request",
			"org-1",
			map[string]any{"args": []string{"gateway", "call", "agent"}},
		)
		if requestErr != nil {
			errCh <- requestErr
			return
		}
		resultCh <- payload
	}()

	time.Sleep(300 * time.Millisecond)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage() // connected event
	require.NoError(t, err)

	_, requestMessage, err := conn.ReadMessage()
	require.NoError(t, err)
	var request struct {
		Data struct {
			RequestID string `json:"request_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(requestMessage, &request))
	require.NotEmpty(t, request.Data.RequestID)

	reply := map[string]any{
		"type":   "memory.extract.response",
		"org_id": "org-1",
		"data": map[string]any{
			"request_id": request.Data.RequestID,
			"ok":         true,
			"output":     "{}",
		},
	}
	replyRaw, marshalErr := json.Marshal(reply)
	require.NoError(t, marshalErr)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, replyRaw))

	select {
	case requestErr := <-errCh:
		require.NoError(t, requestErr)
	case payload := <-resultCh:
		require.NotEmpty(t, payload)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for request response")
	}
}
