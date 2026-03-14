package gateway

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type HealthState string

const (
	HealthStateHealthy     HealthState = "healthy"
	HealthStateDegraded    HealthState = "degraded"
	HealthStateRateLimited HealthState = "rate_limited"
	HealthStateUnavailable HealthState = "unavailable"
)

const (
	healthFailureWindow        = 60 * time.Second
	healthUnavailable5xxWindow = 2 * time.Minute
	healthProbeBackoffBase     = 1 * time.Second
	healthProbeBackoffMax      = 1 * time.Minute
)

type healthRecord struct {
	state               HealthState
	consecutiveFailures int
	lastFailureAt       time.Time
	fiveXXSince         *time.Time
	recoveryBackoff     time.Duration
	recoveryReadyAt     time.Time
}

type HealthChecker struct {
	mu      sync.Mutex
	now     func() time.Time
	records map[uuid.UUID]*healthRecord
}

func NewHealthChecker() *HealthChecker {
	return newHealthCheckerWithClock(time.Now)
}

func newHealthCheckerWithClock(now func() time.Time) *HealthChecker {
	if now == nil {
		now = time.Now
	}
	return &HealthChecker{
		now:     now,
		records: make(map[uuid.UUID]*healthRecord),
	}
}

func (h *HealthChecker) GetState(connectionID uuid.UUID) HealthState {
	state, _ := h.GetStateKnown(connectionID)
	return state
}

func (h *HealthChecker) GetStateKnown(connectionID uuid.UUID) (HealthState, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	record := h.records[connectionID]
	if record == nil {
		return HealthStateHealthy, false
	}
	if record.state == "" {
		return HealthStateHealthy, false
	}
	if (record.state == HealthStateUnavailable || record.state == HealthStateDegraded || record.state == HealthStateRateLimited) &&
		!record.recoveryReadyAt.IsZero() &&
		!h.now().UTC().Before(record.recoveryReadyAt) {
		return HealthStateDegraded, true
	}
	return record.state, true
}

func (h *HealthChecker) RecordSuccess(connectionID uuid.UUID) {
	now := h.now().UTC()

	h.mu.Lock()
	defer h.mu.Unlock()

	record := h.recordLocked(connectionID)
	if record.state == HealthStateHealthy {
		record.consecutiveFailures = 0
		record.lastFailureAt = time.Time{}
		record.fiveXXSince = nil
		record.recoveryBackoff = 0
		record.recoveryReadyAt = time.Time{}
		return
	}

	if !record.recoveryReadyAt.IsZero() && now.Before(record.recoveryReadyAt) {
		return
	}

	record.state = HealthStateHealthy
	record.consecutiveFailures = 0
	record.lastFailureAt = time.Time{}
	record.fiveXXSince = nil
	record.recoveryBackoff = 0
	record.recoveryReadyAt = time.Time{}
}

func (h *HealthChecker) RecordFailure(connectionID uuid.UUID, err error) {
	now := h.now().UTC()

	h.mu.Lock()
	defer h.mu.Unlock()

	record := h.recordLocked(connectionID)

	if record.lastFailureAt.IsZero() || now.Sub(record.lastFailureAt) > healthFailureWindow {
		record.consecutiveFailures = 1
	} else {
		record.consecutiveFailures++
	}
	record.lastFailureAt = now

	if isServerError(err) {
		if record.fiveXXSince == nil {
			timestamp := now
			record.fiveXXSince = &timestamp
		}
	} else {
		record.fiveXXSince = nil
	}

	switch record.state {
	case HealthStateHealthy:
		if record.consecutiveFailures >= 2 {
			record.state = HealthStateDegraded
			bumpRecoveryProbe(record, now)
		}
	case HealthStateDegraded:
		if _, rateLimited := isRateLimitedError(err); rateLimited {
			record.state = HealthStateRateLimited
			bumpRecoveryProbe(record, now)
		}
		if becomesUnavailable(record, now) {
			record.state = HealthStateUnavailable
			bumpRecoveryProbe(record, now)
		}
	case HealthStateRateLimited:
		if becomesUnavailable(record, now) {
			record.state = HealthStateUnavailable
			bumpRecoveryProbe(record, now)
		} else if _, rateLimited := isRateLimitedError(err); rateLimited {
			bumpRecoveryProbe(record, now)
		}
	case HealthStateUnavailable:
		bumpRecoveryProbe(record, now)
	default:
		record.state = HealthStateHealthy
	}
}

func (h *HealthChecker) MarkDegraded(connectionID uuid.UUID) {
	now := h.now().UTC()

	h.mu.Lock()
	defer h.mu.Unlock()

	record := h.recordLocked(connectionID)
	record.state = HealthStateDegraded
	record.lastFailureAt = now
	record.consecutiveFailures = max(record.consecutiveFailures, 2)
	bumpRecoveryProbe(record, now)
}

func (h *HealthChecker) MarkRateLimited(connectionID uuid.UUID) {
	h.MarkRateLimitedFor(connectionID, 0)
}

func (h *HealthChecker) MarkRateLimitedFor(connectionID uuid.UUID, retryAfter time.Duration) {
	now := h.now().UTC()

	h.mu.Lock()
	defer h.mu.Unlock()

	record := h.recordLocked(connectionID)
	record.state = HealthStateRateLimited
	record.lastFailureAt = now
	record.consecutiveFailures = max(record.consecutiveFailures, 2)
	if retryAfter > 0 {
		setRecoveryProbe(record, now, retryAfter)
		return
	}
	bumpRecoveryProbe(record, now)
}

func (h *HealthChecker) MarkUnavailable(connectionID uuid.UUID) {
	now := h.now().UTC()

	h.mu.Lock()
	defer h.mu.Unlock()

	record := h.recordLocked(connectionID)
	record.state = HealthStateUnavailable
	record.lastFailureAt = now
	record.consecutiveFailures = max(record.consecutiveFailures, 5)
	bumpRecoveryProbe(record, now)
}

func becomesUnavailable(record *healthRecord, now time.Time) bool {
	if record.consecutiveFailures >= 5 {
		return true
	}
	return record.fiveXXSince != nil && now.Sub(*record.fiveXXSince) > healthUnavailable5xxWindow
}

func bumpRecoveryProbe(record *healthRecord, now time.Time) {
	if record.recoveryBackoff <= 0 {
		record.recoveryBackoff = healthProbeBackoffBase
	} else {
		record.recoveryBackoff *= 2
		if record.recoveryBackoff > healthProbeBackoffMax {
			record.recoveryBackoff = healthProbeBackoffMax
		}
	}
	record.recoveryReadyAt = now.Add(record.recoveryBackoff)
}

func setRecoveryProbe(record *healthRecord, now time.Time, delay time.Duration) {
	if delay <= 0 {
		delay = healthProbeBackoffBase
	}
	record.recoveryBackoff = delay
	record.recoveryReadyAt = now.Add(delay)
}

func (h *HealthChecker) recordLocked(connectionID uuid.UUID) *healthRecord {
	record := h.records[connectionID]
	if record == nil {
		record = &healthRecord{state: HealthStateHealthy}
		h.records[connectionID] = record
	}
	return record
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
