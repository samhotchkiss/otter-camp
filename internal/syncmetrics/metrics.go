package syncmetrics

import (
	"strings"
	"sync"
	"time"
)

type JobMetrics struct {
	PickedTotal        int64 `json:"picked_total"`
	SuccessTotal       int64 `json:"success_total"`
	FailureTotal       int64 `json:"failure_total"`
	RetryTotal         int64 `json:"retry_total"`
	DeadLetterTotal    int64 `json:"dead_letter_total"`
	ReplayTotal        int64 `json:"replay_total"`
	TotalLatencyMillis int64 `json:"total_latency_millis"`
}

type QuotaMetrics struct {
	Limit          int       `json:"limit"`
	Remaining      int       `json:"remaining"`
	ResetAt        time.Time `json:"reset_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ThrottleEvents int64     `json:"throttle_events"`
}

type Snapshot struct {
	Jobs        map[string]JobMetrics   `json:"jobs"`
	Quota       map[string]QuotaMetrics `json:"quota"`
	GeneratedAt time.Time               `json:"generated_at"`
}

type registry struct {
	mu    sync.RWMutex
	jobs  map[string]*JobMetrics
	quota map[string]*QuotaMetrics
}

var globalRegistry = newRegistry()

func newRegistry() *registry {
	return &registry{
		jobs:  make(map[string]*JobMetrics),
		quota: make(map[string]*QuotaMetrics),
	}
}

func ResetForTests() {
	globalRegistry = newRegistry()
}

func RecordJobPicked(jobType string) {
	job := globalRegistry.jobMetrics(jobType)
	job.PickedTotal++
}

func RecordJobCompleted(jobType string, latency time.Duration) {
	job := globalRegistry.jobMetrics(jobType)
	job.SuccessTotal++
	if latency > 0 {
		job.TotalLatencyMillis += latency.Milliseconds()
	}
}

func RecordJobFailure(jobType string, retryScheduled bool, deadLettered bool) {
	job := globalRegistry.jobMetrics(jobType)
	job.FailureTotal++
	if retryScheduled {
		job.RetryTotal++
	}
	if deadLettered {
		job.DeadLetterTotal++
	}
}

func RecordDeadLetterReplay(jobType string) {
	job := globalRegistry.jobMetrics(jobType)
	job.ReplayTotal++
}

func RecordQuota(jobType string, limit, remaining int, resetAt time.Time) {
	key := normalizeKey(jobType)
	if key == "" {
		return
	}

	now := time.Now().UTC()

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	quotaMetrics, ok := globalRegistry.quota[key]
	if !ok {
		quotaMetrics = &QuotaMetrics{}
		globalRegistry.quota[key] = quotaMetrics
	}
	quotaMetrics.Limit = limit
	quotaMetrics.Remaining = remaining
	quotaMetrics.ResetAt = resetAt.UTC()
	quotaMetrics.UpdatedAt = now
}

func RecordThrottle(jobType string) {
	key := normalizeKey(jobType)
	if key == "" {
		return
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	quotaMetrics, ok := globalRegistry.quota[key]
	if !ok {
		quotaMetrics = &QuotaMetrics{}
		globalRegistry.quota[key] = quotaMetrics
	}
	quotaMetrics.ThrottleEvents++
	quotaMetrics.UpdatedAt = time.Now().UTC()
}

func SnapshotNow() Snapshot {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	snapshot := Snapshot{
		Jobs:        make(map[string]JobMetrics, len(globalRegistry.jobs)),
		Quota:       make(map[string]QuotaMetrics, len(globalRegistry.quota)),
		GeneratedAt: time.Now().UTC(),
	}

	for key, metrics := range globalRegistry.jobs {
		snapshot.Jobs[key] = *metrics
	}
	for key, metrics := range globalRegistry.quota {
		snapshot.Quota[key] = *metrics
	}

	return snapshot
}

func (r *registry) jobMetrics(jobType string) *JobMetrics {
	key := normalizeKey(jobType)
	if key == "" {
		key = "unknown"
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	metrics, ok := r.jobs[key]
	if !ok {
		metrics = &JobMetrics{}
		r.jobs[key] = metrics
	}
	return metrics
}

func normalizeKey(raw string) string {
	return strings.TrimSpace(strings.ToLower(raw))
}
