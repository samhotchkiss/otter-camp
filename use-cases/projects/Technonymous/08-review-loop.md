# Technonymous — Review Loop Details

## Chat → Scratch File Flow
- Notes from **Project Chat** can be saved into `/notes/`.
- Trigger options (MVP):
  - **Explicit command** (“save this to notes”) in chat.
  - **Manual action** by owner/agent from chat UI.
- Avoid auto‑saving every message to keep notes curated.

## Comment Notifications
- When a review is saved (CriticMarkup comments added), notify the **issue owner agent**:
  - “X new inline comments added”
  - Include links to the comment anchors.

## Comment Resolution
- Use a **resolved state** stored in OtterCamp (not in the Markdown).
- UI marks comment resolved; underlying CriticMarkup token can be removed on next save or retained with a `resolved=true` flag in OtterCamp metadata.

## Diff Between Review Cycles
- Provide a “changes since last review” diff:
  - Compare current commit to the last **review commit**.

## Final Approval UX
- Provide explicit **Approve** action.
- Approval moves issue to **Needs Review (Human)** → **Completed**.
- Optionally lock the document after approval (MVP optional).
