package push

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakePreferenceStore struct {
	prefs map[uuid.UUID]PushPreferences
}

func (f *fakePreferenceStore) GetPreferences(_ context.Context, userID uuid.UUID) (PushPreferences, error) {
	if f.prefs == nil {
		f.prefs = map[uuid.UUID]PushPreferences{}
	}
	if value, ok := f.prefs[userID]; ok {
		return value, nil
	}
	return cloneDefaultPreferences(), nil
}

func (f *fakePreferenceStore) SavePreferences(_ context.Context, userID uuid.UUID, prefs PushPreferences) error {
	if f.prefs == nil {
		f.prefs = map[uuid.UUID]PushPreferences{}
	}
	f.prefs[userID] = normalizePreferences(prefs)
	return nil
}

func TestShouldDeliverRespectsTierAndQuietHours(t *testing.T) {
	userID := uuid.New()
	start := "22:00"
	end := "06:00"
	tz := "UTC"

	store := &fakePreferenceStore{prefs: map[uuid.UUID]PushPreferences{
		userID: {
			TierEnabled: map[string]bool{
				TierUrgent: true,
				TierHigh:   true,
				TierNormal: false,
				TierLow:    false,
			},
			ProjectOverrides:   []ProjectPushOverride{},
			QuietHoursEnabled:  true,
			QuietHoursStart:    &start,
			QuietHoursEnd:      &end,
			QuietHoursTimezone: &tz,
			EventTypeOverrides: map[string]bool{},
		},
	}}

	svc, err := NewPreferenceService(PreferenceServiceOptions{
		Repository: store,
		Now: func() time.Time {
			return time.Date(2026, 2, 24, 23, 30, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("NewPreferenceService: %v", err)
	}

	deliver, err := svc.ShouldDeliver(context.Background(), userID, nil, TierNormal, "task.completed")
	if err != nil {
		t.Fatalf("ShouldDeliver normal: %v", err)
	}
	if deliver {
		t.Fatal("expected normal tier delivery to be blocked when tier is disabled")
	}

	deliver, err = svc.ShouldDeliver(context.Background(), userID, nil, TierHigh, "task.blocked")
	if err != nil {
		t.Fatalf("ShouldDeliver high: %v", err)
	}
	if deliver {
		t.Fatal("expected high tier delivery to be blocked during quiet hours")
	}

	deliver, err = svc.ShouldDeliver(context.Background(), userID, nil, TierUrgent, "run.dead_lettered")
	if err != nil {
		t.Fatalf("ShouldDeliver urgent: %v", err)
	}
	if !deliver {
		t.Fatal("expected urgent delivery to bypass quiet hours")
	}
}

func TestShouldDeliverMidnightWrapWindow(t *testing.T) {
	userID := uuid.New()
	start := "22:00"
	end := "06:00"
	tz := "UTC"

	store := &fakePreferenceStore{prefs: map[uuid.UUID]PushPreferences{
		userID: {
			TierEnabled:        map[string]bool{TierUrgent: true, TierHigh: true, TierNormal: true, TierLow: false},
			QuietHoursEnabled:  true,
			QuietHoursStart:    &start,
			QuietHoursEnd:      &end,
			QuietHoursTimezone: &tz,
			ProjectOverrides:   []ProjectPushOverride{},
			EventTypeOverrides: map[string]bool{},
		},
	}}

	svc, err := NewPreferenceService(PreferenceServiceOptions{
		Repository: store,
		Now: func() time.Time {
			return time.Date(2026, 2, 24, 1, 15, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("NewPreferenceService: %v", err)
	}

	deliver, err := svc.ShouldDeliver(context.Background(), userID, nil, TierHigh, "task.blocked")
	if err != nil {
		t.Fatalf("ShouldDeliver high: %v", err)
	}
	if deliver {
		t.Fatal("expected quiet-hours block for midnight-wrap window")
	}
}

func TestUpdatePreferencesValidation(t *testing.T) {
	store := &fakePreferenceStore{}
	svc, err := NewPreferenceService(PreferenceServiceOptions{Repository: store})
	if err != nil {
		t.Fatalf("NewPreferenceService: %v", err)
	}
	userID := uuid.New()

	badTZ := "Invalid/Timezone"
	_, err = svc.UpdatePreferences(context.Background(), userID, PushPreferenceUpdate{QuietHoursTimezone: &badTZ})
	if err == nil {
		t.Fatal("expected invalid timezone error")
	}

	badClock := "25:61"
	_, err = svc.UpdatePreferences(context.Background(), userID, PushPreferenceUpdate{QuietHoursStart: &badClock})
	if err == nil {
		t.Fatal("expected invalid HH:MM error")
	}

	goodTZ := "America/New_York"
	goodStart := "22:00"
	goodEnd := "06:00"
	enabled := true
	updated, err := svc.UpdatePreferences(context.Background(), userID, PushPreferenceUpdate{
		QuietHoursEnabled:  &enabled,
		QuietHoursTimezone: &goodTZ,
		QuietHoursStart:    &goodStart,
		QuietHoursEnd:      &goodEnd,
	})
	if err != nil {
		t.Fatalf("valid update error: %v", err)
	}
	if !updated.QuietHoursEnabled {
		t.Fatal("expected quiet hours enabled after update")
	}
}
