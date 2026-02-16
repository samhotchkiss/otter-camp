# Issue #112 — Living Priority Stack

> STATUS: NOT READY
> Inspired by: Eric Osiu's agent infrastructure article (https://x.com/ericosiu/status/2020883003346714666)

## Problem

When priorities shift, there's no way to tell all agents at once. You either message each one individually, update a shared file and hope they read it, or rely on the next heartbeat cycle to propagate changes. By the time all agents know about a priority shift, hours have passed and some are still working on yesterday's focus.

## Concept

A single, org-wide priority stack that all agents read before acting. When priorities change, every agent knows immediately.

### How It Works

1. **Operator sets priorities** — via OtterCamp UI, CLI, or voice input (see voice pipeline below)
2. **Priorities stored in OtterCamp** — structured, versioned, with history
3. **Recall system injects priorities** — when an agent gets a message, the recall context (from #111) includes current active priorities
4. **Agents reprioritize** — work that aligns with current priorities gets attention; work that doesn't gets deprioritized

### Priority Structure

```json
{
  "priorities": [
    {
      "rank": 1,
      "title": "Ship Otter Camp memory system",
      "context": "This is the biggest gap in our infrastructure. Everything else is secondary until memory works.",
      "scope": "org",
      "set_by": "sam",
      "set_at": "2026-02-09T12:00:00Z"
    },
    {
      "rank": 2,
      "title": "Technonymous content pipeline",
      "context": "Need 3 posts per week. Stone owns this.",
      "scope": "team:content",
      "set_at": "2026-02-08T10:00:00Z"
    }
  ],
  "updated_at": "2026-02-09T12:00:00Z"
}
```

### Integration with #111 (Memory System)

This could be implemented as a special `kind` in the shared knowledge system:
- `kind: 'priority'` entries with `rank` in metadata
- Recall system always includes active priorities at the top of injected context
- Memory Agent detects priority shifts in conversations and auto-updates

### Voice Input Integration

The highest-value input method (from Eric's article):
1. Operator records voice note (phone, desktop, whatever)
2. Transcription (Whisper/WisprFlow)
3. Memory Agent extracts priority changes from transcript
4. Priority stack auto-updates
5. All agents reprioritize on next interaction

This is the "talk → agents move" loop. Total time from voice note to agent reprioritization: seconds.

### UI

- Priority list in OtterCamp dashboard sidebar (always visible)
- Drag-to-reorder
- Scope filters (org-wide vs team-specific)
- History view (what changed and when)
- Per-agent view: "Here's what [agent] thinks the priorities are" (confirm alignment)

### CLI

```bash
otter priorities list
otter priorities set 1 "Ship memory system" --context "Biggest gap, everything else secondary"
otter priorities reorder 3 --to 1
otter priorities clear 5
otter priorities history
```

## Dependencies

- [ ] #111 — Memory Infrastructure (shared knowledge + recall system)

## Open Questions

1. Should priorities auto-expire, or persist until explicitly changed?
2. Per-team priorities vs org-only? (Spec above supports both via scope)
3. Should agents be able to suggest priority changes, or is this operator-only?
