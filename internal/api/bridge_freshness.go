package api

import "time"

const (
	bridgeSyncHealthyWindow  = 10 * time.Second
	bridgeSyncDegradedWindow = 30 * time.Second
)

type bridgeFreshnessStatus string

const (
	bridgeFreshnessHealthy   bridgeFreshnessStatus = "healthy"
	bridgeFreshnessDegraded  bridgeFreshnessStatus = "degraded"
	bridgeFreshnessUnhealthy bridgeFreshnessStatus = "unhealthy"
)

type bridgeFreshnessSnapshot struct {
	Status             bridgeFreshnessStatus
	LastSyncAgeSeconds *int64
	SyncHealthy        bool
	Connected          bool
}

func deriveBridgeFreshness(
	lastSync *time.Time,
	now time.Time,
	transportConnected bool,
	transportKnown bool,
) bridgeFreshnessSnapshot {
	status, ageSeconds := deriveBridgeFreshnessStatus(lastSync, now)
	if transportKnown && !transportConnected {
		status = bridgeFreshnessUnhealthy
	}

	syncHealthy := status == bridgeFreshnessHealthy
	connectedByFreshness := status == bridgeFreshnessHealthy || status == bridgeFreshnessDegraded
	connected := connectedByFreshness
	if transportKnown {
		connected = transportConnected && connectedByFreshness
	}

	return bridgeFreshnessSnapshot{
		Status:             status,
		LastSyncAgeSeconds: ageSeconds,
		SyncHealthy:        syncHealthy,
		Connected:          connected,
	}
}

func deriveBridgeFreshnessStatus(lastSync *time.Time, now time.Time) (bridgeFreshnessStatus, *int64) {
	if lastSync == nil || lastSync.IsZero() {
		return bridgeFreshnessUnhealthy, nil
	}
	age := now.Sub(lastSync.UTC())
	if age < 0 {
		age = 0
	}
	ageSeconds := int64(age / time.Second)
	ageSecondsCopy := ageSeconds

	switch {
	case age <= bridgeSyncHealthyWindow:
		return bridgeFreshnessHealthy, &ageSecondsCopy
	case age <= bridgeSyncDegradedWindow:
		return bridgeFreshnessDegraded, &ageSecondsCopy
	default:
		return bridgeFreshnessUnhealthy, &ageSecondsCopy
	}
}
