# 16. Agent Control Plane

## Purpose

Define how agents get real operational control over OtterCamp and external systems while preserving safety, auditability, and multi-tenant isolation.

## Outcome

Agents can perform meaningful work (project updates, tool execution, CLI and browser actions, MCP calls) through one trusted runtime path with explicit permissions and full traceability.

## Scope

- In scope:
  - Agent authorization model
  - Capability system
  - Policy evaluation and risk gating
  - Execution broker and worker orchestration
  - Approval workflows
  - Audit and observability requirements
- Out of scope:
  - UI details for admin configuration
  - Provider-specific model tuning internals

## Core Principles

- Agent actions are never direct. All actions pass through a control plane.
- Every action has an accountable principal (human or agent identity).
- Capability grants are explicit and minimally permissive.
- High-risk actions are gated by policy and can require human approval.
- Every action is replayable from immutable execution records.

## Control Plane Components

- Policy API: stores and evaluates capability policies.
- Execution Broker: admission control for all requested actions.
- Worker Runtime: executes approved actions in controlled sandboxes.
- Approval Service: handles human-in-the-loop decisions.
- Audit/Event Log: immutable record of decisions and outcomes.

## Canonical Execution Entities

- Run: one end-to-end unit of work.
- RunStep: one stage inside a run.
- RunAttempt: retry envelope for a run or step.
- ModelInvocation: one model call with cost/latency metadata.
- ToolExecution: one tool call with structured input/output.
- RunArtifact: files/screenshots/log outputs produced by execution.
- RunEvent: append-only timeline event for replay/debug.

## Agent Principal Model

Each action request includes:

- `principal_type`: `agent` or `human`
- `principal_id`
- `organization_id`
- `project_id` if project-scoped
- `session_id` or `task_id` context
- `delegated_by` when a human explicitly delegates

## Capability Model

Capabilities are namespaced action permissions.

- Project domain examples:
  - `project.task.read`
  - `project.task.update`
  - `project.flow.advance`
  - `project.flow.blocker.raise`
- Chat domain examples:
  - `chat.session.read`
  - `chat.message.write`
  - `chat.participant.manage`
- System control examples:
  - `system.cli.execute`
  - `system.browser.control`
- Integration examples:
  - `mcp.connection.use:<connection_id>`
  - `mcp.tool.invoke:<connection_id>:<tool_name>`

## Policy Evaluation

Policy decision outcomes:

- `allow`
- `deny`
- `require_approval`

Policy inputs:

- principal identity and role
- capability requested
- target resource scope
- risk attributes (command class, domain, network target, write operation)
- runtime budgets (token, time, money)

Policy layers (highest priority first):

1. Instance safety policy
2. Organization policy
3. Project policy
4. Agent profile policy
5. Request-specific overrides (most restrictive only)

## Execution Lifecycle

1. Agent requests an action.
2. Broker creates `Run` and initial `RunStep`.
3. Broker evaluates policy.
4. If `deny`, run fails with reason.
5. If `require_approval`, run is paused and queued for human review.
6. If `allow`, broker dispatches to worker.
7. Worker executes action in sandbox.
8. Worker emits `RunEvent` updates and stores artifacts.
9. Broker finalizes result and emits realtime update.

## Approval Workflow

Approval requests include:

- requested capability
- normalized action payload
- risk summary
- expected side effects
- diff or preview when available
- timeout/expiry

Approval actions:

- approve
- reject
- approve with constraints (for example reduced scope)

On approval, the run resumes from the blocked step with a new `RunAttempt`.

## Sandboxing and Isolation

- CLI execution:
  - constrained working directories
  - restricted environment variables
  - time and resource limits
  - configurable network policy
- Browser execution:
  - isolated browser contexts per run/session
  - domain allowlists/denylists
  - artifact capture (screenshots, traces)
- MCP execution:
  - per-connection capability checks
  - request/response schema validation

## Reliability and Recovery

- All run state transitions are durable.
- Retries are explicit attempts, never silent overwrites.
- Idempotency keys required for mutating operations.
- Dead-letter handling for repeated execution failures.
- Operator actions are also audited as control-plane events.

## Observability Requirements

Per run and per step capture:

- status and timestamps
- principal and capability
- policy decision and reason
- execution latency
- token/cost metrics where applicable
- failure class and stack/log reference

Operational dashboards must support:

- active/running/blocked runs
- approval queue depth and age
- failure rate by capability and agent
- cost by org/project/agent/model

## Security Requirements

- Default-deny capability posture.
- No privileged execution path outside broker/worker.
- Secret references resolved at execution time, never persisted in plain text run payloads.
- Tamper-evident audit trail for policy decisions and action outcomes.

## API Contract Surface (Initial)

- `POST /control/runs`: request an action run
- `GET /control/runs/{id}`: run detail
- `GET /control/runs/{id}/events`: event stream
- `POST /control/runs/{id}/cancel`: cancel run
- `POST /control/approvals/{id}/approve`: approve blocked action
- `POST /control/approvals/{id}/reject`: reject blocked action
- `GET /control/policies/evaluate`: dry-run policy simulation

## Integration with Other V2 Specs

- Chat: control plane executes chat-triggered tool actions.
- Projects/tasks: flow transitions and blockers use capability checks.
- Models: model invocations are run steps with cost accounting.
- MCP: all MCP tool calls run through broker policy.
- System control: CLI/browser actions require explicit capabilities.

## Non-Goals for Initial Release

- User-authored arbitrary policy scripting language.
- Full cross-region distributed run scheduler.
- Peer-to-peer agent trust without central policy evaluation.

## Open Questions

- Should approval requirements be static policy or dynamically risk-scored?
- Do we allow “session-wide approval grants” or only action-by-action approval?
- What minimum set of capabilities ships as default templates?

