## Issue #151 — Ellie: Memory Infrastructure

> **Priority:** P1
> **Status:** Needs Review
> **Depends on:** #150 (Conversation Schema Redesign)
> **Blocks:** #152 (Ellie Proactive Context Injection)

## Summary

Ellie is the memory/retrieval infrastructure for OtterCamp agents. This spec covers ingestion, extraction, retrieval cascade behavior, retrieval strategy handling, and startup/runtime wiring needed for reliable context recall.

The latest reviewer-required remediation wave (`#904`-`#908`) is complete on branch `codex/spec-151-ellie-memory-infrastructure-r3` and is now queued for external review.

## Reviewer Remediation Completed

- `#904`: merged current `origin/main` conflict and reran full gate.
- `#905`: scoped JSONL scanning by org partition and added cross-org regression.
- `#906`: made ingestion room listing cursor-aware and hardened false-positive heuristics.
- `#907`: added planner expansion cap, rules JSON write-time validation, and shared embedder startup.
- `#908`: delivered P2 hardening (UTF-8 snippet truncation, retrieval limit clamp/truncation, migration 068 schema test, finite float parsing, and embedder-config dependency validation for context injection).

## Execution Log

- [2026-02-12 17:54 MST] Issue #904 | Commit 3ac51cb | closed | Merged origin/main into branch codex/spec-151-ellie-memory-infrastructure-r3, resolved ellie_ingestion_store conflict, and reran full pre-merge gate | Tests: go vet ./...; go build ./...; go test ./... -count=1; cd web && npx vitest run
- [2026-02-12 17:56 MST] Issue #905 | Commit c1af37d | closed | Scoped Ellie JSONL scanner to org-partitioned roots (RootDir/<orgID>) and added org-isolation regression coverage | Tests: go test ./internal/memory -run TestEllieFileJSONLScannerScopesResultsToInputOrg -count=1; go test ./internal/memory -run TestEllieFileJSONLScanner -count=1; go test ./internal/memory -count=1; go test ./... -count=1
- [2026-02-12 17:57 MST] Issue #906 | Commit n/a | in_progress | Resumed spec 151 reviewer-required remediation; moved spec to 02-in-progress and began TDD for ingestion scan/cursor and heuristic false-positive fixes | Tests: n/a
- [2026-02-12 18:00 MST] Issue #906 | Commit 35142a0 | closed | Made ListRoomsForIngestion cursor-aware and hardened ingestion heuristic matching (word-boundary operational context + safer decided-to) with added regressions | Tests: go test ./internal/memory -run TestEllieIngestionWorkerAvoidsFalsePositiveDecisionAndFactClassification -count=1; go test ./internal/store -run TestEllieIngestionStoreListRoomsForIngestionSkipsUpToDateRooms -count=1; go test ./internal/memory -count=1; go test ./internal/store -count=1; go test ./... -count=1
- [2026-02-12 18:03 MST] Issue #907 | Commit 4163fae | closed | Added planner expansion cap, strategy-rules JSON structural validation, and single shared embedder initialization across both embedding workers | Tests: go test ./internal/memory -run TestEllieRetrievalPlannerCapsTopicExpansionSteps -count=1; go test ./internal/store -run TestEllieRetrievalPlannerStoreRejectsMalformedRulesJSON -count=1; go test ./cmd/server -run TestMainConstructsSingleSharedEmbedderForEmbeddingWorkers -count=1; go test ./internal/memory -count=1; go test ./internal/store -count=1; go test ./cmd/server -count=1; go test ./... -count=1
- [2026-02-12 18:07 MST] Issue #908 | Commit 9bf8934 | closed | Completed spec-151 P2 hardening (UTF-8-safe JSONL truncation, tier-2 dedupe truncation, retrieval search-limit clamp, migration 068 schema test, finite float parsing, and context-injection embedder config validation) | Tests: go test ./internal/memory -run "TestTruncateJSONLSnippetPreservesUTF8Boundaries|TestEllieRetrievalCascadeTierTwoNeverReturnsOverLimitAfterDedupe" -count=1; go test ./internal/store -run "TestNormalizeEllieSearchLimitClampsUpperBound|TestMigration068EllieRetrievalQualityEventsFilesExistAndContainCoreDDL" -count=1; go test ./internal/config -run "TestLoadRejectsNaNContextInjectionThreshold|TestLoadRejectsInvalidEmbedderConfigWhenInjectionEnabled" -count=1; go test ./internal/memory -count=1; go test ./internal/store -count=1; go test ./internal/config -count=1; go test ./... -count=1
- [2026-02-12 18:07 MST] Issue #906/#907/#908 | Commit 35142a0/4163fae/9bf8934 | resolved | Cleared all top-level reviewer-required remediation items and transitioned spec to needs-review | Tests: see issue-level entries above

## Review (2026-02-12 18:14 MST)

**Reviewer:** Claude Opus 4.6
**Branch:** `codex/spec-151-ellie-memory-infrastructure-r3`
**PR:** #870
**Disposition:** APPROVED

### Pre-Merge Gate Results

| Check | Result |
|-------|--------|
| `git merge main --no-commit --no-ff` | Already up to date |
| `go vet ./...` | Zero errors |
| `go build ./...` | Compiles cleanly |
| `go test ./... -count=1 -short` | All packages pass (25 packages) |
| `npx vitest run` (frontend) | 94 test files, 442 tests pass |

### Branch Isolation Note

This branch contains work from specs 152 (proactive context injection), 153 (sensitivity fields), and 154 (compliance review), all of which explicitly depend on spec 151 and are already marked completed in `05-completed`. This is a sequential dependency cascade, not a branch isolation violation. Merging this branch lands all four specs onto main.

### Findings (P2 — informational, no blockers)

1. **No request body size limit on compliance rule Create/Patch** — `internal/api/compliance_rules.go:94,144`. Recommend adding `http.MaxBytesReader`.
2. **Error string matching in `handleComplianceRuleStoreError` may leak internal details** — `internal/api/compliance_rules.go:231-238`. Recommend typed/sentinel errors.
3. **`UpdateMessageEmbedding` not scoped by org_id** — `internal/store/ellie_context_injection_store.go:159-183`. Recommend adding `AND org_id = $X` for defense-in-depth.
4. **`ensureProjectScope`/`ensureConversationScope` use `interface{}` parameter type** — `internal/store/compliance_rule_store.go:463,492`. Recommend `*string`.
5. **Cooldown config `0` silently treated as default** — `internal/memory/ellie_context_injection_worker.go:76-81`.
6. **No re-enable API endpoint for disabled compliance rules** — `internal/api/compliance_rules.go`.

### Residual Risks

1. JSONL scanner uses heuristic keyword extraction (not LLM-based). Follow-up issues #850 and #851 are open.
2. `ListPendingMessagesSince` fetches messages across all orgs in the background worker — intentional design but limits future org-scoped isolation.
3. RLS policies on new tables depend on `current_org_id()` function from earlier migrations.

### Test Coverage Assessment

- New behavior tests: present and passing for all major components (ingestion, retrieval cascade, planner, quality sink, context injection, compliance).
- Regression tests: present for cursor-aware ingestion, heuristic false positives, ILIKE wildcard escaping, nil quality-sink slices, migration down paths.
- Edge cases: planner expansion caps, invalid strategy JSON, empty inputs, UTF-8 boundary truncation, cross-org JSONL isolation.
- Integration tests: `ellie_context_injection_integration_test.go` covers end-to-end injection flow.
