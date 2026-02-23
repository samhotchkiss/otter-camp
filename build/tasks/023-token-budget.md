# 023: Token Budget Schema and Service

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | S (≤1 day) |
| Spec refs | doc 13 §TokenBudget, doc 13 §BudgetEnforcement, doc 13 §AnomalyDetection |
| Spec status | finished |
| Depends on | 003, 016, 024 |
| Blocks | 035, 052, 077 |

## Scope

Build the `token_budget` table, repository, and budget enforcement service for all three
budget levels: org, project, and per-agent. Soft limits (warn once per period) and hard
limits (block non-essential capabilities) are both implemented. Anomaly detection background
job is included. Budget enforcement is hierarchical/additive: a single invocation is charged
to all three applicable levels simultaneously (ISSUE #23 resolved).

### Must build

**Migration:**
- `0025_token_budget.sql`

**`token_budget` table** (doc 13):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `project_id uuid references project(id) on delete cascade` — null = org-level budget
- `period text not null check (period in ('daily','weekly','monthly'))` — rolling window
- `soft_limit_tokens bigint` — null = no soft limit; warn-once threshold
- `hard_limit_tokens bigint` — null = no hard limit; block threshold
- `alert_channel text` — notification target slug (e.g. inbox item recipient); null = no alerting
- `is_enabled boolean not null default true`
- `created_by_type text not null check (created_by_type in ('human_user','system'))`
- `created_by_id uuid not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(organization_id, project_id, period)` — use partial index with COALESCE for nullable `project_id`
- Index: `(organization_id, is_enabled) WHERE is_enabled = true`
- Index: `(project_id) WHERE project_id IS NOT NULL`

**Repository layer:**
- `TokenBudgetRepo`: `Create`, `GetByID`, `GetByScope`, `List`, `Update`, `Delete`
- `GetByScope(orgID, projectID *uuid.UUID, period string)` — returns the applicable budget for a scope

**`internal/budget/service.go` — `BudgetService` interface and implementation:**

```go
type BudgetService interface {
    // Called before model invocation (in control plane broker, task 052).
    // Checks all three levels: org, project (if projectID != nil), agent (if agentID != nil).
    // Hierarchical/additive: any level exceeding its hard limit causes Allowed=false.
    CheckBudget(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, estimatedTokens int64) (*BudgetCheckResult, error)

    // Called after model invocation completes (token count known).
    // Records actual usage at all three applicable levels simultaneously.
    RecordUsage(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID, tokensUsed int64) error

    // Called by anomaly detection job
    ScanForAnomalies(ctx context.Context) error

    // CRUD
    Create(ctx context.Context, req CreateBudgetRequest) (*TokenBudget, error)
    Update(ctx context.Context, budgetID uuid.UUID, req UpdateBudgetRequest) (*TokenBudget, error)
    Delete(ctx context.Context, budgetID uuid.UUID) error
    List(ctx context.Context, orgID uuid.UUID) ([]*TokenBudget, error)
}

type BudgetCheckResult struct {
    Allowed       bool
    SoftLimitHit  bool   // true if usage is at/above soft_limit_tokens this period
    HardLimitHit  bool   // true if usage is at/above hard_limit_tokens this period
    CurrentTokens int64
    LimitTokens   int64  // whichever limit was hit (soft or hard)
}
```

**Token usage tracking:**
- Usage is computed by querying `model_invocation` (task 035/036 — forward reference) via a view or aggregation query: `SUM(input_tokens + output_tokens) WHERE organization_id = $1 AND created_at >= period_start`
- Period start computation: `daily` = start of UTC day; `weekly` = start of UTC Monday; `monthly` = first of UTC month
- Usage queries should use `model_usage_rollup` (task 036) where available, falling back to direct `model_invocation` sum for the current partial period

**Soft limit behavior:**
- When `current_usage >= soft_limit_tokens` for the first time in a period: create an `inbox_item` (type: `system_alert`) for the org admin; set a per-period warn flag to prevent duplicate alerts
- Warn flag: stored as a key in a `token_budget_soft_limit_warned` entry in a lightweight in-memory map keyed by `(budget_id, period_start)` — OR stored as a `last_warned_period` column on `token_budget` (add this column to the migration); the latter is preferred
- Add `last_warned_period text` column to `token_budget` for warn tracking (format: `2024-01-15` for daily, `2024-W03` for weekly, `2024-01` for monthly)

**Hard limit behavior:**
- When `current_usage >= hard_limit_tokens`: `CheckBudget` returns `BudgetCheckResult{Allowed: false, HardLimitHit: true}`
- Control plane broker (task 052) enforces this by failing the run before dispatch
- "Non-essential capabilities" blocked: all tier-2 tool calls except `inbox.list` and `session.create` are blocked; tier-1 calls (memory.query, file.read) are allowed
- The broker uses `BudgetCheckResult.Allowed` to make the final allow/deny decision

**Anomaly detection job** (doc 13):
- Runs every 15 minutes via job queue (job_type: `budget.anomaly_scan`)
- Threshold: current-period usage > 3× the 7-day rolling average for the same period type
- For each `token_budget` row: compute rolling average from `model_usage_rollup` for the past 7 days; compare against current-period usage
- On anomaly: create `inbox_item` (type: `system_alert`) for org admin with description including current/average token counts; also emit `budget.anomaly_detected` domain event
- Job is registered with the job queue in `BudgetService.RegisterJobs()` called at startup

### Must NOT build
- Model invocation table (task 035/036)
- Model usage rollup (task 036)
- Control plane broker enforcement wiring (task 052 uses `BudgetService.CheckBudget`)
- Budget API endpoints (covered by project API in task 019 as sub-resource, and org settings)

## Acceptance Criteria

- [ ] Migration `0025_token_budget.sql` applies cleanly; unique constraint on `(organization_id, project_id, period)` with nullable project_id handled correctly
- [ ] `TokenBudgetRepo.GetByScope` returns the correct budget for org-level scope (project_id=null) and project-level scope
- [ ] `BudgetService.CheckBudget` with usage below soft limit: returns `{Allowed: true, SoftLimitHit: false}`
- [ ] `BudgetService.CheckBudget` with usage >= soft limit but < hard limit: returns `{Allowed: true, SoftLimitHit: true}`; `last_warned_period` updated; inbox item created
- [ ] `BudgetService.CheckBudget` with usage >= hard limit: returns `{Allowed: false, HardLimitHit: true}`
- [ ] Soft limit alert sent only once per period (second call in same period does NOT create a second inbox item)
- [ ] `ScanForAnomalies`: usage 4× average → anomaly event emitted; usage 2× average → no event

## Tests Required

**Unit tests:**
- `CheckBudget`: mock usage query; test all three outcomes (below soft, between soft/hard, above hard)
- Warn-once logic: two calls in same period with usage above soft limit → only one inbox item created
- Period start computation: verify `daily`/`weekly`/`monthly` start timestamps for known inputs (use injectable clock)
- Anomaly detection: usage = 3.1× average → anomaly; usage = 2.9× average → no anomaly

**Integration tests:**
- `TokenBudgetRepo.Create` + unique constraint: create two budgets for same `(org, project=null, period='daily')` → error
- `CheckBudget` against real PostgreSQL with seeded `model_invocation` rows: verify sum computation
- Soft limit warn flag: run `CheckBudget` twice above soft limit; verify single `inbox_item` row

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

> ✅ ISSUE #23 (RESOLVED): Budget enforcement is hierarchical/additive. `CheckBudget` accepts an `agentID *uuid.UUID` parameter and checks all three levels (org → project → agent) in sequence. Per-agent usage is computed as `SUM(input_tokens + output_tokens) FROM model_invocation WHERE agent_id = $agentID AND created_at >= agent_period_start` (via the injected `UsageQuerier` interface). A single invocation's actual token count is recorded at all three levels simultaneously via `RecordUsage`.

- The `model_invocation` table (task 035/036) is a forward reference. In this task, `RecordUsage` and `CheckBudget` aggregate from `model_invocation` via a dependency-injected `UsageQuerier` interface:
  ```go
  type UsageQuerier interface {
      SumTokens(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, since time.Time) (int64, error)
      DailyRollups(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, days int) ([]DailyRollup, error)
  }
  ```
  A stub `NullUsageQuerier` that always returns 0 is used until task 036 wires in the real implementation.
- The unique constraint for nullable `project_id` requires a partial unique index: one for `(organization_id, period) WHERE project_id IS NULL` and one for `(organization_id, project_id, period) WHERE project_id IS NOT NULL`. Standard unique constraints do not handle nulls correctly in PostgreSQL for this pattern.
- `inbox_item` creation from `BudgetService` introduces a forward dependency on task 027. Use a `NotificationService` interface injected at construction time; the stub logs to stderr. Task 027 wires in the real implementation.
