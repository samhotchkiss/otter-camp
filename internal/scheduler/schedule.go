package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	ScheduleKindCron     = "cron"
	ScheduleKindInterval = "interval"
	ScheduleKindOnce     = "once"
)

type ScheduleSpec struct {
	Kind     string
	CronExpr string
	Interval time.Duration
	RunAt    time.Time
	Timezone string

	location     *time.Location
	cronSchedule cron.Schedule
}

func NormalizeScheduleSpec(
	kind string,
	cronExpr *string,
	intervalMS *int64,
	runAt *time.Time,
	timezone string,
) (ScheduleSpec, error) {
	normalizedKind, err := normalizeScheduleKind(kind)
	if err != nil {
		return ScheduleSpec{}, err
	}

	trimmedTimezone := strings.TrimSpace(timezone)
	if trimmedTimezone == "" {
		trimmedTimezone = "UTC"
	}
	location, err := time.LoadLocation(trimmedTimezone)
	if err != nil {
		return ScheduleSpec{}, fmt.Errorf("invalid timezone: %w", err)
	}

	spec := ScheduleSpec{
		Kind:     normalizedKind,
		Timezone: trimmedTimezone,
		location: location,
	}

	switch normalizedKind {
	case ScheduleKindCron:
		if cronExpr == nil || strings.TrimSpace(*cronExpr) == "" {
			return ScheduleSpec{}, fmt.Errorf("cron schedule requires cron expression")
		}
		trimmedExpr := strings.TrimSpace(*cronExpr)
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		parsed, parseErr := parser.Parse(trimmedExpr)
		if parseErr != nil {
			return ScheduleSpec{}, fmt.Errorf("invalid cron expression: %w", parseErr)
		}
		spec.CronExpr = trimmedExpr
		spec.cronSchedule = parsed
	case ScheduleKindInterval:
		if intervalMS == nil {
			return ScheduleSpec{}, fmt.Errorf("interval schedule requires interval_ms")
		}
		if *intervalMS <= 0 {
			return ScheduleSpec{}, fmt.Errorf("interval_ms must be greater than zero")
		}
		spec.Interval = time.Duration(*intervalMS) * time.Millisecond
	case ScheduleKindOnce:
		if runAt == nil {
			return ScheduleSpec{}, fmt.Errorf("once schedule requires run_at")
		}
		spec.RunAt = runAt.UTC()
	}

	return spec, nil
}

func ComputeNextRun(spec ScheduleSpec, now time.Time, lastRunAt *time.Time) (*time.Time, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	if spec.location == nil {
		location, err := time.LoadLocation(firstNonEmpty(strings.TrimSpace(spec.Timezone), "UTC"))
		if err != nil {
			return nil, fmt.Errorf("invalid timezone: %w", err)
		}
		spec.location = location
	}

	switch spec.Kind {
	case ScheduleKindInterval:
		if spec.Interval <= 0 {
			return nil, fmt.Errorf("interval schedule requires a positive interval")
		}
		base := now
		if lastRunAt != nil && !lastRunAt.IsZero() {
			base = lastRunAt.UTC()
		}
		next := base.Add(spec.Interval)
		return &next, nil
	case ScheduleKindCron:
		if strings.TrimSpace(spec.CronExpr) == "" {
			return nil, fmt.Errorf("cron schedule requires cron expression")
		}
		if spec.cronSchedule == nil {
			parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
			parsed, err := parser.Parse(spec.CronExpr)
			if err != nil {
				return nil, fmt.Errorf("invalid cron expression: %w", err)
			}
			spec.cronSchedule = parsed
		}

		reference := now
		if lastRunAt != nil && !lastRunAt.IsZero() && lastRunAt.UTC().After(reference) {
			reference = lastRunAt.UTC()
		}
		nextLocal := spec.cronSchedule.Next(reference.In(spec.location))
		next := nextLocal.UTC()
		return &next, nil
	case ScheduleKindOnce:
		if spec.RunAt.IsZero() {
			return nil, fmt.Errorf("once schedule requires run_at")
		}
		runAt := spec.RunAt.UTC()
		if lastRunAt != nil && !lastRunAt.IsZero() && !lastRunAt.UTC().Before(runAt) {
			return nil, nil
		}
		return &runAt, nil
	default:
		return nil, fmt.Errorf("unsupported schedule kind: %s", spec.Kind)
	}
}

func normalizeScheduleKind(raw string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	switch normalized {
	case ScheduleKindCron, ScheduleKindInterval, ScheduleKindOnce:
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid schedule kind")
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
