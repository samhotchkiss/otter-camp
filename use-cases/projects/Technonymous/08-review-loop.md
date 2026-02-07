# Technonymous — Review Loop Details

## Chat → Scratch File Flow
- Notes from **Project Chat** can be saved into `/notes/`.
- **Agent decides** what to save (per agent instruction set).
- Avoid auto‑saving every message to keep notes curated.

## Comment Notifications
- When a review is saved (CriticMarkup comments added), a **new commit** is created.
- Agent is notified of the **new commit** and processes inline comments in that commit.
- Optional: include boilerplate in the agent request describing comment format.

## Comment Resolution
- Comments are **not tracked individually**.
- Agent addresses feedback by updating the document and committing a new version.
- Discussion about feedback happens in issue chat if needed.

## Diff Between Review Cycles
- Provide a “changes since last review” diff:
  - Compare current commit to the last **review commit**.
- Allow viewing **older versions** (previous commits) to recover past comments.

## Final Approval UX
- Provide explicit **Approve** action.
- Approval moves issue to **Needs Review (Human)** → **Completed**.
- Trigger a **confetti celebration** on approval.
