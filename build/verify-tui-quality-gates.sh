#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

echo "[tui-gates] unit: terminal matrix + quality gate helpers"
go test ./internal/tui -count=1 -run 'TestTerminalMatrixNativeAndTmux|TestQualityGateFailuresReportsOutOfBudgetValues'

echo "[tui-gates] integration: degraded mode + replay stability + perf budgets"
go test ./internal/tui -tags integration -count=1 -run 'TestDegradedModeBannerShowsRecoveryGuidance|TestRealtimeClientExtendedReplayStability|TestTUIPerformanceBudgets'

echo "[tui-gates] e2e: first-run cold-open/tour/proof-of-life"
go test ./internal/tui -tags e2e -count=1 -run 'TestFirstRunColdOpenTourAndProofOfLife|TestTmuxCommandPaletteFallbackWorkflow'

if [[ "${OTTERCAMP_TUI_RUN_SOAK:-0}" == "1" ]]; then
  soak_duration="${OTTERCAMP_TUI_SOAK_DURATION:-60m}"
  soak_timeout="${OTTERCAMP_TUI_SOAK_TIMEOUT:-65m}"
  echo "[tui-gates] soak: synthetic realtime stream (${soak_duration})"
  OTTERCAMP_TUI_SOAK_DURATION="$soak_duration" \
    go test ./internal/tui -tags integration -count=1 -run 'TestRealtimeSyntheticSoakNoPanic' -timeout "$soak_timeout"
else
  echo "[tui-gates] soak skipped (set OTTERCAMP_TUI_RUN_SOAK=1, default duration=60m)"
fi
