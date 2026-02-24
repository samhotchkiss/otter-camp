package push

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	TierUrgent = "urgent"
	TierHigh   = "high"
	TierNormal = "normal"
	TierLow    = "low"
)

var validTiers = map[string]struct{}{
	TierUrgent: {},
	TierHigh:   {},
	TierNormal: {},
	TierLow:    {},
}

type ProjectPushOverride struct {
	ProjectID uuid.UUID       `json:"project_id"`
	Enabled   bool            `json:"enabled"`
	Tiers     map[string]bool `json:"tiers"`
}

type PushPreferences struct {
	TierEnabled        map[string]bool       `json:"tier_enabled"`
	ProjectOverrides   []ProjectPushOverride `json:"project_overrides"`
	QuietHoursEnabled  bool                  `json:"quiet_hours_enabled"`
	QuietHoursStart    *string               `json:"quiet_hours_start"`
	QuietHoursEnd      *string               `json:"quiet_hours_end"`
	QuietHoursTimezone *string               `json:"quiet_hours_timezone"`
	EventTypeOverrides map[string]bool       `json:"event_type_overrides"`
}

type PushPreferenceUpdate struct {
	TierEnabled        map[string]bool       `json:"tier_enabled"`
	ProjectOverrides   []ProjectPushOverride `json:"project_overrides"`
	QuietHoursEnabled  *bool                 `json:"quiet_hours_enabled"`
	QuietHoursStart    *string               `json:"quiet_hours_start"`
	QuietHoursEnd      *string               `json:"quiet_hours_end"`
	QuietHoursTimezone *string               `json:"quiet_hours_timezone"`
	EventTypeOverrides map[string]bool       `json:"event_type_overrides"`
}

type PushToken struct {
	Token        string    `json:"token"`
	Platform     string    `json:"platform"`
	DeviceID     string    `json:"device_id"`
	RegisteredAt time.Time `json:"registered_at"`
}

type PushPayload struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Category string `json:"category"`
	DeepLink string `json:"deep_link"`
	ItemID   string `json:"item_id"`
}

type Adapter interface {
	Send(ctx context.Context, token PushToken, payload PushPayload) error
}

var DefaultPushPreferences = PushPreferences{
	TierEnabled: map[string]bool{
		TierUrgent: true,
		TierHigh:   true,
		TierNormal: true,
		TierLow:    false,
	},
	ProjectOverrides:   []ProjectPushOverride{},
	QuietHoursEnabled:  false,
	QuietHoursStart:    nil,
	QuietHoursEnd:      nil,
	QuietHoursTimezone: nil,
	EventTypeOverrides: map[string]bool{},
}

func cloneDefaultPreferences() PushPreferences {
	prefs := DefaultPushPreferences
	prefs.TierEnabled = cloneBoolMap(DefaultPushPreferences.TierEnabled)
	prefs.ProjectOverrides = make([]ProjectPushOverride, 0, len(DefaultPushPreferences.ProjectOverrides))
	for _, item := range DefaultPushPreferences.ProjectOverrides {
		copyItem := item
		copyItem.Tiers = cloneBoolMap(item.Tiers)
		prefs.ProjectOverrides = append(prefs.ProjectOverrides, copyItem)
	}
	prefs.EventTypeOverrides = cloneBoolMap(DefaultPushPreferences.EventTypeOverrides)
	return prefs
}

func normalizePreferences(input PushPreferences) PushPreferences {
	prefs := cloneDefaultPreferences()
	for key, value := range input.TierEnabled {
		normalizedKey := normalizeTier(key)
		if normalizedKey == "" {
			continue
		}
		prefs.TierEnabled[normalizedKey] = value
	}
	if input.ProjectOverrides != nil {
		prefs.ProjectOverrides = make([]ProjectPushOverride, 0, len(input.ProjectOverrides))
		for _, override := range input.ProjectOverrides {
			copyOverride := ProjectPushOverride{
				ProjectID: override.ProjectID,
				Enabled:   override.Enabled,
				Tiers:     map[string]bool{},
			}
			for key, enabled := range override.Tiers {
				normalizedTier := normalizeTier(key)
				if normalizedTier == "" {
					continue
				}
				copyOverride.Tiers[normalizedTier] = enabled
			}
			prefs.ProjectOverrides = append(prefs.ProjectOverrides, copyOverride)
		}
	}
	prefs.QuietHoursEnabled = input.QuietHoursEnabled
	prefs.QuietHoursStart = normalizeOptionalClock(input.QuietHoursStart)
	prefs.QuietHoursEnd = normalizeOptionalClock(input.QuietHoursEnd)
	prefs.QuietHoursTimezone = normalizeOptionalString(input.QuietHoursTimezone)

	prefs.EventTypeOverrides = cloneBoolMap(input.EventTypeOverrides)
	if prefs.EventTypeOverrides == nil {
		prefs.EventTypeOverrides = map[string]bool{}
	}

	return prefs
}

func normalizeTier(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if _, ok := validTiers[normalized]; !ok {
		return ""
	}
	return normalized
}

func normalizeOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	copyValue := trimmed
	return &copyValue
}

func normalizeOptionalClock(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	copyValue := trimmed
	return &copyValue
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	out := make(map[string]bool, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
