# Instructions: Memory Architecture (Three-Layer System)

> How OtterCamp's memory system works from a user/agent perspective.
> Last updated: 2026-02-16

## The Three Layers

### Layer 1: Conversation Extraction
**What:** Facts extracted from conversations and JSONL session logs.
**Source:** What people said — chat messages, agent sessions, heartbeats.
**How:** Ingestion pipeline extracts structured memories (facts, decisions, preferences, lessons), embeds at 1536d, stores in DB.
**Strength:** Breadth. Catches everything — you never know what conversation detail matters later.
**Weakness:** Frozen at extraction time. If the world changes after the conversation, the memory is stale.

### Layer 2: Taxonomy Classification
**What:** Organizing extracted memories into a hierarchical tree for navigation and retrieval.
**Source:** Layer 1 memories, classified into taxonomy nodes.
**How:** Memories are tagged and grouped into a navigable tree (e.g., `personal→vehicles`, `projects→otter-camp→memory-system`).
**Strength:** Findability and completeness. Navigate to the right node, get everything under it. Deterministic, cheap, no embedding needed.
**Weakness:** Requires maintenance. Taxonomy must evolve as new topics emerge.

### Layer 3: Project Documentation
**What:** Authored, maintained markdown docs within each project's `docs/` directory.
**Source:** Written by the agent or human who owns that domain. Curated, not extracted.
**How:** Structured per the project docs spec (see `project-docs-spec.md`). Ellie reads these directly.
**Strength:** Depth and authority. When a structured doc exists, it's the most trustworthy source. Always current if maintained.
**Weakness:** Requires effort. Someone has to write and maintain them.

## How the Layers Interact

```
Conversations happen
    ↓
Layer 1: Extract facts into memories (automated)
    ↓
Layer 2: Classify into taxonomy tree (automated + periodic review)
    ↓
When a topic gets complex enough:
    ↓
Layer 3: Author canonical docs (human/agent-driven)
    ↓
Layer 3 docs supersede scattered Layer 1 extractions for that topic
```

## What This Means for You (as an Agent)

1. **Your conversations are being extracted.** Important facts, decisions, and preferences you discuss will become memories. Be clear and specific — vague statements make bad memories.

2. **The taxonomy organizes your knowledge.** When you need to recall something, the taxonomy tree helps find it even when vector search misses.

3. **When you build something complex, document it.** If a project or subsystem reaches the point where scattered conversation facts aren't enough, write the docs. Put them in `docs/` per the spec. They become the authoritative source.

4. **Don't dump markdown files randomly.** All documentation goes in the project's `docs/` dir with the defined structure. No exceptions.

5. **Commit your docs changes.** Small commits, descriptive messages. The git history is how we know what changed and when.

## What This Means for Retrieval

When Ellie answers a question, it draws from all three layers:
- **Quick personal facts** → Layer 1 (conversation extraction)
- **"Everything about X"** → Layer 2 (taxonomy navigation)
- **"How does X work?"** → Layer 3 (project docs, if they exist)

The query router (being built) will learn which layer to prioritize based on the question type.

## Change Log

- 2026-02-16: Created three-layer memory architecture doc.
