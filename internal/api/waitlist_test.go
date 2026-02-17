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

func TestWaitlistHandlerRateLimitsByClientIP(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	current := time.Date(2026, 2, 16, 21, 0, 0, 0, time.UTC)
	handler := &WaitlistHandler{
		DB: db,
		now: func() time.Time {
			return current
		},
		sendNotification: func(string, string) {},
		limiter:          newWaitlistRateLimiter(2, 5*time.Minute),
	}

	mock.ExpectExec(`INSERT INTO waitlist \(email\) VALUES \(\$1\) ON CONFLICT \(email\) DO NOTHING`).
		WithArgs("first@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO waitlist \(email\) VALUES \(\$1\) ON CONFLICT \(email\) DO NOTHING`).
		WithArgs("second@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))

	firstReq := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":"first@example.com"}`))
	firstReq.Header.Set("X-Forwarded-For", "198.51.100.25")
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	handler.Handle(firstRec, firstReq)
	require.Equal(t, http.StatusOK, firstRec.Code)

	secondReq := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":"second@example.com"}`))
	secondReq.Header.Set("X-Forwarded-For", "198.51.100.25")
	secondReq.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	handler.Handle(secondRec, secondReq)
	require.Equal(t, http.StatusOK, secondRec.Code)

	thirdReq := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":"third@example.com"}`))
	thirdReq.Header.Set("X-Forwarded-For", "198.51.100.25")
	thirdReq.Header.Set("Content-Type", "application/json")
	thirdRec := httptest.NewRecorder()
	handler.Handle(thirdRec, thirdReq)
	require.Equal(t, http.StatusTooManyRequests, thirdRec.Code)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWaitlistHandlerAllowsAfterWindowReset(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	current := time.Date(2026, 2, 16, 21, 0, 0, 0, time.UTC)
	handler := &WaitlistHandler{
		DB: db,
		now: func() time.Time {
			return current
		},
		sendNotification: func(string, string) {},
		limiter:          newWaitlistRateLimiter(1, 1*time.Minute),
	}

	mock.ExpectExec(`INSERT INTO waitlist \(email\) VALUES \(\$1\) ON CONFLICT \(email\) DO NOTHING`).
		WithArgs("first@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO waitlist \(email\) VALUES \(\$1\) ON CONFLICT \(email\) DO NOTHING`).
		WithArgs("third@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))

	firstReq := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":"first@example.com"}`))
	firstReq.Header.Set("X-Forwarded-For", "203.0.113.7")
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	handler.Handle(firstRec, firstReq)
	require.Equal(t, http.StatusOK, firstRec.Code)

	secondReq := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":"second@example.com"}`))
	secondReq.Header.Set("X-Forwarded-For", "203.0.113.7")
	secondReq.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	handler.Handle(secondRec, secondReq)
	require.Equal(t, http.StatusTooManyRequests, secondRec.Code)

	current = current.Add(2 * time.Minute)
	thirdReq := httptest.NewRequest(http.MethodPost, "/api/waitlist", bytes.NewBufferString(`{"email":"third@example.com"}`))
	thirdReq.Header.Set("X-Forwarded-For", "203.0.113.7")
	thirdReq.Header.Set("Content-Type", "application/json")
	thirdRec := httptest.NewRecorder()
	handler.Handle(thirdRec, thirdReq)
	require.Equal(t, http.StatusOK, thirdRec.Code)

	require.NoError(t, mock.ExpectationsWereMet())
}
