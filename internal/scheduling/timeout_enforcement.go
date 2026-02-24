package scheduling

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MaxDurationTimedOutTask describes a scheduled task that exceeded task_schedule.max_duration_ms.
// Task 053's supervisor stuck-task scan must consume this output and mark these tasks as
// "timed_out" (not "stuck").
type MaxDurationTimedOutTask struct {
	TaskID        uuid.UUID
	ScheduleID    uuid.UUID
	MaxDurationMS int64
	ElapsedMS     int64
}

// MaxDurationChecker is the supervisor extension point for schedule timeout enforcement.
//
// Task 053 must call this during its stuck-task scan using SQL equivalent to:
//
//	SELECT pt.id, pt.schedule_id, ts.max_duration_ms,
//	       EXTRACT(EPOCH FROM ($1 - pt.created_at)) * 1000 AS elapsed_ms
//	FROM project_task pt
//	JOIN task_schedule ts ON ts.id = pt.schedule_id
//	WHERE pt.schedule_id IS NOT NULL
//	  AND ts.max_duration_ms IS NOT NULL
//	  AND pt.work_status NOT IN ('done', 'cancelled')
//	  AND EXTRACT(EPOCH FROM ($1 - pt.created_at)) * 1000 > ts.max_duration_ms
//
// Returned rows are task candidates to transition to timed_out.
type MaxDurationChecker interface {
	ListTimedOutScheduledTasks(ctx context.Context, asOf time.Time) ([]MaxDurationTimedOutTask, error)
}

// NoopMaxDurationChecker preserves the supervisor integration surface until task 053 wires
// actual timeout scanning against project_task and task_schedule.
type NoopMaxDurationChecker struct{}

func (NoopMaxDurationChecker) ListTimedOutScheduledTasks(context.Context, time.Time) ([]MaxDurationTimedOutTask, error) {
	return nil, nil
}
