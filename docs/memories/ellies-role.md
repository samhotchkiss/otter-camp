# Memories: Ellie's Role

> Summary: Ellie is one of three permanent OtterCamp agents. She owns memory extraction, retrieval, context injection, and compliance.
> Last updated: 2026-02-16
> Audience: Agents integrating with Ellie behavior.

## Permanent Agent

Ellie is one of three agents that exist in every OtterCamp install (alongside Frank and Lori). She is always running and cannot be retired.

## Responsibilities

Ellie owns:
- **Extracting durable memories from conversations** — the Layer 1 ingestion pipeline
- **Retrieving context** before/while conversations run — serving relevant memories to active agents
- **Injecting proactive context** when relevance thresholds are met — pushing context without being asked
- **Recording retrieval quality signals** — feedback loop for tuning
- **Supporting compliance-review memory capture** — sensitivity and compliance workflows

## What Ellie Does NOT Own

- **Issue/project orchestration** — Frank + core issue APIs handle this
- **Agent lifecycle** — Lori handles hiring/firing/staffing
- **GitHub sync** — separate integration paths
- **Authoring project docs (Layer 3)** — agents and humans write docs; Ellie reads them

## Where Implemented

- Ingestion: `internal/memory/ellie_ingestion_worker.go`
- Retrieval: `internal/memory/ellie_retrieval_cascade.go`
- Injection: `internal/memory/ellie_context_injection_worker.go`
- Compliance handoff: `internal/api/issue_review_*`, `internal/api/compliance_rules.go`

## Design Constraints from Experiments

These proven findings should drive Ellie's implementation:

1. **Use 1536d OpenAI embeddings** — never 768d truncation (60% → 80% hit rate)
2. **Run entity synthesis periodically** — biggest single retrieval improvement (80% → 95%)
3. **Include file-backed memories** for current-state and preference queries (+36pp, +33pp)
4. **Don't weight by importance field** — hurts retrieval (-4pp)
5. **Don't add BM25 hybrid search** — regresses results (80% → 75%)
6. **Build a query router** for category-dependent strategy selection

See `docs/memories/overview.md` for the full findings summary.

## Change Log

- 2026-02-16: Expanded with permanent agent context, Layer 3 boundary, and experimental design constraints.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
