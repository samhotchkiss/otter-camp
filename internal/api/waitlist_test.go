package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestWaitlistHandlerPersistsEmail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	handler := &WaitlistHandler{
		DB: db,
		now: func() time.Time {
			return time.Date(2026, 2, 16, 21, 0, 0, 0, time.UTC)
		},
		sendNotification: func(string, string) {},
	}

	mock.ExpectExec(`INSERT INTO waitlist \(email\) VALUES \(\$1\) ON CONFLICT \(email\) DO NOTHING`).
		WithArgs("test@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":" Test@Example.com "}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Handle(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp WaitlistResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Success)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWaitlistHandlerRejectsInvalidEmail(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	handler := &WaitlistHandler{
		DB:               db,
		now:              time.Now,
		sendNotification: func(string, string) {},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":"not-an-email"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Handle(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestWaitlistHandlerHandlesDuplicateEmail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	handler := &WaitlistHandler{
		DB: db,
		now: func() time.Time {
			return time.Date(2026, 2, 16, 21, 0, 0, 0, time.UTC)
		},
		sendNotification: func(string, string) {},
	}

	mock.ExpectExec(`INSERT INTO waitlist \(email\) VALUES \(\$1\) ON CONFLICT \(email\) DO NOTHING`).
		WithArgs("dupe@example.com").
		WillReturnResult(sqlmock.NewResult(1, 0))

	req := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":"dupe@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Handle(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp WaitlistResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.Success)
	require.NoError(t, mock.ExpectationsWereMet())
}
