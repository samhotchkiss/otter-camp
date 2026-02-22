# 04. Auth, Tenancy, and Identity

## Requirements

- Multi-user login to one instance.
- Strong organization-level data segmentation.
- Coexistence of human identities and agent identities.

## Tenancy Model

- Primary segmentation by `organization_id`.
- All major entities carry org ownership.
- Row-level access enforcement in API and DB.

## Human Identity

- `human_user` with unique auth identity.
- Membership model: user can belong to one or more orgs.
- Session auth supports cookie and token modes.

## Agent Identity

- `agent` identity separate from human users.
- Agent credentials scoped to org and policy.
- Agent can act in chat and tasks under explicit permissions.

## Authorization

- Baseline RBAC roles: owner, admin, member, viewer.
- Resource-level policy checks for project/chat/task operations.
- Capability grants for sensitive actions (tool use, deployment, secrets).

## Auditability

- Every write includes actor identity (human or agent).
- Immutable audit event trail for security-sensitive actions.
- Delegation trace when a human authorizes an agent action.

## Open Questions

- Should we support SSO in V2 GA or post-GA?
- Do we need org-to-org sharing at launch?
- What is the minimum policy language for permissions?

