package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultPaginationLimit = 50
	MaxPaginationLimit     = 200
)

type PaginationParams struct {
	Cursor string
	Limit  int
	Order  string
}

type PaginationCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type PaginationEncoder struct{}
type PaginationDecoder struct{}

type encodedPaginationCursor struct {
	T  string `json:"t"`
	ID string `json:"id"`
}

func ParsePaginationParams(values url.Values) PaginationParams {
	limit := DefaultPaginationLimit
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = clampLimit(parsed)
		}
	}

	order := strings.ToLower(strings.TrimSpace(values.Get("order")))
	if order != "asc" && order != "desc" {
		order = "desc"
	}

	return PaginationParams{
		Cursor: strings.TrimSpace(values.Get("cursor")),
		Limit:  limit,
		Order:  order,
	}
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultPaginationLimit
	}
	if limit > MaxPaginationLimit {
		return MaxPaginationLimit
	}
	return limit
}

func (PaginationEncoder) Encode(createdAt time.Time, id uuid.UUID) string {
	cursor := encodedPaginationCursor{
		T:  createdAt.UTC().Format(time.RFC3339Nano),
		ID: id.String(),
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func (PaginationDecoder) Decode(cursor string) (time.Time, uuid.UUID, error) {
	rawCursor := strings.TrimSpace(cursor)
	if rawCursor == "" {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor is empty")
	}

	payload, err := base64.RawURLEncoding.DecodeString(rawCursor)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor base64url: %w", err)
	}

	var decoded encodedPaginationCursor
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor json: %w", err)
	}

	if strings.TrimSpace(decoded.T) == "" {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor field t is required")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, decoded.T)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("parse cursor t: %w", err)
	}

	if strings.TrimSpace(decoded.ID) == "" {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor field id is required")
	}
	id, err := uuid.Parse(decoded.ID)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("parse cursor id: %w", err)
	}

	return createdAt.UTC(), id, nil
}
