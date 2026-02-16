## Summary

The bridge's self-healing reconnect loop handles transient drops but silently fails on prolonged disconnects. When one side (especially OpenClaw) stays down for minutes/hours, the bridge just retries forever without escalating. This caused a multi-hour outage on Feb 10 where the bridge was running but not functional — the dashboard showed "Bridge offline" and nobody was alerted.

## Problem

On Feb 10, the bridge process ran continuously since Sunday but lost its OpenClaw WebSocket connection. The OtterCamp reconnect loop worked fine, but the OpenClaw side failed silently. The err log showed repeated `not connected to OpenClaw` errors with no escalation. A manual restart fixed it instantly.

Spec #109 (Bridge Reliability & Self-Healing) added reconnect logic and heartbeat monitoring, but it lacks:

1. **Escalation after sustained failure** — no alert after N consecutive failures
2. **Self-restart capability** — can't recover from gateway-side WS corruption
3. **Accurate health reporting** — `/health` endpoint doesn't distinguish "process alive" from "both connections active"

## Required Changes

### 1. Connection Health Tracking

Track consecutive failures and duration of disconnect per connection (OpenClaw, OtterCamp):

```typescript
interface ConnectionHealth {
  role: 'openclaw' | 'ottercamp';
  connected: boolean;
  lastConnectedAt: Date | null;
  disconnectedSince: Date | null;
  consecutiveFailures: number;
  totalReconnectAttempts: number;
}
```

### 2. Escalation Tiers

| Condition | Action |
|-----------|--------|
| 5 consecutive failures (~30s) | Log warning |
| 30 consecutive failures (~3min) | Push alert to OtterCamp activity feed (if connected) |
| 60 consecutive failures (~5min) | Self-restart (re-exec the bridge process) |
| Self-restart fails twice | Write to err log, exit with non-zero code (let process manager handle it) |

### 3. Health Endpoint Enhancement

`GET /health` (already exists on :8787) should return:

```json
{
  "status": "degraded",
  "openclaw": {
    "connected": false,
    "disconnectedSince": "2026-02-10T18:00:00Z",
    "consecutiveFailures": 47
  },
  "ottercamp": {
    "connected": true,
    "lastConnectedAt": "2026-02-10T19:30:00Z",
    "consecutiveFailures": 0
  },
  "uptime": "48h12m",
  "lastSuccessfulSync": "2026-02-10T17:55:00Z"
}
```

Status values: `healthy` (both connected), `degraded` (one side down), `disconnected` (both down).

### 4. Self-Restart Mechanism

When escalation hits the restart tier:
- Log the restart reason
- Flush any pending sync data
- `process.exit(1)` — rely on the process manager (launchd/systemd/pm2) to restart
- OR: `child_process.execFile(process.argv[0], process.argv.slice(1))` for self-re-exec

### 5. Dashboard Integration

The "Bridge offline" banner in the frontend should show more detail when the bridge is degraded:
- "Bridge connected but OpenClaw unreachable" vs "Bridge offline"
- Time since last successful sync

## Files to Modify

- `bridge/openclaw-bridge.ts` — connection health tracking, escalation logic, self-restart, enhanced health endpoint
- `bridge/openclaw-bridge.test.ts` — tests for escalation tiers
- `web/src/components/BridgeStatus.tsx` (or equivalent) — degraded state display

## Acceptance Criteria

- [ ] Bridge self-restarts after 5 minutes of sustained disconnect on either side
- [ ] Health endpoint accurately reports per-connection status
- [ ] Alert pushed to OtterCamp after 3 minutes of disconnect
- [ ] Self-restart logged with reason
- [ ] Dashboard shows degraded state (not just online/offline binary)

## Execution Log
- [2026-02-10 14:03 MST] Issue #695 | Commit n/a | created | Planned bridge health payload enhancement slice with explicit bridge test commands | Tests: n/a
- [2026-02-10 14:03 MST] Issue #696 | Commit n/a | created | Planned reconnect escalation tiers (warn/alert/restart) with explicit bridge test commands | Tests: n/a
- [2026-02-10 14:03 MST] Issue #697 | Commit n/a | created | Planned dashboard degraded/offline UX slice with explicit web test command | Tests: n/a
- [2026-02-10 14:03 MST] Issue #695 | Commit n/a | in-progress | Spec moved to 02-in-progress and execution branch codex/spec-122-bridge-escalation-prolonged-disconnect created from origin/main | Tests: n/a
- [2026-02-10 14:07 MST] Issue #695 | Commit 217c097 | pushed | Added per-connection bridge health payload metadata, formatted uptime, and lastSuccessfulSync tracking | Tests: npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts && npm run test:bridge
- [2026-02-10 14:07 MST] Issue #695 | Commit 217c097 | closed | Closed GitHub issue after push with implementation summary and explicit test evidence | Tests: n/a
- [2026-02-10 14:10 MST] Issue #696 | Commit ee907aa | pushed | Added reconnect escalation tiers (5/30/60), alert queuing, and restart escalation guard state | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts && npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts && npm run test:bridge
- [2026-02-10 14:10 MST] Issue #696 | Commit ee907aa | closed | Closed GitHub issue with follow-up comment containing clean summary and test evidence after shell quoting hiccup | Tests: n/a
- [2026-02-10 14:12 MST] Issue #697 | Commit 6c054aa | pushed | Updated dashboard degraded/offline messaging and added last successful sync timing detail with tests | Tests: cd web && npm test -- DashboardLayout.test.tsx --run
- [2026-02-10 14:12 MST] Issue #697 | Commit 6c054aa | closed | Closed GitHub issue after posting implementation summary and test evidence comment | Tests: n/a
- [2026-02-10 14:12 MST] Issue #697 | Commit 6c054aa | opened-pr | Opened PR #698 for spec-122 reviewer visibility and linked closed implementation issues | Tests: n/a
- [2026-02-10 14:12 MST] Issue #697 | Commit 6c054aa | moved-to-needs-review | Implementation complete; spec moved from 02-in-progress to 03-needs-review pending external review | Tests: n/a

- [2026-02-10 14:30 MST] Issue #699 | Commit n/a | in-progress | Reviewer-required follow-up started; spec moved from 01-ready to 02-in-progress and execution resumed on branch codex/spec-122-bridge-escalation-prolonged-disconnect | Tests: n/a
- [2026-02-10 14:31 MST] Issue #700 | Commit n/a | created | Split reviewer P1 into dedicated rebase/conflict micro-issue with explicit full regression gates | Tests: go vet ./... && go build ./... && npx vitest run && cd web && npx vitest run
- [2026-02-10 14:31 MST] Issue #701 | Commit n/a | created | Split reviewer P2 restart escalation side-effect coverage into dedicated bridge test micro-issue | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts && npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts && npm run test:bridge
- [2026-02-10 14:31 MST] Issue #702 | Commit n/a | created | Split reviewer P2 alert queue side-effect coverage into dedicated bridge test micro-issue | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts && npx vitest run bridge/__tests__/openclaw-bridge.activity-buffer.test.ts && npm run test:bridge
- [2026-02-10 14:31 MST] Issue #700 | Commit n/a | in-progress | Started rebase/conflict-resolution implementation for reviewer-required mergeability fix | Tests: n/a
- [2026-02-10 14:34 MST] Issue #700 | Commit 1a82fda | pushed | Rebasing and conflict-resolution follow-up published; PR #698 merge conflict cleared without force-push via ancestry-preserving merge workaround | Tests: go vet ./... && go build ./... && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.health-endpoint.test.ts && npm run test:bridge && cd web && npx vitest run
- [2026-02-10 14:34 MST] Issue #700 | Commit 1a82fda | closed | Closed rebase/conflict micro-issue with commit/test evidence and updated parent tracker #699 | Tests: n/a
- [2026-02-10 14:34 MST] Issue #701 | Commit n/a | in-progress | Started TDD for restart escalation side-effect integration coverage in bridge connection-state tests | Tests: n/a

- [2026-02-10 14:40 MST] Issue #701 | Commit e324398 | pushed | Added restart escalation side-effect integration tests and reconnect-state test getter via TDD | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts
- [2026-02-10 14:40 MST] Issue #701 | Commit e324398 | closed | Closed restart escalation micro-issue with commit/test evidence | Tests: n/a
- [2026-02-10 14:40 MST] Issue #702 | Commit 3bf275f | pushed | Added alert queue side-effect integration tests (threshold, dedupe, disconnected skip) and focused bridge test hooks | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts && npm run test:bridge
- [2026-02-10 14:40 MST] Issue #702 | Commit 3bf275f | closed | Closed alert queue micro-issue with commit/test evidence | Tests: n/a
- [2026-02-10 14:40 MST] Issue #699 | Commit 1a82fda,e324398,3bf275f | closed | Parent reviewer-follow-up tracker closed after all child micro-issues were completed and validated | Tests: go vet ./... && go build ./... && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.health-endpoint.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts && npm run test:bridge && cd web && npx vitest run
- [2026-02-10 14:40 MST] Issue #699 | Commit 1a82fda,e324398,3bf275f | moved-to-needs-review | Reviewer-required changes complete; removed top-level reviewer block and moved spec from 02-in-progress to 03-needs-review for external validation | Tests: n/a
- [2026-02-10 14:51 MST] Issue #703 | Commit n/a | in-progress | Reviewer-required mergeability fix started; spec moved from 01-ready to 02-in-progress on branch codex/spec-122-bridge-escalation-prolonged-disconnect | Tests: n/a
- [2026-02-10 14:52 MST] Issue #703 | Commit n/a | validated | Verified branch sync with origin/main (already up to date), removed resolved top-level reviewer block, and passed full pre-merge gate for reviewer-required mergeability fix | Tests: go vet ./... && go build ./... && go test ./... && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.health-endpoint.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts && cd web && npx vitest run
- [2026-02-10 14:52 MST] Issue #703 | Commit n/a | moved-to-needs-review | Reviewer-required changes complete; spec moved from 02-in-progress to 03-needs-review for external validation | Tests: n/a
- [2026-02-10 15:02 MST] Issue #708 | Commit n/a | in-progress | New reviewer-required cycle started; spec moved from 01-ready to 02-in-progress on branch codex/spec-122-bridge-escalation-prolonged-disconnect | Tests: n/a
- [2026-02-10 15:02 MST] Issue #708 | Commit n/a | created | Planned branch hygiene and pre-merge conflict gate micro-issue for reviewer P1/P2 branch-scope findings | Tests: git merge main --no-commit --no-ff && git merge --abort && go vet ./... && go build ./... && npx vitest run && cd web && npx vitest run
- [2026-02-10 15:02 MST] Issue #709 | Commit n/a | created | Planned alert threshold retry semantics micro-issue with disconnected-at-30 regression test requirement | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "disconnected at alert threshold" && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts && npm run test:bridge
- [2026-02-10 15:02 MST] Issue #710 | Commit n/a | created | Planned restart-failure reconnect timer micro-issue with attempt-60 failure regression coverage | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "restart failure" && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts && npm run test:bridge
- [2026-02-10 15:02 MST] Issue #711 | Commit n/a | created | Planned disconnectedSince sentinel initialization fix and health payload null regression coverage | Tests: npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts -t "disconnectedSince" && npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts && npm run test:bridge
- [2026-02-10 15:02 MST] Issue #712 | Commit n/a | created | Planned OtterCamp close-path escalation parity tests with dedicated trigger helper | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "OtterCamp" && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts && npm run test:bridge
- [2026-02-10 15:02 MST] Issue #713 | Commit n/a | created | Planned alert dedupe reset reconnect-cycle regression test micro-issue | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "reconnect" && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts && npm run test:bridge
- [2026-02-10 15:09 MST] Issue #708 | Commit 6fb5542 | pushed | Added root Vitest config to make reviewer-required `npx vitest run` gate deterministic for bridge suites | Tests: git merge origin/main --no-commit --no-ff && git merge --abort && go vet ./... && go build ./... && npx vitest run && cd web && npx vitest run
- [2026-02-10 15:09 MST] Issue #708 | Commit 6fb5542 | closed | Closed branch-hygiene micro-issue after mergeability, diff-scope, and regression gate validation passed | Tests: n/a
- [2026-02-10 15:09 MST] Issue #709 | Commit df884ad | pushed | Fixed alert tier retry semantics and added missed-attempt-30 reconnect regression coverage | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "after threshold once ottercamp reconnects" && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.health-endpoint.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts && npm run test:bridge
- [2026-02-10 15:09 MST] Issue #709 | Commit df884ad | closed | Closed alert-threshold retry micro-issue with commit/test evidence | Tests: n/a
- [2026-02-10 15:09 MST] Issue #710 | Commit df884ad | closed | Closed restart-failure reconnect scheduling micro-issue with timer-state regression coverage | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "restart hook failure"
- [2026-02-10 15:09 MST] Issue #711 | Commit df884ad | closed | Closed disconnectedSince sentinel initialization micro-issue with health payload null regression test | Tests: npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts -t "disconnectedSince"
- [2026-02-10 15:09 MST] Issue #712 | Commit df884ad | closed | Closed OtterCamp close-path escalation parity micro-issue with dedicated trigger helper and parity tests | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "ottercamp close"
- [2026-02-10 15:09 MST] Issue #713 | Commit df884ad | closed | Closed alert dedupe reconnect-cycle micro-issue with full outage-reset-outage regression coverage | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "dedupe after reconnect"
- [2026-02-10 15:09 MST] Issue #713 | Commit 6fb5542,df884ad | updated-pr | Updated PR #698 with reviewer-facing implementation summary and full validation evidence for issues #708-#713 | Tests: go vet ./... && go build ./... && go test ./... && npx vitest run && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.health-endpoint.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts && npm run test:bridge && cd web && npx vitest run
- [2026-02-10 15:10 MST] Issue #713 | Commit 6fb5542,df884ad | moved-to-needs-review | Reviewer-required P1/P2/P3 items resolved, top-level reviewer block removed, and spec moved from 02-in-progress to 03-needs-review for external validation | Tests: n/a
- [2026-02-10 15:10 MST] Issue #704 | Commit 6fb5542,df884ad | closed | Reconciled legacy duplicate reviewer issue by linking merged branch-hygiene validation and mergeability evidence from current cycle | Tests: git merge origin/main --no-commit --no-ff && git merge --abort && go vet ./... && go build ./... && npx vitest run && cd web && npx vitest run
- [2026-02-10 15:10 MST] Issue #705 | Commit df884ad | closed | Reconciled legacy duplicate alert-retry reviewer issue with current-cycle fix and regression coverage evidence | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "after threshold once ottercamp reconnects" && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts
- [2026-02-10 15:10 MST] Issue #706 | Commit df884ad | closed | Reconciled legacy duplicate restart-scheduling reviewer issue with current-cycle reconnect-timer fix evidence | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts -t "restart hook failure" && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts
- [2026-02-10 15:10 MST] Issue #707 | Commit df884ad | closed | Reconciled legacy duplicate disconnectedSince/test-coverage reviewer issue with current-cycle sentinel initialization and parity coverage evidence | Tests: npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts -t "disconnectedSince" && npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts
- [2026-02-10 15:20 MST] Issue #714 | Commit n/a | in-progress | Reviewer-required P0 runtime regression cycle started; spec moved from 01-ready to 02-in-progress on branch codex/spec-122-bridge-escalation-prolonged-disconnect | Tests: n/a
- [2026-02-10 15:21 MST] Issue #714 | Commit n/a | validated-failing | Confirmed expected pre-fix failure with missing `recentCompactionRecoveryByKey` references via strict TypeScript grep check | Tests: cd web && npx tsc --noEmit --strict ../bridge/openclaw-bridge.ts 2>&1 | grep recentCompaction
- [2026-02-10 15:22 MST] Issue #714 | Commit 1defaa4 | pushed | Restored `recentCompactionRecoveryByKey` declaration at module scope before `lastSuccessfulSyncAtMs` and pushed branch update | Tests: cd web && npx tsc --noEmit --strict ../bridge/openclaw-bridge.ts 2>&1 | grep recentCompaction || true && npx vitest run bridge/__tests__/ && npm run test:bridge
- [2026-02-10 15:22 MST] Issue #714 | Commit 1defaa4 | closed | Closed GitHub issue and posted implementation/test evidence comment | Tests: n/a
- [2026-02-10 15:23 MST] Issue #714 | Commit 1defaa4 | updated-pr | Updated PR #698 with reviewer-facing P0 regression fix summary and validation evidence | Tests: n/a
- [2026-02-10 15:23 MST] Issue #714 | Commit 1defaa4 | moved-to-needs-review | Resolved reviewer-required P0 block, removed top-level reviewer-required section, and moved spec from 02-in-progress to 03-needs-review for external validation | Tests: n/a
