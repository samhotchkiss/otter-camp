# Issue #126 — Seed Starter Trio (Frank, Lori, Ellie) on Fresh Install

> **Priority:** P0
> **Status:** Ready

## Summary

A fresh Otter Camp install should create three default agents: **Frank** (Chief of Staff), **Lori** (Agent Resources Director), and **Ellie** (Chief Context & Compliance Officer). Currently the onboarding bootstrap creates an org, user, project, and issue — but zero agents. Users start with an empty agent roster.

## Current State

`internal/api/onboarding.go` → `bootstrapOnboarding()` creates:
- ✅ Organization
- ✅ User
- ✅ Session token
- ✅ "Getting Started" project
- ✅ "Welcome to Otter Camp" issue
- ❌ No agents

The three Starter Trio profiles already exist in `data/agents/`:
- `data/agents/chief-of-staff/` — Frank
- `data/agents/agent-relations-expert/` — Lori
- `data/agents/process-management-expert/` — Lori (same person, different capability)

And in `www/personas/`:
- `www/personas/chief-of-staff.json`
- `www/personas/agent-relations-expert.json`
- `www/personas/process-management-expert.json`

## Required Changes

### 1. Add agent seeding to `bootstrapOnboarding()`

**File:** `internal/api/onboarding.go`

After creating the project and issue, seed three agents into the `agents` table:

```go
var starterAgents = []struct {
    Slug        string
    DisplayName string
    Status      string
}{
    {Slug: "frank", DisplayName: "Frank", Status: "active"},
    {Slug: "lori", DisplayName: "Lori", Status: "active"},
    {Slug: "ellie", DisplayName: "Ellie", Status: "active"},
}
```

Add a new function `ensureOnboardingAgents(ctx, tx, orgID)` that inserts these three agents (idempotent — `ON CONFLICT DO NOTHING` on `(org_id, slug)`).

Call it in `bootstrapOnboarding()` after `ensureOnboardingIssue()`:

```go
if err := ensureOnboardingAgents(ctx, tx, orgID); err != nil {
    return OnboardingBootstrapResponse{}, err
}
```

### 2. Include agents in the response

Add to `OnboardingBootstrapResponse`:

```go
Agents []OnboardingAgent `json:"agents"`
```

Where:
```go
type OnboardingAgent struct {
    ID          string `json:"id"`
    Slug        string `json:"slug"`
    DisplayName string `json:"display_name"`
}
```

### 3. Implementation for `ensureOnboardingAgents`

```go
func ensureOnboardingAgents(ctx context.Context, tx *sql.Tx, orgID string) ([]OnboardingAgent, error) {
    agents := make([]OnboardingAgent, 0, len(starterAgents))
    for _, sa := range starterAgents {
        var id string
        err := tx.QueryRowContext(
            ctx,
            `INSERT INTO agents (org_id, slug, display_name, status)
             VALUES ($1, $2, $3, $4)
             ON CONFLICT (org_id, slug) DO UPDATE SET display_name = EXCLUDED.display_name
             RETURNING id`,
            orgID,
            sa.Slug,
            sa.DisplayName,
            sa.Status,
        ).Scan(&id)
        if err != nil {
            return nil, err
        }
        agents = append(agents, OnboardingAgent{
            ID:          id,
            Slug:        sa.Slug,
            DisplayName: sa.DisplayName,
        })
    }
    return agents, nil
}
```

### 4. Update `otter init` CLI output

**File:** `cmd/otter/init.go`

After onboarding completes, print the seeded agents:

```
✅ Created organization: My Org
✅ Created agents: Frank, Lori, Ellie
✅ Created project: Getting Started
```

### 5. Update onboarding frontend (if applicable)

**File:** `web/src/pages/` (onboarding flow)

If there's a setup wizard in the frontend, it should show the three agents after onboarding completes. The agents will already exist in the DB — the frontend just needs to display them.

## Constraints

- **Idempotent**: Running onboarding twice should not create duplicate agents. Use `ON CONFLICT`.
- **Slug-based**: The slugs `frank`, `lori`, `ellie` are the canonical identifiers. Display names can change later.
- **No profile content yet**: This issue only creates the agent records in the DB. Loading their full SOUL.md/IDENTITY.md from `data/agents/` is a separate concern (relates to #125 Three + Temps architecture). For now, just the rows in the `agents` table.
- **Existing installs unaffected**: This only runs during `bootstrapOnboarding()`, which is gated by `onboardingSetupLocked()`. Already-bootstrapped instances won't be modified.

## Tests

- `internal/api/onboarding_test.go`: Bootstrap creates 3 agents with correct slugs and display names
- `internal/api/onboarding_test.go`: Bootstrap is idempotent — second call doesn't duplicate agents
- `internal/api/onboarding_test.go`: Response includes agent IDs, slugs, display names
- `cmd/otter/init_test.go`: CLI output includes agent creation confirmation

## Acceptance Criteria

- [ ] `POST /api/onboarding/bootstrap` creates Frank, Lori, and Ellie agents
- [ ] Response JSON includes `agents` array with all three
- [ ] `otter init` prints agent creation confirmation
- [ ] Second bootstrap call doesn't duplicate agents
- [ ] Agents appear in the agents list (`GET /api/agents`) after onboarding

## Execution Log

- [2026-02-12 12:09 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 126 from 01-ready to 02-in-progress as next priority actionable item | Tests: n/a
- [2026-02-12 12:09 MST] Issue #n/a | Commit n/a | in_progress | Created/switch to dedicated branch codex/spec-126-seed-starter-trio from origin/main for spec isolation | Tests: n/a
- [2026-02-12 12:11 MST] Issue #813 | Commit n/a | opened | Planned API onboarding seeding + response contract micro-issue with explicit tests before coding | Tests: n/a
- [2026-02-12 12:11 MST] Issue #811 | Commit n/a | opened | Planned ottercli onboarding response parsing micro-issue with explicit tests before coding | Tests: n/a
- [2026-02-12 12:11 MST] Issue #812 | Commit n/a | opened | Planned otter init starter-trio confirmation output micro-issue with explicit tests before coding | Tests: n/a
- [2026-02-12 12:11 MST] Issue #813 | Commit f4fcc2f | closed | Seeded starter trio agents during onboarding bootstrap and returned agents in API response with onboarding coverage for seeding/idempotence/list visibility | Tests: go test ./internal/api -run 'TestOnboardingBootstrapCreatesLocalRecords|TestOnboardingBootstrapSeedsStarterTrioAgents|TestBootstrapOnboardingIsIdempotentForStarterAgents' -count=1; go test ./internal/api -count=1
- [2026-02-12 12:12 MST] Issue #811 | Commit a332a31 | closed | Extended ottercli onboarding response decoding to include starter-agent payload and added parsing tests | Tests: go test ./internal/ottercli -run 'TestClientOnboardingBootstrapUsesExpectedPathAndPayload|TestClientOnboardingBootstrapParsesStarterAgents' -count=1; go test ./internal/ottercli -count=1
- [2026-02-12 12:13 MST] Issue #812 | Commit deee860 | closed | Added otter init seeded-agent confirmation output and CLI tests; aligned existing Ellie wording assertion in init suite | Tests: go test ./cmd/otter -run 'TestInitBootstrapLocalSuccessPersistsConfig|TestInitBootstrapLocalPrintsSeededStarterAgents' -count=1; go test ./cmd/otter -count=1; go test ./internal/api ./internal/ottercli ./cmd/otter -count=1
- [2026-02-12 12:13 MST] Issue #n/a | Commit n/a | in_progress | Opened PR #816 for reviewer visibility on branch codex/spec-126-seed-starter-trio | Tests: n/a
- [2026-02-12 12:13 MST] Issue #n/a | Commit n/a | needs_review | Moved spec 126 from 02-in-progress to 03-needs-review after completing and pushing issues #813/#811/#812 | Tests: n/a
