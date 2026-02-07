# Technonymous — File‑Type Driven Editor

## Objective
Select the correct editing/review interface based on file extension.

## Rules (MVP)
- **.md** → rendered Markdown + source toggle + CriticMarkup comments
- **.txt** → plain text editor
- **.go/.ts/.js/.py** → code editor with syntax highlighting + diff
- **.png/.jpg/.gif** → image preview (no inline comments in MVP)

## Acceptance Criteria
- UI switches editor based on file extension.
- Markdown docs show the special review interface.
