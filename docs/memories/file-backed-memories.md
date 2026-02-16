# Memories: File-Backed Memories

> Summary: Ingesting memories from workspace files, freshness detection, and experimental results.
> Last updated: 2026-02-16
> Audience: Agents working on memory coverage and recency.

## What It Is

File-backed memories bridge the gap between what's in conversations and what's in files. Many important facts (current status, preferences, architecture docs, book chapters) live in markdown files that never get discussed in conversation — or get discussed once and then the file evolves while the memory stays frozen.

## Schema Additions

Three columns added to `memories` table:
- `file_path TEXT` — Absolute path to source file
- `file_content_hash TEXT` — MD5 hash for change detection
- `file_mtime TIMESTAMPTZ` — File modification time at scan

## Three Ingestion Tools

### 1. File Scanner (`file-scanner.mjs`)
- Scans 635+ .md files across SamsBrain + OpenClaw workspaces
- Extracts memories via Haiku with `file_path` linkage
- Embeds with OpenAI 1536d
- Deduplicates against existing memories
- **Result:** 630 memories from first 100 files (535 remaining)

### 2. Project Summarizer (`project-summarizer.mjs`)
- Generates one high-importance summary memory per project from directory tree
- **Result:** 27 project summaries

### 3. Memory MD Indexer (`memory-md-indexer.mjs`)
- Indexes agent MEMORY.md and daily note files as first-class memories
- These are high-signal — agents curate their own memories
- **Result:** 125 memories from agent workspaces

## Experimental Results

### Where File-Backed Memories Win

| Query Category | Baseline (vector) | With Files | Delta |
|---|---|---|---|
| current_state | 34% | 70% (file_only) | **+36pp** |
| preference | 67% | 100% (vector+files) | **+33pp** |
| file_content | 57% | 63% (file_only) | +6pp |

### Where File-Backed Memories Hurt

| Query Category | Baseline (vector) | File Only | Delta |
|---|---|---|---|
| project | 94% | 65% | **-29pp** |
| personal | 100% | 79% | **-21pp** |

### Why

- **Current-state wins:** Files get updated. Conversation memories are frozen at extraction time. When someone asks "what's happening now?", the file has the answer.
- **Preference wins:** Source-of-truth files like SOUL.md and AI Prompt.md contain richer preference detail than conversation paraphrases.
- **Project/personal losses:** Conversation-extracted memories have richer context and synthesis than raw file extractions. Using file_only loses this.

**Conclusion:** File-backed memories are an augmentation layer, not a replacement. Use them alongside conversation memories, never exclusively.

## Freshness Detection

`freshness-check.mjs` compares `file_content_hash` and `file_mtime` against current file state.

- Initial run: 64 file-backed memories checked, all fresh
- Should be scheduled periodically to detect drift

## Ecosystem Inventory

Full inventory documented in `FULL-ECOSYSTEM-INVENTORY.md`:
- **SamsBrain:** 442 .md files (vault, projects, agents, recipes, personal)
- **OpenClaw:** 13 active agent workspaces, 228 .md files
- **OtterCamp:** 1 project repo
- **Total:** 645 .md files, 100 scanned so far (535 remaining)

## Implications for Ellie

1. **Periodic file re-scan** — detect changes, re-extract stale memories
2. **Live file reading at query time** — when a memory has `file_path` and the query is current-state/preference, read the actual file for the freshest answer
3. **Don't over-index on file memories** — they supplement conversation memories, they don't replace them

## Related Docs

- `docs/memories/overview.md`
- `docs/memories/recall.md`
- `docs/memories/experiment-log.md`

## Change Log

- 2026-02-16: Created with file scanner results, freshness detection, and 6-strategy benchmark findings.
