# 19. Mobile UI

## Status: Stub — to be fleshed out.

## Purpose

Monitoring and lightweight interaction on the go. The operator should be able to check status, respond to escalations, and approve/reject review items from their phone.

## Key Capabilities (to be designed)

- Dashboard: project status at a glance, blocked items, progress summary.
- Notifications: push notifications for blockers, escalations, review requests, task completions.
- Quick actions: approve/reject review items, respond to agent escalations, unblock tasks.
- Chat: lightweight sync sessions with agents (e.g., quick question to Frank).
- Read-only views: task detail, run history, agent activity.

## Design Considerations

- Optimized for interruption-driven use — check, act, leave.
- Push notifications are the primary entry point (not browsing).
- Biometric auth for sensitive operations.
- Deep links from notifications to specific tasks/sessions.

## Open Questions

- Native (Swift/Kotlin) vs cross-platform (React Native, Flutter)?
- iOS first, or iOS and Android simultaneously?
- How much chat capability — full sessions or just quick replies?
- Is this a post-launch priority, or does it ship with V2?
