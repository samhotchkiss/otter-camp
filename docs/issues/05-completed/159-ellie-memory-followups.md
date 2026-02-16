# 159 - Ellie Memory Follow-Ups: Semantic Retrieval and LLM Ingestion

> **Priority:** P1
> **Status:** Needs Review
> **Depends on:** #151 (Ellie memory infrastructure)

## Summary

Spec 151 shipped with heuristic retrieval/extraction scaffolding and explicitly tracked two follow-up gaps:

- Semantic/vector retrieval for Tier 2 (memories) and Tier 3 (chat history)
- LLM-powered ingestion extraction with deterministic fallback

This spec closes those gaps with test-first implementation and small issue slices.

## Scope

In:
- Blend semantic and keyword ranking for Ellie memory/chat retrieval paths
- Generate query embeddings during retrieval when embedder is configured
- Add LLM extraction pass for ingestion windows with structured outputs
- Preserve deterministic heuristic fallback when LLM extraction is unavailable or fails
- Add regression coverage for semantic recall and LLM/fallback behavior

Out:
- Full reranker/ML pipeline
- Compliance gating changes

## Planned Work Units

1. Semantic retrieval ranking in store query paths
2. Retrieval cascade query-embedding integration
3. LLM ingestion extraction path + metadata
4. Deterministic fallback behavior + failure handling

## Execution Log

- [2026-02-13 09:27 MST] Issue #159 | Commit n/a | created | Created local follow-up spec for unresolved Spec 151 semantic retrieval + LLM ingestion work; queued in 01-ready for execution | Tests: n/a
- [2026-02-13 09:27 MST] Issue #159 | Commit n/a | moved_to_in_progress | Moved spec 159 from 01-ready to 02-in-progress to begin execution loop and pre-code issue breakdown | Tests: n/a
- [2026-02-13 09:28 MST] Issue #941/#942/#943/#944 | Commit n/a | created | Created full Spec 159 micro-issue set with explicit tests before implementation (semantic store, retrieval embedder integration, LLM extraction, deterministic fallback) | Tests: n/a
- [2026-02-13 09:32 MST] Issue #941 | Commit 20e91238 | closed | Added semantic+keyword blended retrieval ranking in Ellie store with non-literal semantic regression coverage for memory/chat retrieval | Tests: go test ./internal/store -run 'TestEllieRetrievalStoreSemanticQueryFindsNonLiteralMatches|TestEllieRetrievalStoreSemanticQueryBlendsKeywordAndVectorRanking|TestEllieRetrievalStoreTreatsWildcardQueryCharsAsLiterals|TestEllieRetrievalStoreProjectAndOrgScopes|TestEllieRetrievalStoreKeywordScaffoldBehavior' -count=1; go test ./... -count=1 (known unrelated failures in embedder/migration schema tests)
- [2026-02-13 09:33 MST] Issue #942 | Commit ede4013f | closed | Wired retrieval-cascade query embedding path with semantic store calls for tier 2/3 and graceful keyword fallback when embedding is unavailable | Tests: go test ./internal/memory -run 'TestEllieRetrievalCascadeUsesSemanticStoreResults|TestEllieRetrievalCascadeFallsBackWhenQueryEmbeddingUnavailable|TestEllieRetrievalCascadeTierTwoNeverReturnsOverLimitAfterDedupe' -count=1; go test ./internal/memory -count=1 (known unrelated embedder test failures)
- [2026-02-13 09:35 MST] Issue #943 | Commit edd00426 | closed | Added LLM-powered Ellie ingestion extraction path with normalized candidate mapping and traceable metadata (method/model/trace/source ids) | Tests: go test ./internal/memory -run 'TestEllieIngestionWorkerUsesLLMExtractionWhenConfigured|TestEllieIngestionWorkerLLMExtractionMetadataIncludesTraceability|TestEllieIngestionWorkerGroupsMessagesWithinTimeWindow' -count=1; go test ./internal/memory -count=1 (known unrelated embedder test failures)
- [2026-02-13 09:37 MST] Issue #944 | Commit 91ca46f2 | closed | Added deterministic fallback to heuristic extraction when LLM extractor is unavailable/errors/returns no candidates, with fallback regressions and cursor-path validation | Tests: go test ./internal/memory -run 'TestEllieIngestionWorkerFallsBackToHeuristicsWhenLLMUnavailable|TestEllieIngestionWorkerFallsBackToHeuristicsWhenLLMExtractorErrors|TestEllieIngestionWorkerFallsBackToHeuristicsWhenLLMReturnsNoCandidates|TestEllieIngestionWorkerAdvancesCursorAfterSuccessfulWrite|TestEllieIngestionWorkerBackfillModeResumesNormalCursoring' -count=1; go test ./internal/memory -count=1 (known unrelated embedder test failures)
- [2026-02-13 09:37 MST] Issue #850/#851 | Commit 20e91238/ede4013f/edd00426/91ca46f2 | closed | Closed parent follow-up issues after completing and closing micro-issues #941-#944 covering semantic retrieval, retrieval query embedding integration, LLM ingestion extraction, and deterministic fallback | Tests: see issue-level entries above
- [2026-02-13 09:37 MST] Issue #159 | Commit 91ca46f2 | moved_to_needs_review | Completed all planned micro-issues (#941-#944), pushed branch updates, and moved spec to 03-needs-review pending external validation | Tests: go test ./internal/store -run 'TestEllieRetrievalStoreSemanticQueryFindsNonLiteralMatches|TestEllieRetrievalStoreSemanticQueryBlendsKeywordAndVectorRanking|TestEllieRetrievalStoreTreatsWildcardQueryCharsAsLiterals|TestEllieRetrievalStoreProjectAndOrgScopes|TestEllieRetrievalStoreKeywordScaffoldBehavior' -count=1; go test ./internal/memory -run 'TestEllieRetrievalCascadeUsesSemanticStoreResults|TestEllieRetrievalCascadeFallsBackWhenQueryEmbeddingUnavailable|TestEllieRetrievalCascadeTierTwoNeverReturnsOverLimitAfterDedupe|TestEllieIngestionWorkerUsesLLMExtractionWhenConfigured|TestEllieIngestionWorkerLLMExtractionMetadataIncludesTraceability|TestEllieIngestionWorkerFallsBackToHeuristicsWhenLLMUnavailable|TestEllieIngestionWorkerFallsBackToHeuristicsWhenLLMExtractorErrors|TestEllieIngestionWorkerFallsBackToHeuristicsWhenLLMReturnsNoCandidates|TestEllieIngestionWorkerAdvancesCursorAfterSuccessfulWrite|TestEllieIngestionWorkerBackfillModeResumesNormalCursoring' -count=1
- [2026-02-13 09:37 MST] Issue #159 | Commit 91ca46f2 | pr_opened | Opened reviewer visibility PR #945 from codex/spec-159-ellie-memory-followups into main after completing implementation and queue transition to 03-needs-review | Tests: see issue-level entries above
