package push

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type PreferenceStore interface {
	GetPreferences(ctx context.Context, userID uuid.UUID) (PushPreferences, error)
	SavePreferences(ctx context.Context, userID uuid.UUID, prefs PushPreferences) error
}

type PreferenceService struct {
	repo PreferenceStore
	now  func() time.Time
}

type PreferenceServiceOptions struct {
	Repository PreferenceStore
	Now        func() time.Time
}

func NewPreferenceService(opts PreferenceServiceOptions) (*PreferenceService, error) {
	if opts.Repository == nil {
		return nil, fmt.Errorf("repository is required")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PreferenceService{repo: opts.Repository, now: now}, nil
}

func (s *PreferenceService) GetPreferences(ctx context.Context, userID uuid.UUID) (PushPreferences, error) {
	prefs, err := s.repo.GetPreferences(ctx, userID)
	if err != nil {
		return PushPreferences{}, err
	}
	return normalizePreferences(prefs), nil
}

func (s *PreferenceService) UpdatePreferences(ctx context.Context, userID uuid.UUID, update PushPreferenceUpdate) (PushPreferences, error) {
	if err := validatePreferenceUpdate(update); err != nil {
		return PushPreferences{}, err
	}

	current, err := s.repo.GetPreferences(ctx, userID)
	if err != nil {
		return PushPreferences{}, err
	}
	current = normalizePreferences(current)

	for key, enabled := range update.TierEnabled {
		normalizedTier := normalizeTier(key)
		if normalizedTier == "" {
			continue
		}
		current.TierEnabled[normalizedTier] = enabled
	}

	if update.ProjectOverrides != nil {
		nextOverrides := make([]ProjectPushOverride, 0, len(update.ProjectOverrides))
		for _, override := range update.ProjectOverrides {
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
			nextOverrides = append(nextOverrides, copyOverride)
		}
		current.ProjectOverrides = nextOverrides
	}

	if update.QuietHoursEnabled != nil {
		current.QuietHoursEnabled = *update.QuietHoursEnabled
	}
	if update.QuietHoursStart != nil {
		current.QuietHoursStart = normalizeOptionalClock(update.QuietHoursStart)
	}
	if update.QuietHoursEnd != nil {
		current.QuietHoursEnd = normalizeOptionalClock(update.QuietHoursEnd)
	}
	if update.QuietHoursTimezone != nil {
		current.QuietHoursTimezone = normalizeOptionalString(update.QuietHoursTimezone)
	}
	if update.EventTypeOverrides != nil {
		if current.EventTypeOverrides == nil {
			current.EventTypeOverrides = map[string]bool{}
		}
		for key, enabled := range update.EventTypeOverrides {
			normalizedKey := strings.TrimSpace(key)
			if normalizedKey == "" {
				continue
			}
			current.EventTypeOverrides[normalizedKey] = enabled
		}
	}

	if err := s.repo.SavePreferences(ctx, userID, current); err != nil {
		return PushPreferences{}, err
	}
	return current, nil
}

func (s *PreferenceService) ShouldDeliver(ctx context.Context, userID uuid.UUID, projectID *uuid.UUID, urgencyTier, eventType string) (bool, error) {
	prefs, err := s.GetPreferences(ctx, userID)
	if err != nil {
		return false, err
	}

	tier := normalizeTier(urgencyTier)
	if tier == "" {
		tier = TierNormal
	}
	tierEnabled := prefs.TierEnabled[tier]

	if projectID != nil && *projectID != uuid.Nil {
		if override, ok := findProjectOverride(prefs.ProjectOverrides, *projectID); ok {
			if !override.Enabled {
				return false, nil
			}
			if projectTierEnabled, exists := override.Tiers[tier]; exists {
				tierEnabled = projectTierEnabled
			}
		}
	}

	if !tierEnabled {
		return false, nil
	}

	normalizedEventType := strings.TrimSpace(eventType)
	if normalizedEventType != "" {
		if override, exists := prefs.EventTypeOverrides[normalizedEventType]; exists && !override {
			return false, nil
		}
	}

	if prefs.QuietHoursEnabled && tier != TierUrgent {
		withinQuietHours, err := isWithinQuietHours(s.now(), prefs)
		if err != nil {
			return false, err
		}
		if withinQuietHours {
			return false, nil
		}
	}

	return true, nil
}

func validatePreferenceUpdate(update PushPreferenceUpdate) error {
	for key := range update.TierEnabled {
		if normalizeTier(key) == "" {
			return fmt.Errorf("invalid tier key: %s", key)
		}
	}
	for _, override := range update.ProjectOverrides {
		for key := range override.Tiers {
			if normalizeTier(key) == "" {
				return fmt.Errorf("invalid project tier key: %s", key)
			}
		}
	}
	if update.QuietHoursStart != nil {
		if _, err := parseClock(*update.QuietHoursStart); err != nil {
			return err
		}
	}
	if update.QuietHoursEnd != nil {
		if _, err := parseClock(*update.QuietHoursEnd); err != nil {
			return err
		}
	}
	if update.QuietHoursTimezone != nil {
		trimmed := strings.TrimSpace(*update.QuietHoursTimezone)
		if trimmed != "" {
			if _, err := time.LoadLocation(trimmed); err != nil {
				return fmt.Errorf("invalid quiet hours timezone")
			}
		}
	}
	return nil
}

func findProjectOverride(overrides []ProjectPushOverride, projectID uuid.UUID) (ProjectPushOverride, bool) {
	for _, override := range overrides {
		if override.ProjectID == projectID {
			return override, true
		}
	}
	return ProjectPushOverride{}, false
}

func isWithinQuietHours(nowUTC time.Time, prefs PushPreferences) (bool, error) {
	if !prefs.QuietHoursEnabled {
		return false, nil
	}
	if prefs.QuietHoursStart == nil || prefs.QuietHoursEnd == nil || prefs.QuietHoursTimezone == nil {
		return false, nil
	}
	startMinute, err := parseClock(*prefs.QuietHoursStart)
	if err != nil {
		return false, err
	}
	endMinute, err := parseClock(*prefs.QuietHoursEnd)
	if err != nil {
		return false, err
	}
	loc, err := time.LoadLocation(strings.TrimSpace(*prefs.QuietHoursTimezone))
	if err != nil {
		return false, fmt.Errorf("invalid quiet hours timezone")
	}

	localNow := nowUTC.In(loc)
	currentMinute := localNow.Hour()*60 + localNow.Minute()

	if startMinute == endMinute {
		return false, nil
	}
	if startMinute < endMinute {
		return currentMinute >= startMinute && currentMinute < endMinute, nil
	}
	return currentMinute >= startMinute || currentMinute < endMinute, nil
}

func parseClock(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	parsed, err := time.Parse("15:04", trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid quiet hours format")
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}
