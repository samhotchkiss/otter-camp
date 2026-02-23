# 059: Browser Tool Execution

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 11 §BrowserSession, doc 11 §BrowserActions, doc 11 §BrowserHandoff, doc 11 §BrowserSecurity, doc 16 §RunPause |
| Spec status | finished |
| Depends on | 052, 053, 055, 004, 027, 043 |
| Blocks | 078, 087 |

## Scope

Build the browser tool execution subsystem: `browser_session`, `browser_action`, and
`browser_handoff` DDL; the `BrowserExecutor` that dispatches browser actions via a headless
Chrome process; automatic screenshots after every navigation/interaction/error into
`run_artifact`; the human handoff flow (inbox item creation, run pause, resume); domain
policy enforcement; credential injection; and idle timeout handling.

### Must build

**Migrations:**
- `0066_browser_session.sql`
- `0067_browser_action.sql`
- `0068_browser_handoff.sql`

**`browser_session` table** (doc 11):
- `id uuid primary key default gen_random_uuid()`
- `task_id uuid not null references project_task(id) on delete cascade` — task-scoped, not run-scoped
- `project_id uuid not null references project(id) on delete cascade`
- `agent_id uuid not null references agent(id) on delete cascade`
- `organization_id uuid not null references organization(id) on delete cascade`
- `status text not null check (status in ('active','idle','closed','revoked'))` default `'active'`
- `browser_type text not null default 'chromium'` — headless browser type
- `current_url text` — last known URL
- `user_agent text`
- `credentials_injected jsonb not null default '[]'` — list of credential slugs injected (keys only, never values)
- `idle_since timestamptz` — set when last action completed; cleared on new action
- `revoked_at timestamptz` — populated on revocation
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Rule: at most one active `browser_session` per `task_id` (partial unique index `WHERE status='active'`)
- Index: `(task_id, status)`, `(agent_id)`, `(organization_id, created_at)`

**`browser_action` table** (doc 11):
- `id uuid primary key default gen_random_uuid()`
- `browser_session_id uuid not null references browser_session(id) on delete cascade`
- `run_id uuid not null references run(id) on delete cascade`
- `run_step_id uuid not null references run_step(id) on delete cascade`
- `action_type text not null check (action_type in ('navigate','click','type','select','hover','scroll','press_key','screenshot','extract_text','extract_structured','get_page_info','wait_for','wait_for_navigation','back','forward','refresh'))` — 15 action types from doc 11; see ISSUE #26 note below
- `input jsonb not null default '{}'` — action-specific parameters
- `output jsonb` — action result (page info, extracted text, etc.)
- `screenshot_artifact_id uuid references run_artifact(id) on delete set null` — automatic screenshot taken after every nav/interaction/error
- `status text not null check (status in ('pending','in_progress','completed','failed','blocked_by_domain_policy'))` default `'pending'`
- `error_message text`
- `url_at_execution text` — URL when the action was executed
- `started_at timestamptz`
- `completed_at timestamptz`
- `duration_ms integer`
- `created_at timestamptz not null default now()`
- Index: `(browser_session_id, created_at)`, `(run_id)`, `(run_step_id)`

**`browser_handoff` table** (doc 11):
- `id uuid primary key default gen_random_uuid()`
- `browser_session_id uuid not null references browser_session(id) on delete cascade`
- `inbox_item_id uuid not null references inbox_item(id) on delete cascade`
- `run_id uuid not null references run(id) on delete cascade`
- `agent_id uuid not null references agent(id) on delete cascade`
- `target_user_id uuid not null references human_user(id) on delete cascade`
- `reason text not null` — why handoff was requested (agent-provided explanation)
- `pre_handoff_screenshot_artifact_id uuid references run_artifact(id) on delete set null` — screenshot taken just before pausing
- `post_handoff_screenshot_artifact_id uuid references run_artifact(id) on delete set null` — screenshot taken after human completes handoff
- `status text not null check (status in ('pending','completed','expired','cancelled'))` default `'pending'`
- `expires_at timestamptz not null` — default `now() + interval '24 hours'`
- `completed_at timestamptz`
- `created_at timestamptz not null default now()`
- Index: `(run_id)`, `(inbox_item_id)`, `(browser_session_id)`

**Repositories:**
- `BrowserSessionRepository` — Create, Get, GetActiveByTask, UpdateStatus, UpdateCurrentURL, UpdateIdleSince, ListByAgent
- `BrowserActionRepository` — Create, Get, UpdateCompletion, ListBySession, ListByRun
- `BrowserHandoffRepository` — Create, Get, GetByInboxItem, UpdateStatus, ListBySession

**`BrowserExecutor`** (in `internal/browser/executor.go`):

**Session management:**
- `BrowserExecutor.GetOrCreateSession(ctx, taskID, agentID, projectID) (BrowserSession, error)`:
  - Check for existing active session (`BrowserSessionRepository.GetActiveByTask`).
  - If found: return existing session (sessions persist across runs for the same task).
  - If not found: launch headless Chrome subprocess via `chromedp` (or Playwright MCP wrapper — implementation choice; use an interface `BrowserDriver` for testability); create `browser_session` row.
  - Credential injection: call `SecretService.ResolveForBrowserSession(ctx, agentID)` to load any browser-credential bindings; inject into the browser context at session creation. Store only the slugs (not values) in `credentials_injected`.
  - Idle timeout (1 hour): start a goroutine that monitors `idle_since`; if `now() - idle_since > 1 hour` → call `CloseSession`.

**Action dispatch:**
- `BrowserExecutor.ExecuteAction(ctx, sessionID, runID, runStepID, action BrowserActionInput) (BrowserActionOutput, error)`:
  1. Load `browser_session`; verify `status='active'`.
  2. Domain policy check (see below): if denied → create `browser_action` with `status='blocked_by_domain_policy'`; return `ErrDomainPolicyDenied`.
  3. Create `browser_action` row with `status='in_progress'`.
  4. Execute the action via `BrowserDriver.Execute(action)`.
  5. Take automatic screenshot after every: navigate, click, type, select, hover, scroll, press_key, wait_for_navigation, back, forward, refresh, and on any error. Store as `run_artifact(artifact_type='screenshot', content_type='image/png', inline_content=null)`. Set `browser_action.screenshot_artifact_id`.
  6. `screenshot` action: explicit screenshot (also stored as `run_artifact`).
  7. Update `browser_action` with `status='completed'|'failed'`, `output`, `duration_ms`, `url_at_execution`.
  8. Update `browser_session.current_url` and `idle_since = now()`.
  9. Return output.

**Session close and revocation:**
- `BrowserExecutor.CloseSession(ctx, sessionID) error`: terminate browser process; set `browser_session.status='closed'`.
- `BrowserExecutor.RevokeSession(ctx, sessionID) error`: set `browser_session.status='revoked'`, `revoked_at=now()`. In-flight action (if any) finishes; next `ExecuteAction` call returns `ErrSessionRevoked`.

**Domain policy enforcement:**
- `DomainPolicyChecker.IsAllowed(ctx, orgID, url string) bool`:
  - Parse URL; extract eTLD+1 domain.
  - Sensitive domains denied by default (hardcoded denylist):
    ```
    *.banking.* (heuristic: any domain with "bank" in name)
    accounts.google.com
    login.microsoftonline.com
    *.auth0.com
    *.okta.com
    signin.aws.amazon.com
    console.aws.amazon.com
    *.stripe.com
    *.paypal.com
    *.braintreegateway.com
    *.checkout.com
    *.klarna.com
    ```
  - Organization-level policy can add to the denylist OR explicitly allowlist domains (stored in `organization.settings.browser_policy` — jsonb field, design choice).
  - No credential sites, no payment pages (doc 11 rule).
  - Returns `false` (denied) if domain matches any denylist entry.

> ⚠️ ISSUE #26 (BLOCKER): Doc 20 defines `browser.evaluate` (JavaScript execution in page context). Doc 11 does NOT include it in the browser action model. The `action_type` CHECK constraint above reflects doc 11's 15 actions only and omits `browser.evaluate`. Do not add `evaluate` to the action_type enum until Sam resolves this contradiction. The `BrowserExecutor` will return `ErrUnknownActionType` for any `evaluate` action dispatched via the broker.

**Human handoff flow:**
- `BrowserExecutor.RequestHandoff(ctx, sessionID, runID, agentID, targetUserID, reason) (BrowserHandoff, error)`:
  1. Take screenshot → store as `run_artifact`; save as `pre_handoff_screenshot_artifact_id`.
  2. Create `inbox_item(item_type='browser_handoff', target_user_id=targetUserID, source_task_id, action_payload={browser_session_id, browser_handoff_id, pre_screenshot_url})` via `InboxService.CreateItem` (task 028).
  3. Create `browser_handoff` row linking session + inbox_item + run.
  4. Call `RunService.PauseRun(ctx, runID)` — run transitions to `paused`.
  5. Return `BrowserHandoff`.

- `BrowserExecutor.CompleteHandoff(ctx, handoffID, actionDecision) error`:
  - Called when the human acts on the `browser_handoff` inbox_item (via task 032 `POST /v1/inbox/:id/act`).
  - `actionDecision`: `completed` (human finished browser task) or `cancelled` (human cancelled).
  - Take screenshot → `post_handoff_screenshot_artifact_id`.
  - Update `browser_handoff.status = 'completed'|'cancelled'`, `completed_at = now()`.
  - Call `InboxService.MarkActioned(ctx, inboxItemID)`.
  - If `completed`: call `RunService.ResumeRun(ctx, runID)` — run transitions `paused → in_progress`.
  - If `cancelled`: call `RunService.FailRun(ctx, runID, "handoff_cancelled", "permanent")`.

- **Handoff expiry:**
  - `SupervisorBrowserHandoff` (part of the Supervisor in task 053) checks for `browser_handoff` rows where `status='pending' AND expires_at < now()`.
  - On expiry: set `status='expired'`; fail the associated run; create escalation inbox item.

**Idle timeout goroutine:**
- Each `browser_session` has an associated idle monitor goroutine.
- Checks `browser_session.idle_since` every 5 minutes.
- If `now() - idle_since > 1 hour`: calls `CloseSession(sessionID)`.
- Goroutine exits when session is closed/revoked or application shuts down.

**Browser tool definition entries:**
- `0069_tool_definition_browser_seed.sql` — insert rows for 15 browser tools (`browser.navigate`, `browser.click`, etc.) with:
  - `tool_tier='tier2'`
  - `tool_domain='browser'`
  - `capability='browser.navigate'` (or equivalent per action)

### Must NOT build

- CLI execution (task 058)
- Policy capability check (task 055 broker)
- `inbox_item` table DDL (task 027)
- Run service pause/resume (task 053) — this task calls those methods
- SSE streaming (task 047)
- Headless browser CI setup (task 078/089)

## Acceptance Criteria

- [ ] `browser_session` partial unique index prevents two `status='active'` rows for the same `task_id`
- [ ] `BrowserExecutor.ExecuteAction` for a `navigate` action to a payment domain (`*.stripe.com`) returns `ErrDomainPolicyDenied` and creates a `browser_action` row with `status='blocked_by_domain_policy'`
- [ ] Every `navigate`, `click`, and `type` action results in a `run_artifact(artifact_type='screenshot')` row with `inline_content=null`
- [ ] `BrowserExecutor.RequestHandoff` creates an `inbox_item` with `item_type='browser_handoff'` and transitions the run to `paused`
- [ ] `BrowserExecutor.CompleteHandoff` with `completed` transitions the run from `paused` to `in_progress`
- [ ] `BrowserExecutor.CompleteHandoff` with `cancelled` calls `RunService.FailRun`
- [ ] Idle session (mock `idle_since = now() - 90 minutes`) is closed by the idle monitor
- [ ] `BrowserExecutor.ExecuteAction` on a `revoked` session returns `ErrSessionRevoked` immediately

## Tests Required

**Unit tests:**
- `DomainPolicyChecker.IsAllowed`: `accounts.google.com` → false; `en.wikipedia.org` → true; org denylist addition → custom domain denied
- `RequestHandoff`: mock `InboxService.CreateItem`, `RunService.PauseRun` called in order; `browser_handoff` row created
- `CompleteHandoff(completed)`: mock `RunService.ResumeRun` called; `browser_handoff.status='completed'`
- `CompleteHandoff(cancelled)`: mock `RunService.FailRun` called; `browser_handoff.status='cancelled'`
- Revoked session guard: `ExecuteAction` after `RevokeSession` → `ErrSessionRevoked` without executing any action
- Screenshot always taken: mock driver; `navigate` action → mock `TakeScreenshot` called once; verify `screenshot_artifact_id` set on `browser_action`
- Session uniqueness: two `GetOrCreateSession` calls for same `task_id` → second call returns same session ID

**Integration tests:**
- Partial unique index: insert two `browser_session` rows with same `task_id, status='active'` → second insert fails with constraint violation
- Handoff round-trip: create browser_session + browser_handoff; simulate handoff completion via inbox act; verify run status changes, `browser_handoff.status='completed'`
- Domain policy block: `ExecuteAction` with `navigate` to denied domain → `browser_action.status='blocked_by_domain_policy'` in DB

**E2E tests:**
- None — covered by dedicated E2E task 078 (with headless Chrome)

## Implementer Notes

**Browser driver interface:**
Define `BrowserDriver interface { Execute(ctx, action) (output, error); TakeScreenshot(ctx) ([]byte, error); Close() }` in `internal/browser/driver.go`. Implement a `ChromedpDriver` (using `github.com/chromedp/chromedp`). In tests, use a `MockBrowserDriver` that returns fixture data without launching a real browser.

**Credential injection:**
Credentials for browser sessions are loaded from the agent's MCP secret bindings or explicitly configured session credentials. The exact configuration schema for "browser credentials" is not fully specified — implement as: check `agent.settings.browser_credentials` (jsonb, list of `{domain_pattern, secret_slug}` pairs). Inject using the browser's `SetCookies` or `SetExtraHTTPHeaders` depending on credential type. If `agent.settings` does not have `browser_credentials`, no injection occurs.

**Screenshot storage:**
Screenshots are `image/png` and are ALWAYS stored in object storage (never inline). `run_artifact.inline_content` is always null for screenshots. The `download_url` (presigned link from task 054) is the access mechanism.

> ⚠️ ISSUE #26 (BLOCKER): `browser.evaluate` (JavaScript execution) is defined in doc 20 but absent from doc 11's browser action model. The `action_type` CHECK constraint does NOT include `evaluate`. Do not implement or enable `browser.evaluate` until Sam resolves the contradiction between doc 20 and doc 11.
