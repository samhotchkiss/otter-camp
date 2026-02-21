package importer

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/scheduler"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

const openClawCronSyncMetadataKey = "openclaw_cron_jobs"

type OpenClawCronJobMetadata struct {
	ID            string `json:"id"`
	Name          string `json:"name,omitempty"`
	Schedule      string `json:"schedule,omitempty"`
	SessionTarget string `json:"session_target,omitempty"`
	PayloadType   string `json:"payload_type,omitempty"`
	PayloadText   string `json:"payload_text,omitempty"`
}

type OpenClawCronJobImportResult struct {
	Total    int      `json:"total"`
	Imported int      `json:"imported"`
	Updated  int      `json:"updated"`
	Skipped  int      `json:"skipped"`
	Warnings []string `json:"warnings,omitempty"`
}

type OpenClawCronJobImporter struct {
	db       *sql.DB
	jobStore *store.AgentJobStore
	now      func() time.Time
}

func NewOpenClawCronJobImporter(db *sql.DB) *OpenClawCronJobImporter {
	return &OpenClawCronJobImporter{
		db:       db,
		jobStore: store.NewAgentJobStore(db),
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (i *OpenClawCronJobImporter) ImportFromSyncMetadata(
	ctx context.Context,
	orgID string,
) (OpenClawCronJobImportResult, error) {
	if i == nil || i.db == nil {
		return OpenClawCronJobImportResult{}, errors.New("openclaw cron job importer is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return OpenClawCronJobImportResult{}, errors.New("org id is required")
	}

	records, err := i.loadMetadata(ctx)
	if err != nil {
		return OpenClawCronJobImportResult{}, err
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, orgID)
	result := OpenClawCronJobImportResult{Total: len(records)}

	for _, record := range records {
		if strings.TrimSpace(record.ID) == "" {
			record.ID = fallbackOpenClawCronID(record)
		}
		if strings.TrimSpace(record.ID) == "" {
			result.Skipped++
			result.Warnings = append(result.Warnings, "skip <unknown>: missing cron metadata id")
			continue
		}

		agentID, resolveErr := i.resolveTargetAgentID(workspaceCtx, orgID, record.SessionTarget)
		if resolveErr != nil {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip %s: %v", record.ID, resolveErr))
			continue
		}

		schedule, scheduleErr := parseOpenClawCronSchedule(record.Schedule)
		if scheduleErr != nil {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip %s: %v", record.ID, scheduleErr))
			continue
		}
		if _, normalizeErr := scheduler.NormalizeScheduleSpec(
			schedule.Kind,
			schedule.CronExpr,
			schedule.IntervalMS,
			schedule.RunAt,
			firstNonEmptyString(derefString(schedule.Timezone), "UTC"),
		); normalizeErr != nil {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip %s: invalid schedule: %v", record.ID, normalizeErr))
			continue
		}

		payloadKind := normalizeOpenClawPayloadKind(record.PayloadType)
		payloadText := strings.TrimSpace(record.PayloadText)
		if payloadText == "" {
			payloadText = strings.TrimSpace(record.Name)
		}
		if payloadText == "" {
			payloadText = fmt.Sprintf("Imported OpenClaw cron job %s", record.ID)
		}

		marker := fmt.Sprintf("[openclaw-cron-id:%s]", record.ID)
		description := buildOpenClawCronDescription(record, marker)
		disabled := false
		paused := store.AgentJobStatusPaused

		existingID, findErr := i.findImportedJobID(workspaceCtx, agentID, marker)
		if findErr != nil {
			return result, findErr
		}

		if existingID == "" {
			if _, createErr := i.jobStore.Create(workspaceCtx, store.CreateAgentJobInput{
				AgentID:      agentID,
				Name:         firstNonEmptyString(strings.TrimSpace(record.Name), "OpenClaw Cron "+record.ID),
				Description:  &description,
				ScheduleKind: schedule.Kind,
				CronExpr:     schedule.CronExpr,
				IntervalMS:   schedule.IntervalMS,
				RunAt:        schedule.RunAt,
				Timezone:     schedule.Timezone,
				PayloadKind:  payloadKind,
				PayloadText:  payloadText,
				Enabled:      &disabled,
				Status:       &paused,
			}); createErr != nil {
				return result, createErr
			}
			result.Imported++
			continue
		}

		if _, updateErr := i.jobStore.Update(workspaceCtx, existingID, store.UpdateAgentJobInput{
			Name:         strPtr(firstNonEmptyString(strings.TrimSpace(record.Name), "OpenClaw Cron "+record.ID)),
			Description:  &description,
			ScheduleKind: &schedule.Kind,
			CronExpr:     schedule.CronExpr,
			IntervalMS:   schedule.IntervalMS,
			RunAt:        schedule.RunAt,
			Timezone:     schedule.Timezone,
			PayloadKind:  &payloadKind,
			PayloadText:  &payloadText,
			Enabled:      &disabled,
			Status:       &paused,
		}); updateErr != nil {
			return result, updateErr
		}
		result.Updated++
	}

	return result, nil
}

func (i *OpenClawCronJobImporter) loadMetadata(ctx context.Context) ([]OpenClawCronJobMetadata, error) {
	var raw string
	err := i.db.QueryRowContext(
		ctx,
		`SELECT value FROM sync_metadata WHERE key = $1`,
		openClawCronSyncMetadataKey,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load openclaw cron metadata: %w", err)
	}

	records := []OpenClawCronJobMetadata{}
	if strings.TrimSpace(raw) == "" {
		return records, nil
	}
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, fmt.Errorf("parse openclaw cron metadata: %w", err)
	}

	sort.Slice(records, func(a, b int) bool {
		left := strings.TrimSpace(records[a].ID)
		right := strings.TrimSpace(records[b].ID)
		if left != right {
			return left < right
		}
		left = strings.TrimSpace(records[a].Name)
		right = strings.TrimSpace(records[b].Name)
		if left != right {
			return left < right
		}
		return strings.TrimSpace(records[a].SessionTarget) < strings.TrimSpace(records[b].SessionTarget)
	})
	return records, nil
}

func (i *OpenClawCronJobImporter) findImportedJobID(ctx context.Context, agentID, marker string) (string, error) {
	conn, err := store.WithWorkspace(ctx, i.db)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	var id string
	err = conn.QueryRowContext(
		ctx,
		`SELECT id
		 FROM agent_jobs
		 WHERE agent_id = $1
		   AND description LIKE $2
		 ORDER BY created_at ASC
		 LIMIT 1`,
		agentID,
		"%"+marker+"%",
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("query imported openclaw cron job: %w", err)
	}
	return id, nil
}

func (i *OpenClawCronJobImporter) resolveTargetAgentID(ctx context.Context, orgID, target string) (string, error) {
	identifier := strings.TrimSpace(target)
	if identifier == "" {
		return "", errors.New("missing session_target")
	}

	lower := strings.ToLower(identifier)
	if strings.HasPrefix(lower, "agent:chameleon:oc:") {
		identifier = strings.TrimSpace(identifier[len("agent:chameleon:oc:"):])
	}

	conn, err := store.WithWorkspace(ctx, i.db)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	var agentID string
	err = conn.QueryRowContext(
		ctx,
		`SELECT id
		 FROM agents
		 WHERE org_id = $1
		   AND (id::text = $2 OR LOWER(slug) = LOWER($2) OR LOWER(display_name) = LOWER($2))
		 LIMIT 1`,
		orgID,
		identifier,
	).Scan(&agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("agent not found for session_target %q", target)
		}
		return "", err
	}
	return agentID, nil
}

type parsedOpenClawCronSchedule struct {
	Kind       string
	CronExpr   *string
	IntervalMS *int64
	RunAt      *time.Time
	Timezone   *string
}

func parseOpenClawCronSchedule(raw string) (parsedOpenClawCronSchedule, error) {
	schedule := strings.TrimSpace(raw)
	if schedule == "" {
		return parsedOpenClawCronSchedule{}, errors.New("missing schedule")
	}

	lower := strings.ToLower(schedule)
	if strings.HasPrefix(lower, "every ") || strings.HasPrefix(lower, "interval ") {
		parts := strings.SplitN(schedule, " ", 2)
		if len(parts) != 2 {
			return parsedOpenClawCronSchedule{}, errors.New("invalid interval schedule")
		}
		duration, err := time.ParseDuration(strings.TrimSpace(parts[1]))
		if err != nil || duration <= 0 {
			return parsedOpenClawCronSchedule{}, errors.New("invalid interval duration")
		}
		ms := duration.Milliseconds()
		if ms <= 0 {
			return parsedOpenClawCronSchedule{}, errors.New("interval must be at least 1ms")
		}
		return parsedOpenClawCronSchedule{
			Kind:       store.AgentJobScheduleInterval,
			IntervalMS: &ms,
			Timezone:   strPtr("UTC"),
		}, nil
	}

	if strings.HasPrefix(lower, "at ") {
		parts := strings.SplitN(schedule, " ", 2)
		if len(parts) != 2 {
			return parsedOpenClawCronSchedule{}, errors.New("invalid once schedule")
		}
		runAt, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[1]))
		if err != nil {
			return parsedOpenClawCronSchedule{}, errors.New("invalid once run_at timestamp")
		}
		utc := runAt.UTC()
		return parsedOpenClawCronSchedule{
			Kind:     store.AgentJobScheduleOnce,
			RunAt:    &utc,
			Timezone: strPtr("UTC"),
		}, nil
	}

	expr := schedule
	timezone := "UTC"
	fields := strings.Fields(schedule)
	if len(fields) > 1 && strings.HasPrefix(strings.ToUpper(fields[0]), "CRON_TZ=") {
		timezone = strings.TrimSpace(strings.TrimPrefix(fields[0], "CRON_TZ="))
		expr = strings.Join(fields[1:], " ")
	}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return parsedOpenClawCronSchedule{}, errors.New("invalid cron expression")
	}
	return parsedOpenClawCronSchedule{
		Kind:     store.AgentJobScheduleCron,
		CronExpr: &expr,
		Timezone: &timezone,
	}, nil
}

func buildOpenClawCronDescription(record OpenClawCronJobMetadata, marker string) string {
	name := strings.TrimSpace(record.Name)
	if name == "" {
		name = strings.TrimSpace(record.ID)
	}
	target := strings.TrimSpace(record.SessionTarget)
	if target == "" {
		target = "unknown"
	}
	return fmt.Sprintf("Imported from OpenClaw cron job %s targeting %s %s", name, target, marker)
}

func normalizeOpenClawPayloadKind(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case store.AgentJobPayloadSystemEvent:
		return store.AgentJobPayloadSystemEvent
	default:
		return store.AgentJobPayloadMessage
	}
}

func fallbackOpenClawCronID(record OpenClawCronJobMetadata) string {
	seed := strings.TrimSpace(record.Name) + "|" + strings.TrimSpace(record.Schedule) + "|" + strings.TrimSpace(record.SessionTarget)
	if strings.TrimSpace(seed) == "||" {
		return ""
	}
	sum := sha1.Sum([]byte(seed))
	return "openclaw-" + hex.EncodeToString(sum[:6])
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func strPtr(v string) *string {
	return &v
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
