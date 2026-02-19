# Shell Layout Handoff Notes

These notes define how follow-on specs should integrate with the new shell model.

## Scope

- Spec 502+ should treat the shell as already migrated to sidebar/header/workspace/chat-slot structure.
- Page redesign specs should only update page body regions inside `main#main-content` unless explicitly changing shell behavior.

## Shell Contract

- `shell-layout`: root app shell container
- `shell-sidebar`: left navigation rail (mobile state via `.open`)
- `shell-header`: top control/status row
- `shell-workspace`: content + chat split wrapper
- `shell-content`: scrollable page content area
- `shell-chat-slot`: right chat dock container

## Do

Do:
- Keep page content inside `shell-content`.
- Reuse shell nav/route adapter links (`/inbox`, `/projects`, `/project/:projectId`, `/issue/:issueId`, `/review/:documentId`).
- Preserve command palette and bridge status visibility in `shell-header`.
- Add targeted tests when shell controls or nav behavior change.

Don't:
- Remove `shell-sidebar`/`shell-chat-slot` wrappers in page-level specs.
- Reintroduce topbar-first layouts that bypass the shell structure.
- Rename shell class contracts without updating tests and dependent specs.
- Move page-specific styles into shell/global layout files unless broadly reusable.
