package ws

import (
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
	handler := NewOpenClawHandler(NewHub())

	req := httptest.NewRequest(http.MethodGet, "/ws/openclaw", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestOpenClawHandlerRejectsInvalidToken(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "expected-secret")
	handler := NewOpenClawHandler(NewHub())

	req := httptest.NewRequest(http.MethodGet, "/ws/openclaw?token=wrong-secret", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestOpenClawHandlerAcceptsValidToken(t *testing.T) {
	t.Setenv("OPENCLAW_WS_SECRET", "valid-secret")
	hub := NewHub()
	go hub.Run()

	handler := NewOpenClawHandler(hub)
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
