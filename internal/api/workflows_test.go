package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseProjectWorkflowTriggerCron(t *testing.T) {
	trigger := parseProjectWorkflowTrigger(json.RawMessage(`{"kind":"cron","expr":"0 6 * * *","tz":"America/Denver"}`))
	require.Equal(t, "cron", trigger.Type)
	require.Equal(t, "0 6 * * *", trigger.Cron)
	require.Contains(t, trigger.Label, "Daily at 6:00")
	require.Contains(t, trigger.Label, "America/Denver")
}

func TestParseProjectWorkflowTriggerEvery(t *testing.T) {
	trigger := parseProjectWorkflowTrigger(json.RawMessage(`{"kind":"every","everyMs":900000}`))
	require.Equal(t, "interval", trigger.Type)
	require.Equal(t, "15m0s", trigger.Every)
	require.Equal(t, "Every 15m0s", trigger.Label)
}

func TestParseProjectWorkflowTriggerDefaultManual(t *testing.T) {
	trigger := parseProjectWorkflowTrigger(nil)
	require.Equal(t, "manual", trigger.Type)
	require.Equal(t, "Manual", trigger.Label)
}

func TestDeriveLegacyWorkflowLastStatus(t *testing.T) {
	require.Equal(t, "", deriveLegacyWorkflowLastStatus(nil))
	now := time.Now()
	require.Equal(t, "ok", deriveLegacyWorkflowLastStatus(&now))
}
