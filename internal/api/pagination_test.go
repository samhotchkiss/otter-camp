package api

import (
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPaginationEncodeDecodeRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	id := uuid.New()

	encoded := (PaginationEncoder{}).Encode(createdAt, id)
	gotCreatedAt, gotID, err := (PaginationDecoder{}).Decode(encoded)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}

	if !gotCreatedAt.Equal(createdAt) {
		t.Fatalf("createdAt = %s, want %s", gotCreatedAt, createdAt)
	}
	if gotID != id {
		t.Fatalf("id = %s, want %s", gotID, id)
	}
}

func TestPaginationDecodeCorruptBase64ReturnsError(t *testing.T) {
	_, _, err := (PaginationDecoder{}).Decode("not-base64!!")
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestPaginationLimitClamping(t *testing.T) {
	testCases := []struct {
		name      string
		rawLimit  string
		wantLimit int
	}{
		{name: "zero defaults", rawLimit: "0", wantLimit: 50},
		{name: "too high clamps", rawLimit: "300", wantLimit: 200},
		{name: "in range", rawLimit: "25", wantLimit: 25},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			values := url.Values{}
			values.Set("limit", tc.rawLimit)

			params := ParsePaginationParams(values)
			if params.Limit != tc.wantLimit {
				t.Fatalf("limit = %d, want %d", params.Limit, tc.wantLimit)
			}
		})
	}
}
