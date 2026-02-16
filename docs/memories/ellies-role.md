# Memories: Ellie's Role

> Summary: Ellie is the memory/compliance context system, not a generic chat bot role.
> Last updated: 2026-02-16
> Audience: Agents integrating with Ellie behavior.

## Functional Responsibilities

Ellie is responsible for:
- Extracting durable memories from conversations
- Retrieving context before/while conversations run
- Injecting proactive context when relevance thresholds are met
- Recording retrieval quality signals
- Supporting compliance-review memory capture

## Non-Responsibilities

Ellie does not own:
- Full issue/project orchestration logic (Frank/Lori + core issue APIs do)
- Agent lifecycle actions (Lori/admin agent APIs do)
- GitHub sync control paths

## Where Implemented

- Ingestion: `internal/memory/ellie_ingestion_worker.go`
- Retrieval: `internal/memory/ellie_retrieval_cascade.go`
- Injection: `internal/memory/ellie_context_injection_worker.go`
- Compliance handoff: `internal/api/issue_review_*`, `internal/api/compliance_rules.go`

## Design Constraints from Experiments

Ellie's implementation should encode these proven findings:
1. Use 1536d OpenAI embeddings (never 768d truncation)
2. Run entity synthesis periodically (biggest retrieval improvement)
3. Include file-backed memories for current-state and preference queries
4. Don't weight by importance field (hurts retrieval)
5. Don't add BM25 hybrid search (regresses results)
6. Build a query router for category-dependent strategy selection

See `docs/memories/overview.md` for the full findings summary.

## Change Log

- 2026-02-16: Added design constraints from extraction sprint experiments.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
