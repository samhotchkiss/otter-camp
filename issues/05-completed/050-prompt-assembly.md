# 050: Prompt Assembly Engine

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 05 §PromptLayers, doc 05 §PromptAssembly, doc 05 §MemoryInjection, doc 10 §SkillActivation, doc 09 §MCPPrompts |
| Spec status | finished |
| Depends on | 043, 044, 045, 013, 011, 020, 038, 040, 049, 033 |
| Blocks | 048, 072, 083 |

## Scope

Build the 7-layer prompt construction pipeline that assembles the final prompt sent to the
model on each agent turn. Covers each layer, per-layer token counting, truncation/compression
logic, memory injection, skill activation, and MCP prompt loading.

### Must build

**`PromptAssembler`** — the central orchestrator for prompt construction:
- `Assemble(ctx, input AssemblyInput) (*AssembledPrompt, error)`
  - `AssemblyInput`: `{session_id, turn_id, agent_id, model_profile_id, tool_descriptors []ToolDescriptor}`
  - `AssembledPrompt`: `{messages []PromptMessage, system_prompt string, total_tokens int, layer_tokens map[string]int, memory_manifest MemoryManifest, errors []string}`
  - Returns `ErrContextCompressed` (non-fatal) if context was compressed during assembly; the turn engine should note this but continue.
  - All layer token counts are recorded in the return value for storage in `model_invocation.metadata`.

**Layer 1 — Agent identity** (never cut):
- Source: `agent.system_prompt` + `agent.operator_instructions` (if set).
- Formatted as the `system` role message (not a user-turn message).
- Token budget: reserved; this layer is never truncated under any circumstances.
- If `agent.system_prompt` is null (temp agent with no explicit prompt): use a default: `"You are a helpful AI assistant operating within the OtterCamp system."`.

**Layer 2 — Policies and constraints** (never cut):
- Source: evaluated capability policy for this agent + session (task 033 `PolicyEvaluator`).
- Includes: active deny-list summaries ("You are not permitted to execute shell commands"),
  any agent-specific `operator_instructions` overrides, and trust tier of the current session.
- Formatted as a continuation of the system message (appended to Layer 1, same `system` role block).
- Token budget: reserved; never truncated.

**Layer 3 — Scope context**:
- Source: the session's `scope_type` and `scope_id` context.
  - `scope_type='organization'`: inject org name and description from `organization` table.
  - `scope_type='project'`: inject project name, description, and current flow template name from `project` table.
  - `scope_type='project_task'`: inject project name + task title + task description + current
    flow node name + flow node description + visit count (if `flow_node_execution.visit_count > 1`,
    add: "Note: this node has been visited N times previously.").
- Formatted as a system message section: `"## Current Context\n..."`.
- Token budget: 500 tokens soft cap; if context data exceeds 500 tokens, truncate the task
  description (not the project name or node name) with `[truncated]`.

**Layer 4 — Skills and MCP prompts**:
- Source A — Skills: load attached skills for this agent+flow_node combo.
  - Priority order: `flow_node_skill` rows (ordered by `position` column, task 017/025) →
    `agent_skill_attachment` rows (ordered by `priority`).
  - For each skill: read the skill file at `skill.file_path` from the skills directory
    (relative to `OTTERCAMP_SKILLS_DIR` env var; default `./skills/`).
  - Skills with lower position number are injected first (higher priority).
- Source B — MCP prompts: for each MCP connection in the session's active tool set (task 049),
  if the connection has a `prompts` capability, fetch the list of MCP prompts from the
  connection and inject them.
  - MCP prompts are fetched via the MCP protocol (task 020/021); cache per session for session lifetime.
  - Conflict resolution: if a skill and an MCP prompt cover the same domain (detected by
    keyword overlap in the title/description), skills take precedence over MCP prompts.
    Log a warning: `"skill '{name}' overrides MCP prompt '{name}' on domain conflict"`.
- Token budget: 2000 tokens total for Layer 4. If combined skills + MCP prompts exceed budget:
  truncate MCP prompts first (lower priority), then truncate lower-priority skills. Never cut
  the first skill (position=0 / highest priority).
- Formatted as a system message section: `"## Skills and Context\n<skill content>\n\n<mcp prompts>"`.

**Layer 5 — Memory injection**:
- Source: `MemoryRetriever.Query` (task 040) with `mode='passive'`.
- `MemoryRetriever.Query` returns ranked memories (most relevant first). The prompt assembler
  reverses the order before injection: **most relevant memory last** (closest to the end of
  the system block, nearest to the actual conversation). This is the "attention-aware ordering"
  from the spec.
- Token budget: `memory_budget_tokens` (resolved from model profile; default 8000 tokens).
- Budget-aware: inject memories greedily from lowest-priority (first in the reversed list) up
  to the token budget. If a single memory would exceed the remaining budget, skip it and try
  the next (do not truncate individual memory content).
- Cooldown enforcement: memories that were injected in the PREVIOUS turn (tracked via
  `memory_manifest` stored in `model_invocation.metadata`) are skipped unless the session has
  fewer than 3 memories in total (avoid empty injection for sparse memory orgs).
- Sensitivity gate: memories with `sensitivity='restricted'` are excluded from passive injection.
  The `MemoryRetriever.Query` call with `mode='passive'` already excludes restricted memories;
  this is documented here for clarity.
- Formatted as a system message section: `"## Relevant Context\n<memory content sorted most-relevant-last>"`.
- `MemoryManifest`: records which memory IDs were injected (for cooldown tracking and reaction feedback).

**Layer 6 — Conversation history**:
- Source: `ChatMessageRepo.ListBySession` — fetch messages up to a budget, starting from the most recent.
- Summarization integration: before fetching raw messages, load all `chat_summary` rows for the session
  and use them to replace the oldest messages:
  - Messages with `sequence_number` within a summary's `[from_sequence, to_sequence]` range
    are replaced by the summary text as a single `system` role message: `"[Summary of earlier conversation]: <summary_text>"`.
  - Messages outside any summary range are included as raw messages.
  - Trigger summarization check: call `SummarizationChecker.ShouldSummarize(ctx, sessionID, layer6Budget)`.
    If `true`, enqueue a `chat_summarize` job (async) and continue with current history (the
    summarization job runs after this turn completes).
- Token budget: `layer6_budget_tokens` (resolved from model profile; the largest layer — typically 60–70% of the model's context window minus other layers).
- History truncation: if raw messages + summaries exceed the layer 6 budget, drop the oldest
  messages first (never drop summaries; summaries are kept even if they are old). If the budget
  is exceeded even after dropping all raw messages (only summaries remain but they still exceed budget):
  emit `ErrContextCompressed` (non-fatal) and truncate the oldest summaries.
- Formatted as the conversation message array (user/assistant/tool_call/tool_result roles).

**Layer 7 — Tool descriptions** (dropped first):
- Source: `ToolDescriptor` list from `AssemblyInput.tool_descriptors` (populated by task 049).
- Format: the `tools` array in the model API request (JSON Schema per-tool, standard format).
- Token budget: ~4000–5000 tokens soft cap. If the full tool list exceeds 5000 tokens:
  - Remove deprioritized tools first (Stage 3 from task 049 ordered them last).
  - Remove MCP tools second (prioritized tools last to be cut).
  - Never remove tier-1 core tools (memory.query, file.read, etc.).
- Layer 7 is the first layer to be reduced when context is tight. The system prompt layers
  (1–5) are never cut for Layer 7 reduction.

**Token counting:**
- Use a lightweight token estimator: `len(text) / 4` (characters / 4) as a proxy for tokens.
  This is fast and good enough for budget gating. Full tokenizer (tiktoken or equivalent) is
  NOT used on the hot path in V2.
- Per-layer token counts are computed before and after truncation and stored in the `layer_tokens`
  map: `{"layer1": 450, "layer2": 120, "layer3": 380, "layer4": 1200, "layer5": 3800, "layer6": 14000, "layer7": 4200}`.
- Total token count = sum of all layers.
- These counts are stored in `model_invocation.metadata` by the turn engine after the model call.

**`MemoryManifest`** (struct):
- `InjectedMemoryIDs []uuid.UUID` — IDs of memories injected in this turn
- `TotalMemories int` — total memories considered before budget/cooldown filtering
- `BudgetUsed int` — tokens used by Layer 5
- This is returned from `Assemble` and stored in `model_invocation.metadata` as JSON.

### Must NOT build

- Memory retrieval pipeline implementation (task 040, already built — just called here)
- Model gateway implementation (tasks 035, 036 — called by the turn engine, not here)
- Tool resolution pipeline (task 049, already built — result passed in)
- Skill file reading abstraction (use `os.ReadFile` or the object storage layer for file-backed skills; this is thin I/O, not a separate service)
- Progressive summarization execution (task 045 — only the check is called here; enqueuing is done here)
- Chat service CRUD (task 044)

## Acceptance Criteria

- [ ] Layer 1 (agent identity) is always present in the output; it is never truncated even when the total context would exceed the model's window
- [ ] Layer 5 (memory injection) injects memories in most-relevant-LAST order (the highest cosine-score memory is the last memory item in the injected block)
- [ ] Layer 5 cooldown: a memory injected in the previous turn is NOT injected again in the current turn (unless there are fewer than 3 total org memories)
- [ ] Layer 6 (conversation history): summaries replace the covered message range; raw messages in the summarized range do NOT appear in the assembled prompt
- [ ] Layer 7 (tool descriptions) is reduced first when total token count exceeds the model's context window; Layers 1–5 remain intact
- [ ] `layer_tokens` map in the returned `AssembledPrompt` has non-zero entries for all 7 layers (or zero for layers that contribute 0 tokens, e.g. no skills attached)
- [ ] `ErrContextCompressed` is returned (non-fatal) when Layer 6 budget is exceeded after dropping all raw messages
- [ ] Skill conflict with MCP prompt: a warning is logged; skill content takes precedence over the MCP prompt content

## Tests Required

**Unit tests (95% coverage target):**
- Layer 1 always present: mock all other layers to return max-token content; verify Layer 1 still in output
- Layer 5 ordering: mock retriever returning [memory_A (score=0.9), memory_B (score=0.5), memory_C (score=0.3)]; verify injection order in prompt is [C, B, A] (most relevant last)
- Layer 5 cooldown: previous `MemoryManifest.InjectedMemoryIDs = [id_A]`; current retriever returns [A, B, C]; verify A is skipped; output contains [C, B] in order
- Layer 5 budget: token budget = 200 tokens (4×50 chars); memory_A = 60 chars, memory_B = 60 chars, memory_C = 60 chars; verify only 3 memories fit (180/200); 4th memory skipped
- Layer 6 summary substitution: session has summary covering sequence 1–20 and raw messages 21–25; assembled history contains 1 summary message + 5 raw messages; NOT 20 raw messages + summary
- Layer 7 reduction order: deprioritized tools dropped before prioritized; core tier-1 tools never dropped
- `ErrContextCompressed` trigger: mock Layer 6 producing content that exceeds budget even after all raw messages dropped; verify error returned
- Token estimator: `estimateTokens("hello world") == 2` (11 chars / 4 = 2, integer division)
- MCP prompt conflict: mock skill "git workflow" and MCP prompt "git workflow guide"; verify skill wins, warning logged

**Integration tests:**
- Full assembly with real DB: seed session + agent + 10 messages (3 covered by a summary) + 2 active memories + 1 skill; call `Assemble`; verify: Layer 1 from agent.system_prompt, summary present, 7 raw messages, 2 memories injected, skill content present, total_tokens > 0
- Layer 4 skill file read: create a skill row with `file_path='skills/test-skill.md'`; write file to disk at the expected path; call `Assemble`; verify skill content in Layer 4 section of output
- Memory cooldown integration: call `Assemble` twice for same session; first call returns manifest with memory_A; mock previous manifest stored in invocation metadata; second call skips memory_A

**E2E tests:**
- None — covered by dedicated E2E task 072 and 083

## Implementer Notes

- The 7-layer assembly order in the final `messages` array for the model API:
  1. A single `system` role message containing Layers 1+2+3+4+5 concatenated (system block is one message for models that only accept one system message).
  2. Layer 6 conversation history messages (user/assistant/tool_call/tool_result, in chronological order).
  3. Layer 7 is NOT in the messages array — it is the `tools` parameter of the model API call.
- Layer 5 memory injection uses passive mode via `MemoryRetriever.Query(ctx, QueryInput{SessionID, Mode: "passive"})`. The retriever handles scope filtering and sensitivity gating. The assembler only needs to reverse the order and apply the token budget + cooldown filter.
- Skill file reading: the `skill.file_path` is a relative path. Resolve it against `OTTERCAMP_SKILLS_DIR` (env var, default `./skills/`). Use `os.ReadFile`. If the file does not exist, log a warning and skip the skill (do not fail the assembly). This is a graceful degradation.
- MCP prompt fetching: call `MCPConnection.ListPrompts()` for each connection with a prompts capability. This is a live MCP protocol call (task 020/021). Cache the result per session (store in `session_tool_set.metadata` or a dedicated in-memory cache keyed by `session_id + connection_id`). MCP prompt fetching should be parallelized across connections.
- The `MemoryManifest` returned from `Assemble` must be passed back to the turn engine (task 048), which stores it in `model_invocation.metadata`. On the next turn for the same session, the turn engine retrieves the previous invocation's manifest from `model_invocation.metadata` and passes it as `PreviousManifest` in the `AssemblyInput`. The assembler uses this to enforce the cooldown.
- Layer 4 conflict detection: compare the first 30 words of the skill description against the first 30 words of each MCP prompt description. If 3+ words overlap (stop-word-filtered), consider it a conflict. This is intentionally simple heuristic; false positives result in MCP prompts being suppressed (skill wins), which is the safe outcome.
