package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type PreferenceRepository struct {
	pool *pgxpool.Pool
}

func NewPreferenceRepository(pool *pgxpool.Pool) *PreferenceRepository {
	return &PreferenceRepository{pool: pool}
}

func (r *PreferenceRepository) GetPreferences(ctx context.Context, userID uuid.UUID) (PushPreferences, error) {
	settings, err := r.getSettings(ctx, userID)
	if err != nil {
		return PushPreferences{}, err
	}

	value, exists := settings["push_preferences"]
	if !exists || value == nil {
		return cloneDefaultPreferences(), nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return PushPreferences{}, fmt.Errorf("marshal push_preferences: %w", err)
	}
	var decoded PushPreferences
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return PushPreferences{}, fmt.Errorf("decode push_preferences: %w", err)
	}

	return normalizePreferences(decoded), nil
}

func (r *PreferenceRepository) SavePreferences(ctx context.Context, userID uuid.UUID, prefs PushPreferences) error {
	normalized := normalizePreferences(prefs)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal push_preferences: %w", err)
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE human_user
		SET settings = jsonb_set(COALESCE(settings, '{}'::jsonb), '{push_preferences}', $2::jsonb, true)
		WHERE id = $1
	`, userID, encoded)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (r *PreferenceRepository) GetTokens(ctx context.Context, userID uuid.UUID) ([]PushToken, error) {
	settings, err := r.getSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	value, exists := settings["push_tokens"]
	if !exists || value == nil {
		return []PushToken{}, nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal push_tokens: %w", err)
	}
	var decoded []PushToken
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, fmt.Errorf("decode push_tokens: %w", err)
	}
	for i := range decoded {
		decoded[i].Token = strings.TrimSpace(decoded[i].Token)
		decoded[i].Platform = strings.ToLower(strings.TrimSpace(decoded[i].Platform))
		decoded[i].DeviceID = strings.TrimSpace(decoded[i].DeviceID)
		decoded[i].RegisteredAt = decoded[i].RegisteredAt.UTC()
	}
	return decoded, nil
}

func (r *PreferenceRepository) RegisterToken(ctx context.Context, userID uuid.UUID, token PushToken) error {
	normalized, err := normalizeToken(token)
	if err != nil {
		return err
	}
	if normalized.RegisteredAt.IsZero() {
		normalized.RegisteredAt = time.Now().UTC()
	}

	encoded, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal push token: %w", err)
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE human_user
		SET settings = jsonb_set(
			COALESCE(settings, '{}'::jsonb),
			'{push_tokens}',
			(
				CASE
					WHEN EXISTS (
						SELECT 1
						FROM jsonb_array_elements(COALESCE(settings->'push_tokens', '[]'::jsonb)) AS elem
						WHERE elem->>'device_id' = $2
					)
					THEN (
						SELECT COALESCE(jsonb_agg(
							CASE
								WHEN elem->>'device_id' = $2 THEN $3::jsonb
								ELSE elem
							END
						), '[]'::jsonb)
						FROM jsonb_array_elements(COALESCE(settings->'push_tokens', '[]'::jsonb)) AS elem
					)
					ELSE COALESCE(settings->'push_tokens', '[]'::jsonb) || jsonb_build_array($3::jsonb)
				END
			),
			true
		)
		WHERE id = $1
	`, userID, normalized.DeviceID, encoded)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (r *PreferenceRepository) RevokeToken(ctx context.Context, userID uuid.UUID, deviceID string) error {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return fmt.Errorf("device_id is required")
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE human_user
		SET settings = jsonb_set(
			COALESCE(settings, '{}'::jsonb),
			'{push_tokens}',
			COALESCE((
				SELECT jsonb_agg(elem)
				FROM jsonb_array_elements(COALESCE(settings->'push_tokens', '[]'::jsonb)) AS elem
				WHERE elem->>'device_id' <> $2
			), '[]'::jsonb),
			true
		)
		WHERE id = $1
	`, userID, deviceID)
	if err != nil {
		return mapDBError(err)
	}
	if tag.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}

func (r *PreferenceRepository) getSettings(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	var raw json.RawMessage
	err := r.pool.QueryRow(ctx, `
		SELECT settings
		FROM human_user
		WHERE id = $1
	`, userID).Scan(&raw)
	if err == pgx.ErrNoRows {
		return nil, repo.ErrNotFound
	}
	if err != nil {
		return nil, mapDBError(err)
	}

	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	decoded := make(map[string]any)
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode user settings: %w", err)
	}
	return decoded, nil
}

func normalizeToken(token PushToken) (PushToken, error) {
	normalized := PushToken{
		Token:        strings.TrimSpace(token.Token),
		Platform:     strings.ToLower(strings.TrimSpace(token.Platform)),
		DeviceID:     strings.TrimSpace(token.DeviceID),
		RegisteredAt: token.RegisteredAt.UTC(),
	}
	if normalized.Token == "" {
		return PushToken{}, fmt.Errorf("token is required")
	}
	if normalized.DeviceID == "" {
		return PushToken{}, fmt.Errorf("device_id is required")
	}
	if normalized.Platform != "apns" && normalized.Platform != "fcm" {
		return PushToken{}, fmt.Errorf("platform must be apns or fcm")
	}
	return normalized, nil
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	if err == pgx.ErrNoRows {
		return repo.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23503", "23505", "23514":
		return repo.ErrConflict
	default:
		return err
	}
}
