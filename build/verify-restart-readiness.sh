#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

mkdir -p bin

echo "[restart-smoke] build ottercamp binary"
go build -o bin/ottercamp ./cmd/ottercamp

echo "[restart-smoke] kickoff retry stays single-project and blocks duplicate follow-on mutations"
go test ./internal/turn -tags integration -count=1 -run 'TestTurnEngineIntegrationFreshKickoffRetryKeepsSingleProjectAndSession'

echo "[restart-smoke] queued task reaches in_progress with live execution state"
go test ./internal/controlplane -tags integration -count=1 -run 'TestTaskQueueProcessorIntegrationQueuedFlowTaskStartsFlowAndRun'

echo "[restart-smoke] task writes output and advances to review"
go test ./internal/turn -tags integration -count=1 -run 'TestTurnEngineIntegrationSmokeTaskWritesOutputAndAdvancesToReview'

echo "[restart-smoke] long-running task keeps momentum across continuation and resume"
go test ./internal/turn -tags integration -count=1 -run 'TestTurnEngineIntegrationContentMigrationCheckpointPushesFirstOutputBeforeMoreScaffolding|TestTurnEngineIntegrationContentMigrationResumeUsesPersistedCheckpointState'

echo "[restart-smoke] TUI project/task/worker state matches live runtime truth"
go test ./internal/tui -tags integration -count=1 -run 'TestSmokeRuntimeTruthProjectTaskAndWorkerSignals'

echo "[restart-smoke] PASS"
