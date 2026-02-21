# Instructions: Project Documentation Spec

> How to structure and maintain the `docs/` directory in any OtterCamp project.
> Last updated: 2026-02-16

## Why This Exists

OtterCamp's memory system (Layer 3) reads project docs directly as authoritative knowledge. Messy, unstructured docs mean bad memory. Clean docs mean clean recall. This spec ensures every project's documentation is navigable by both humans and Ellie.

## Required Structure

```
project-name/
  docs/
    START-HERE.md              # Required. Project map. Read-this-first index.
    <domain>/                  # One dir per major subsystem or topic area
      overview.md              # Required per domain. Entry point.
      <topic>.md               # One file per distinct topic
      open-questions.md        # Optional. Unresolved decisions and WIP.
```

### Example: OtterCamp

```
otter-camp/
  docs/
    START-HERE.md
    memories/
      overview.md
      vector-embedding.md
      recall.md
      entity-synthesis.md
      ...
    agents/
      overview.md
      ...
    projects/
      overview.md
      ...
    instructions/              # Special: how to work WITH OtterCamp
      START-HERE.md
      project-docs-spec.md     # This file
      ...
```

### Example: ItsAlive

```
itsalive/
  docs/
    START-HERE.md
    engine/
      overview.md
      scoring.md
      content-pipeline.md
    api/
      overview.md
      endpoints.md
    content/
      overview.md
      voice-guide.md
```

## Rules

### 1. START-HERE.md is mandatory
Every project's `docs/` dir has a START-HERE.md at the root. It is the index. It links to every domain dir with a one-line description. An agent landing cold reads this first.

### 2. One domain dir per major subsystem
Group related docs into directories. The domain name should be obvious and descriptive. If you can't name the domain in one or two words, you're grouping wrong.

### 3. Every domain has an overview.md
The entry point for that domain. Summarizes current state, key decisions, and links to topic files. Think of it as the START-HERE for that subdomain.

### 4. One topic, one file
If a file covers two distinct topics, split it. If two files cover the same topic, merge them. File names are lowercase, hyphenated, descriptive.

### 5. Docs are authored, not extracted
These are maintained by the agent or human who owns that domain. They're written with intent, reviewed, and updated. They are NOT:
- LLM dump files with no curation
- Auto-generated docs that nobody reads
- Copy-paste from chat conversations

### 6. Change log at the bottom of every file
Every doc ends with a `## Change Log` section. Every meaningful update gets a dated entry. Format: `- YYYY-MM-DD: <what changed>.`

### 7. Commit on every meaningful change
Small commits with descriptive messages. Don't batch. The git history is the audit trail.

### 8. Update START-HERE.md when adding docs
If you create a new domain dir or significant topic file, update the project's START-HERE.md to reference it. Orphan docs are invisible docs.

## What's NOT Allowed

- **No markdown files outside `docs/`** — unless they're repo-standard files (README.md, CHANGELOG.md, etc.)
- **No docs without a home** — every file lives in a domain dir (or at the `docs/` root for cross-cutting concerns)
- **No mega-docs** — if a file exceeds ~300 lines, it probably covers multiple topics. Split it.
- **No orphans** — if START-HERE.md doesn't link to it, it doesn't exist

## How This Connects to Memory

OtterCamp's memory system has three layers:
1. **Conversation extraction** — facts pulled from chat (breadth)
2. **Taxonomy classification** — organizing memories into a navigable tree (findability)
3. **Project documentation** — these docs (depth and authority)

When Ellie retrieves context and a structured doc exists for the topic, the doc wins over scattered conversation extractions. Well-maintained project docs are the highest-quality memory source.

## Change Log

- 2026-02-16: Created project documentation spec.
