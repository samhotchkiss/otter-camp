//go:build integration

package gateway

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestHealthCheckerIntegrationDegradesAfterTwoFailures(t *testing.T) {
	checker := NewHealthChecker()
	connectionID := uuid.New()

	checker.RecordFailure(connectionID, errors.New("timeout"))
	checker.RecordFailure(connectionID, errors.New("timeout"))

	if state := checker.GetState(connectionID); state != HealthStateDegraded {
		t.Fatalf("state = %q, want %q", state, HealthStateDegraded)
	}
}
