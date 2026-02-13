package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJobScheduleNormalizeIntervalRequiresPositiveInterval(t *testing.T) {
	_, err := NormalizeScheduleSpec("interval", nil, int64Ptr(0), nil, "UTC")
	require.Error(t, err)
}

func TestJobScheduleNormalizeCronRejectsInvalidExpression(t *testing.T) {
	_, err := NormalizeScheduleSpec("cron", strPtr("not cron"), nil, nil, "UTC")
	require.Error(t, err)
}

func TestJobScheduleComputeIntervalFirstRun(t *testing.T) {
	spec, err := NormalizeScheduleSpec("interval", nil, int64Ptr(30000), nil, "UTC")
	require.NoError(t, err)

	now := time.Date(2026, 2, 12, 18, 0, 0, 0, time.UTC)
	nextRunAt, err := ComputeNextRun(spec, now, nil)
	require.NoError(t, err)
	require.NotNil(t, nextRunAt)
	require.Equal(t, now.Add(30*time.Second), nextRunAt.UTC())
}

func TestJobScheduleComputeIntervalFromLastRun(t *testing.T) {
	spec, err := NormalizeScheduleSpec("interval", nil, int64Ptr(60000), nil, "UTC")
	require.NoError(t, err)

	now := time.Date(2026, 2, 12, 18, 0, 0, 0, time.UTC)
	lastRun := now.Add(-2 * time.Minute)
	nextRunAt, err := ComputeNextRun(spec, now, &lastRun)
	require.NoError(t, err)
	require.NotNil(t, nextRunAt)
	require.Equal(t, lastRun.Add(1*time.Minute), nextRunAt.UTC())
}

func TestJobScheduleComputeCronHonorsTimezone(t *testing.T) {
	spec, err := NormalizeScheduleSpec("cron", strPtr("0 9 * * *"), nil, nil, "America/New_York")
	require.NoError(t, err)

	now := time.Date(2026, 2, 12, 14, 30, 0, 0, time.UTC)
	nextRunAt, err := ComputeNextRun(spec, now, nil)
	require.NoError(t, err)
	require.NotNil(t, nextRunAt)

	expected := time.Date(2026, 2, 13, 14, 0, 0, 0, time.UTC)
	require.Equal(t, expected, nextRunAt.UTC())
}

func TestJobScheduleComputeOnceOnlyBeforeRun(t *testing.T) {
	runAt := time.Date(2026, 2, 12, 20, 0, 0, 0, time.UTC)
	spec, err := NormalizeScheduleSpec("once", nil, nil, &runAt, "UTC")
	require.NoError(t, err)

	nextRunAt, err := ComputeNextRun(spec, runAt.Add(-1*time.Hour), nil)
	require.NoError(t, err)
	require.NotNil(t, nextRunAt)
	require.Equal(t, runAt, nextRunAt.UTC())

	nextAfterCompletion, err := ComputeNextRun(spec, runAt.Add(1*time.Hour), &runAt)
	require.NoError(t, err)
	require.Nil(t, nextAfterCompletion)
}

func TestJobScheduleNormalizeRejectsUnknownKind(t *testing.T) {
	_, err := NormalizeScheduleSpec("yearly", nil, nil, nil, "UTC")
	require.Error(t, err)
}

func strPtr(v string) *string {
	return &v
}

func int64Ptr(v int64) *int64 {
	return &v
}
