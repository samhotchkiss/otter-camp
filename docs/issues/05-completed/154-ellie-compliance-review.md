# Issue #154 — Ellie: Compliance Review

> **Priority:** P1
> **Status:** Ready
> **Depends on:** #150 (Conversation Schema Redesign), #151 (Ellie Memory Infrastructure)
> **Author:** Josh S / Sam

## Summary

Ellie reviews all completed work for compliance — checking that agents followed org-wide rules, project-specific scope, and issue-level acceptance criteria. Rules are managed conversationally (talk to an agent, not edit a file) and stored in the database.

## Three Layers of Rules

### Layer 1: Org-Wide Rules

Apply to every project, every agent, every issue. Examples:

- Commit message conventions
- Code review required before merge
- No secrets in code
- Documentation must be updated
- Small incremental commits, not big batches
- "No Prisma" (global technical preference)

### Layer 2: Project-Specific Rules

Apply to a single project. Examples:

- "This project uses Next.js + Drizzle + Fly"
- "Tone is casual, not corporate"
- "Auth module is frozen — don't touch it"
- "All API endpoints require tests"
- "Max 500 lines per PR"

### Layer 3: Issue-Level Acceptance Criteria

Already exists — issue specs define what "done" means. Ellie checks the output matches the spec.

## Rules Storage

Rules live in the database, not in files. Users create and modify rules by talking to an agent.

### Schema

```sql
CREATE TABLE compliance_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,  -- NULL = org-wide
    title TEXT NOT NULL,
    description TEXT NOT NULL,          -- human-readable rule description
    check_instruction TEXT NOT NULL,    -- LLM-readable instruction for Ellie's review
    category TEXT NOT NULL CHECK (category IN (
        'code_quality',     -- linting, tests, coverage, commit conventions
        'security',         -- secrets, auth, exposure
        'scope',            -- staying within spec, no unauthorized changes
        'style',            -- tone, voice, formatting
        'process',          -- review gates, approval requirements
        'technical'         -- stack choices, architecture constraints
    )),
    severity TEXT NOT NULL DEFAULT 'required' CHECK (severity IN (
        'required',         -- must pass, blocks completion
        'recommended',      -- should pass, flagged but doesn't block
        'informational'     -- noted for awareness, never blocks
    )),
    enabled BOOLEAN NOT NULL DEFAULT true,
    source_conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX compliance_rules_org_idx
    ON compliance_rules (org_id, enabled) WHERE enabled = true;
CREATE INDEX compliance_rules_project_idx
    ON compliance_rules (project_id, enabled) WHERE project_id IS NOT NULL AND enabled = true;
```

**Key design:**
- `project_id IS NULL` = org-wide rule
- `project_id IS NOT NULL` = project-specific rule
- `check_instruction` is what Ellie actually uses during review — optimized for LLM comprehension
- `source_conversation_id` = provenance back to the conversation where the rule was created
- Rules can be disabled without deletion (audit trail)

### Conversational Rule Management

Users create rules by talking to any agent:

```
Sam: "From now on, all PRs need tests"
Frank: "Got it. I'll add that as an org-wide compliance rule."
  → Frank calls Ellie (or Otter Camp API directly)
  → Creates compliance_rule:
      title: "All PRs require tests"
      description: "Every pull request must include test coverage for new functionality"
      check_instruction: "Verify that the PR includes new or updated test files covering the changes. If new API endpoints, routes, or store methods were added, corresponding test functions must exist."
      category: "code_quality"
      severity: "required"
      project_id: NULL (org-wide)
      source_conversation_id: <this conversation>
```

Modifying rules works the same way:

```
Sam: "Actually, the test requirement only applies to backend code, not frontend"
Frank: "Updated. Tests are now required for backend PRs only."
  → Updates check_instruction to scope to backend
```

Disabling:

```
Sam: "Suspend the test requirement for the hackathon project"
Frank: "Done. Test requirement disabled for Project X."
  → Sets enabled=false for that project-specific override, or creates a project-level rule that overrides
```

## The Review Flow

```
Temp closes an issue
  → Ellie is notified (issue.closed event)
  → Ellie collects applicable rules:
      1. All org-wide rules (project_id IS NULL, enabled = true)
      2. Project-specific rules for this issue's project
      3. Issue acceptance criteria from the spec
  → Ellie reviews the work against each rule:
      - Reads the diff/commits
      - Reads the issue spec
      - Applies each check_instruction
  → Generates a compliance report
  → If all required rules pass:
      → Issue stays closed, report attached
  → If any required rule fails:
      → Issue reopened with specific feedback
      → Lori notified for reassignment or escalation
  → Recommended/informational findings noted but don't block
```

### Compliance Report

Attached to the issue after review:

```
## Compliance Review — Issue #847

### ✅ Passed
- [code_quality] Commit message conventions — all 3 commits follow format
- [security] No secrets in code — no API keys, passwords, or tokens found
- [process] Code review required — PR has 1 approval

### ❌ Failed (required)
- [code_quality] All PRs require tests — new endpoint POST /api/rooms has no test coverage
  → Action: Add test for room creation endpoint

### ⚠️ Flagged (recommended)
- [style] Documentation updated — README not updated with new endpoint
  → Suggestion: Add /api/rooms to API docs

### ℹ️ Notes (informational)
- [technical] Uses Postgres jsonb for metadata — consistent with project conventions
```

### Parallel Execution & Model Tiering

Each rule check is independent — fan out, run in parallel, fan in results.

```
15 applicable rules → 15 parallel checks → collect results → generate report
```

Not every check needs a frontier model. Tier by complexity:

| Category | Model | Why |
|----------|-------|-----|
| `security` | Haiku / local | Pattern matching — grep for API keys, passwords |
| `code_quality` | Haiku / local | Structural — test files exist? commit format? |
| `process` | Haiku / local | Metadata — PR has approval? review gate passed? |
| `style` | Sonnet | Needs comprehension — tone, voice, formatting |
| `scope` | Sonnet | Compare work against spec — reasoning required |
| `technical` | Sonnet | Domain understanding — right stack, right patterns |

Configurable via `ellie.compliance_model` (default) and per-category overrides. Most orgs will run 60%+ of checks on Haiku/local, keeping cost and latency low.

### Failure Handling

When a required rule fails:

1. Ellie reopens the issue with the compliance report
2. Specific failures are listed with actionable feedback
3. Lori is notified — she can reassign to the same temp or hire a new one
4. The temp fixes the issues and re-closes
5. Ellie reviews again (only checks previously-failed rules + any new changes)

Re-review is scoped — Ellie doesn't re-run the full suite on every iteration, just the failures plus a sanity check on new changes.

## Rule Inheritance & Override

Rules cascade:

```
Org-wide rules (always apply)
  + Project-specific rules (add to org-wide)
  + Project-specific overrides (can relax org-wide rules for this project)
```

**Override example:** Org-wide says "all PRs need tests." Project X is a hackathon prototype — project-level rule says "test requirement: severity=informational" for this project. Ellie notes it but doesn't block.

Overrides are explicit and tracked — you can see which projects have relaxed which org-wide rules and why (via `source_conversation_id`).

## Context Extraction

During review, Ellie also extracts knowledge (#151 integration):

- Patterns worth remembering ("this agent consistently writes clean commit messages")
- Recurring issues ("third time this week a temp forgot tests — maybe the issue template needs updating")
- New technical decisions made during implementation
- Scope drift patterns ("temps keep modifying the auth module even though it's frozen")

These feed into the `memories` table as `lesson`, `pattern`, or `anti_pattern` kinds.

## Open Questions

1. **Review depth** — How deep does Ellie go on code? She's not doing a full code review (that's a reviewer temp's job via Lori). She's checking structural compliance. Where's the line?
2. **Review latency** — How fast does the review need to happen after issue close? Near-real-time, or is a 5-minute cycle fine?
3. **Rule conflicts** — What happens when two rules contradict? (e.g., "use Postgres" org-wide, "use SQLite" project-specific). Project-specific wins? Flag for human?
4. **Bulk review** — When a project completes with 20 issues, does Ellie review each individually or do a project-level sweep?

## References

- #150 — Conversation Schema Redesign (`conversations` table for rule provenance)
- #151 — Ellie Memory Infrastructure (context extraction during review)
- #152 — Proactive Context Injection (Ellie may proactively remind agents of rules during work)
- #125 — Three + Temps Architecture (Ellie's compliance role definition)
- [2026-02-12 16:17 MST] Issue #154 | Commit n/a | in_progress | Moved spec 154 from `01-ready` to `02-in-progress` and created isolated branch `codex/spec-154-ellie-compliance-review` from `origin/main` | Tests: n/a
- [2026-02-12 16:18 MST] Issue #877 | Commit n/a | created | Created micro-issue for compliance_rules schema migration, RLS, and schema validation tests | Tests: n/a
- [2026-02-12 16:18 MST] Issue #878 | Commit n/a | created | Created micro-issue for compliance rules store CRUD and applicable scope resolution queries | Tests: n/a
- [2026-02-12 16:18 MST] Issue #879 | Commit n/a | created | Created micro-issue for compliance evaluator aggregation/report domain logic | Tests: n/a
- [2026-02-12 16:18 MST] Issue #880 | Commit n/a | created | Created micro-issue for issue-close compliance review orchestration and reopen behavior | Tests: n/a
- [2026-02-12 16:18 MST] Issue #881 | Commit n/a | created | Created micro-issue for conversational compliance rule management API handlers and scope checks | Tests: n/a
- [2026-02-12 16:18 MST] Issue #882 | Commit n/a | created | Created micro-issue for compliance findings to Ellie memory extraction with dedupe | Tests: n/a
- [2026-02-12 16:38 MST] Issue #877 | Commit 9254777 | closed | Added migration 072 (`compliance_rules`) plus schema coverage for table/constraints/index/RLS policy | Tests: `go test ./internal/store -run 'TestMigration072ComplianceRulesFilesExistAndContainCoreDDL|TestSchemaComplianceRulesTableAndConstraints' -count=1`
- [2026-02-12 16:38 MST] Issue #878 | Commit 88f9352 | closed | Shipped compliance rule store create/disable/list scope logic with validation tests | Tests: `go test ./internal/store -run 'TestComplianceRuleStoreCreateAndDisable|TestComplianceRuleStoreListApplicableRules|TestComplianceRuleStoreRejectsInvalidCategoryAndSeverity' -count=1`
- [2026-02-12 16:38 MST] Issue #879 | Commit 53c7017 | closed | Added compliance report aggregation/markdown formatter and blocking classification behavior | Tests: `go test ./internal/memory -run 'TestBuildComplianceReportCategorizesFindings|TestComplianceReportMarkdownIncludesActionableSections' -count=1`
- [2026-02-12 16:38 MST] Issue #880 | Commit 925f527 | closed | Wired issue-close compliance orchestration, report comment attachment, and reopen-on-required-failure flow | Tests: `go test ./internal/api -run 'TestIssueCloseComplianceReviewPassesAndStaysClosed|TestIssueCloseComplianceReviewReopensOnRequiredFailure' -count=1`
- [2026-02-12 16:38 MST] Issue #881 | Commit 3c4c7b7 | closed | Added compliance rule API handlers/routes (list/create/update/disable) and org/project scoping validation | Tests: `go test ./internal/api -run 'TestComplianceRuleHandlersCreateUpdateDisable|TestComplianceRuleHandlersEnforceOrgScope' -count=1`; `go test ./internal/store -run 'TestComplianceRuleStore(UpdateAndGetByID|CreateRejectsCrossOrgProject|CreateAndDisable|ListApplicableRules|RejectsInvalidCategoryAndSeverity)' -count=1`
- [2026-02-12 16:38 MST] Issue #882 | Commit 9bf47eb | closed | Added compliance finding -> memory candidate mapping with deterministic fingerprint dedupe and ingestion wiring | Tests: `go test ./internal/memory -run 'TestComplianceFindingsProduceMemoryCandidates|TestComplianceMemoryExtractionDedupesRepeatedFindings' -count=1`; `go test ./internal/store -run 'TestEllieIngestionStoreHasComplianceFingerprint' -count=1`
- [2026-02-12 16:38 MST] Issue #154 | Commit n/a | in_review_ready | Opened PR #886 for spec 154 branch `codex/spec-154-ellie-compliance-review` | Tests: `go test ./... -count=1`
