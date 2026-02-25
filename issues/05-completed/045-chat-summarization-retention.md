# 045: Chat Progressive Summarization and Retention

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 02 §ProgressiveSummarization, doc 02 §SessionRetention, doc 02 §SummarizationThreshold |
| Spec status | finished |
| Depends on | 043, 044, 035, 036, 024 |
| Blocks | 048, 050, 072, 083 |

## Scope

Build the progressive summarization pipeline and session retention/cleanup lifecycle. This
is service logic that runs as background jobs and is also triggered inline by the prompt
assembly engine (task 050) when the conversation history budget is nearly exhausted.

### Must build

**Summarization threshold detection:**
- `SummarizationChecker.ShouldSummarize(ctx, sessionID, layerBudgetTokens) (bool, error)`
  - Estimates token count of the unsummarized message history for the session.
  - Returns `true` when the history would consume ~50–60% of `layerBudgetTokens` (the layer 6
    token budget passed in from the prompt assembler — task 050).
  - Uses a lightweight token estimator (character count / 4 as a proxy; not a full tokenizer) to
    avoid a model call just for the threshold check.
  - Caches the per-session unsummarized token estimate in memory for 30 seconds to avoid
    redundant DB reads during rapid message appends.

**Summarization runner:**
- `Summarizer.RunForSession(ctx, sessionID) (*ChatSummary, error)`
  - Identifies the oldest ~25–30% of unsummarized turns by sequence_number (those without an
    overlapping `chat_summary` entry).
  - Constructs a summarization prompt from the raw messages in the target range.
  - **Preserves verbatim**: file paths, URLs, artifact IDs, code blocks, and tool call/result
    pairs must appear verbatim in the summary; the LLM is instructed to preserve these exactly.
  - Calls the model gateway (task 035/036) using `invocation_purpose='summarization'` and the
    session's resolved model profile.
  - Writes the resulting `chat_summary` row with `from_sequence` and `to_sequence` set to the
    range covered.
  - Summarization runs in the session's own context (uses the session's model profile assignment);
    NOT in a separate isolated context.
  - Returns `ErrAlreadySummarized` if the target range is already fully covered by existing summaries.

**Job queue integration:**
- `job_type: 'chat_summarize'` — enqueued by the prompt assembler (task 050) when threshold is
  crossed, or by `chat.session.closed` event handler.
  - Payload: `{session_id, layer_budget_tokens}`
  - On pickup, calls `Summarizer.RunForSession`.
  - Idempotent: if a summary for the target range already exists, the job is a no-op.
- `job_type: 'chat_session_cleanup'` — enqueued on session close; runs next-day at 03:00 UTC
  (schedule via job queue delayed execution).
  - Payload: `{session_id, cleanup_type: 'ephemeral_purge'|'tool_result_compaction'|'summary_consolidation'}`

**Session lifecycle cleanup** (executed by `chat_session_cleanup` job):
- `SessionCleaner.RunCleanup(ctx, sessionID, cleanupType) error`
  - **Immediate on close** (triggered synchronously within `ChatService.CloseSession` before returning):
    - Publish `chat.session.closed` event → memory extraction worker picks up asynchronously.
    - This is NOT part of `SessionCleaner` — it is done in task 044's `CloseSession` method.
  - **Deferred ephemeral purge** (cleanup_type='ephemeral_purge', runs next day):
    - Applies only to async sessions with `mode='async'` created for flow_node_executions (identified via `metadata.flow_node_execution_id` present).
    - Deletes `chat_message` rows where `role IN ('tool_call','tool_result')` that are older than 90 days AND the session is closed.
    - Does NOT delete `role='assistant'` or `role='user'` messages; they may be referenced by memory extraction.
  - **Tool result compaction** (cleanup_type='tool_result_compaction'):
    - For closed sessions: replaces verbose tool_result message `content` with a compact summary
      string `"[tool_result compacted — N bytes]"` for tool results larger than 4KB.
    - Only applies to messages where `status='final'` (already finalized).
    - Updates `status` remains `'final'`; sets `metadata.compacted=true`.
  - **Summary consolidation** (cleanup_type='summary_consolidation'):
    - If a session has more than 5 `chat_summary` rows, merge adjacent summaries into a single
      comprehensive summary (run one additional model call with all summary texts concatenated).
    - Replaces the N old summary rows with a single consolidated row covering the same range.
    - Idempotent: if already consolidated to ≤5 summaries, no-op.

**Daily retention enforcement job:**
- `job_type: 'chat_retention_sweep'` — runs daily; enqueued by a cron schedule (task 065 scheduling engine).
  - Payload: `{cutoff_date}` — sessions closed more than 90 days ago (per retention policy, doc 13).
  - Marks sessions as `status='archived'` (NOT hard-deleted in V2; archival is a soft marker).
  - Archives associated `chat_message`, `chat_artifact`, and `chat_summary` rows by setting a
    retention marker in metadata: `{archived_at: timestamp}`. Hard deletion is out of scope for V2.

### Must NOT build

- Chat session schema (task 043)
- Chat service session/message/turn CRUD (task 044)
- Prompt assembly layer 6 (conversation history) — task 050 calls `ShouldSummarize` and enqueues the job
- Memory extraction pipeline (task 039)
- Model gateway implementation (tasks 035, 036)
- General retention sweep for non-chat tables (task 063)

## Acceptance Criteria

- [ ] `ShouldSummarize` returns `true` when estimated unsummarized token count exceeds 55% of `layerBudgetTokens`; returns `false` when below threshold
- [ ] `Summarizer.RunForSession` covers exactly the oldest 25–30% of unsummarized turns and writes a `chat_summary` row with matching `from_sequence` / `to_sequence`
- [ ] Summary preserves verbatim: a file path like `/workspace/src/main.go` in the original messages appears unchanged in `summary_text`
- [ ] Calling `RunForSession` when the target range is already covered returns `ErrAlreadySummarized` and does not create a duplicate `chat_summary` row
- [ ] `chat_session_cleanup` job with `cleanup_type='tool_result_compaction'` compacts tool_result messages >4KB; `status` remains `'final'`; `metadata.compacted=true`
- [ ] Summary consolidation: session with 7 `chat_summary` rows → after consolidation job → 1 summary row covering the full original range
- [ ] `RunCleanup` with `cleanup_type='ephemeral_purge'` does NOT delete `role='assistant'` or `role='user'` messages

## Tests Required

**Unit tests:**
- `ShouldSummarize` threshold: mock message history totaling 52% of budget → returns true; 48% → returns false
- `Summarizer.RunForSession` target range selection: session with 100 turns (0 summarized) → selects turns 1–28 as target (28% of 100); session with turns 1–50 already summarized and 51–150 unsummarized → selects turns 51–87 (roughly 28% of 100 unsummarized)
- `ErrAlreadySummarized`: mock existing summary covering the full candidate range → verify error returned, no DB write attempted
- Tool result compaction: message content 5000 bytes → after compaction → `"[tool_result compacted — 5000 bytes]"`, `metadata.compacted=true`

**Integration tests:**
- Full summarization round-trip: seed session with 40 messages (10 turns) in test DB; call `RunForSession`; verify `chat_summary` row created with correct from_sequence/to_sequence; verify model was called with `invocation_purpose='summarization'`
- Idempotency: call `RunForSession` twice for same session/range → second call returns `ErrAlreadySummarized`; DB has exactly 1 summary row
- Summary consolidation: seed 7 `chat_summary` rows for a closed session; run `summary_consolidation` job; DB has 1 summary row covering original range

**E2E tests:**
- None — covered by dedicated E2E task 072 and 083

## Implementer Notes

- The `ShouldSummarize` check is called from inside the prompt assembly pipeline (task 050), which runs synchronously on the hot path. Keep it fast: a DB query counting unsummarized messages multiplied by average character length divided by 4 is sufficient. Do NOT make a model call here.
- "Verbatim preservation" in summaries is implemented by injecting the following instruction into the summarization system prompt: "Preserve ALL file paths, URLs, artifact IDs, code blocks, and tool call/result pairs exactly as they appear. Do not paraphrase, abbreviate, or omit these elements. If in doubt, copy verbatim."
- The 25–30% target for summarization is approximate. The exact algorithm: find the sequence_number of the oldest unsummarized message; find the sequence_number of the message at the 28th percentile of unsummarized messages; set `to_sequence` to that percentile boundary. Round to the nearest turn boundary (do not cut in the middle of a turn's messages).
- `chat_summary.model_invocation_id` should be populated by retrieving the model invocation ID from the model gateway response and storing it on the summary row. This enables cost attribution for summarization model calls.
- Tool result compaction (4KB threshold) applies per-message, not per-session total. The 4KB limit is the `length(content)` of a single `chat_message` row. Compaction replaces the content in-place using `ChatMessageRepo.UpdateContent` — this is one of the few post-final content updates allowed (the repo must allow compaction explicitly; consider a dedicated `CompactContent` method that bypasses the `status='final'` guard).
