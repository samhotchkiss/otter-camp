package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeAuth struct {
	identity Identity
	err      error
}

func (f fakeAuth) Authenticate(_ context.Context, _ *http.Request) (Identity, error) {
	if f.err != nil {
		return Identity{}, f.err
	}
	return f.identity, nil
}

func TestHandlerInitialize(t *testing.T) {
	h := NewHTTPHandler(NewServer(), fakeAuth{identity: Identity{OrgID: "org-1", UserID: "user-1"}})

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer oc_sess_test")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		Result  struct {
			ProtocolVersion string `json:"protocolVersion"`
			Capabilities    struct {
				Tools struct {
					ListChanged bool `json:"listChanged"`
				} `json:"tools"`
			} `json:"capabilities"`
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "2.0", resp.JSONRPC)
	require.Equal(t, "2025-06-18", resp.Result.ProtocolVersion)
	require.Equal(t, "otter-camp", resp.Result.ServerInfo.Name)
	require.True(t, resp.Result.Capabilities.Tools.ListChanged)
}

func TestHandlerRejectsUnauthorized(t *testing.T) {
	h := NewHTTPHandler(NewServer(), fakeAuth{err: ErrInvalidAuth})

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp struct {
		Error struct {
			Code int    `json:"code"`
			Msg  string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, -32001, resp.Error.Code)
	require.NotEmpty(t, resp.Error.Msg)
}

func TestHandlerInvalidJSONRPC(t *testing.T) {
	h := NewHTTPHandler(NewServer(), fakeAuth{identity: Identity{OrgID: "org-1", UserID: "user-1"}})

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString("{"))
	req.Header.Set("Authorization", "Bearer oc_sess_test")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var resp struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, -32700, resp.Error.Code)
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	h := NewHTTPHandler(NewServer(), fakeAuth{identity: Identity{OrgID: "org-1", UserID: "user-1"}})

	req := httptest.NewRequest(http.MethodPut, "/mcp", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandlerAuthErrorMapsToUnauthorized(t *testing.T) {
	h := NewHTTPHandler(NewServer(), fakeAuth{err: errors.New("db down")})

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
