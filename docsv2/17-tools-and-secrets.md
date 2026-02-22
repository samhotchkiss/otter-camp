# 17. Tools and Secrets

## Purpose

Define a first-principles V2 design for tool execution and secret handling that is secure, auditable, and usable by both humans and agents.

## Outcome

- Agents can invoke tools through a single controlled runtime path.
- Secrets are never exposed in plain text to models or persisted in logs/artifacts.
- Tool and secret access is consistently enforced across chat, projects, MCP, CLI, and browser workflows.

## Scope

- In scope:
  - Tool definition and registration
  - Tool execution contracts and safety gates
  - Secret storage, access, rotation, and audit
  - Multi-tenant scope and permission model
- Out of scope:
  - Vendor-specific KMS implementation details
  - UI wireframes for admin settings

## Core Principles

- Default deny for tools and secret access.
- All tool calls execute via the Agent Control Plane.
- Tools declare schemas, risk class, and required capabilities.
- Secrets are referenced by handle, not by raw value.
- Secret values are resolved only at execution time.
- Logs and events are redacted by default.

## Canonical Entities

- `ToolDefinition`: immutable versioned contract for a tool.
- `ToolBinding`: scoped enablement of a tool for org/project/agent.
- `ToolExecution`: one run step invoking one tool.
- `SecretRecord`: encrypted secret metadata + ciphertext envelope.
- `SecretHandle`: opaque reference used by tools and agents.
- `SecretLease`: short-lived in-memory resolution of a secret for one execution.

## Tool Taxonomy

- `system`: built-in platform tools (project/task/chat operations).
- `runtime`: environment tools (CLI, browser control, file operations).
- `integration`: external tools exposed through MCP connections.
- `skill`: tools packaged with reusable skill modules.

## Tool Definition Contract

Each tool version must declare:

- `tool_id` and `version`
- `name` and `description`
- `input_schema` (JSON schema)
- `output_schema` (JSON schema)
- `required_capabilities`
- `risk_level` (`low`, `medium`, `high`, `critical`)
- `side_effect_class` (`read_only`, `state_change`, `external_action`)
- `timeout_ms` and `retry_policy`
- `network_requirements` (if any)

Tools without complete metadata are not executable.

## Tool Binding and Enablement

Enablement is explicit and scoped:

- instance-level deny/allow baseline
- organization tool policy
- project overrides
- agent profile constraints

A tool can be registered globally but disabled for most scopes by default.

## Execution Flow

1. Agent/human requests a tool action.
2. Control Plane validates tool contract and schema.
3. Policy engine checks capability + scope + risk.
4. If required, approval workflow blocks execution.
5. Worker executes with sandbox and resource limits.
6. Result is normalized to `output_schema`.
7. Events/artifacts are stored with redaction.

This flow is mandatory for every tool type.

## Secret Model

Secret records contain:

- `secret_id`
- `organization_id`
- optional `project_id`
- `name` (unique in scope)
- `provider` (`internal`, `env`, `vault`, `aws_sm`, etc.)
- `ciphertext` or provider reference
- `created_by`, `updated_by`
- `created_at`, `updated_at`, `expires_at` (optional)
- `rotation_policy` metadata

Secret plaintext is never persisted in application tables or event payloads.

## Encryption and Key Strategy

- Envelope encryption for stored secret material.
- Per-environment master key source (local, self-hosted, managed).
- Optional per-org data keys for stricter tenant isolation.
- Key rotation supported without changing secret handles.

## Secret Access and Injection

- Tool inputs carry `secret://` handles, not raw values.
- Worker resolves handles into short-lived `SecretLease` objects.
- Leases exist only in memory for the execution lifetime.
- Secret values are injected into subprocess env or request headers at dispatch time.
- Secret values are stripped from stdout/stderr before persistence.

## Permission Model

Required checks for secret use:

- principal has tool capability
- principal has secret capability (`secret.read` via policy)
- principal scope matches secret scope (org/project)
- policy allows this tool to use this secret class

Optional split permission model:

- `secret.manage`: create/update/delete
- `secret.use`: attach to tool execution
- `secret.read`: reveal raw value (admin-only, disabled by default)

## Redaction and Data Loss Prevention

- Global redaction pipeline for logs, events, traces, and artifacts.
- Deterministic tokenization for known secret values/hashes.
- Response filtering for model outputs to block accidental secret echo.
- Block persistence when redaction confidence is low and mark run as policy failure.

## Rotation and Revocation

- Manual rotation on demand.
- Scheduled rotation policies per secret class.
- Immediate revocation invalidates active leases.
- Tool bindings can be suspended without deleting secret records.
- Rotation/revocation emits audit events and operator alerts.

## Audit Requirements

Capture immutable events for:

- secret create/update/delete/rotate/revoke
- secret access attempts (allow/deny)
- tool execution with referenced secret handles
- approval decisions tied to high-risk tool calls

Audit records include principal, scope, timestamp, reason, and run linkage.

## Deployment Modes

- Local self-hosted:
  - default internal encrypted secret store
  - optional env-based bootstrap secrets
- VPS/self-managed:
  - internal store or external vault provider
  - operator-managed key material
- Managed OtterCamp hosting:
  - managed secret backend
  - tenant-isolated key hierarchy

Behavioral contract remains identical across modes.

## API Surface (Initial)

- `POST /tools/definitions`
- `GET /tools/definitions`
- `POST /tools/bindings`
- `GET /tools/bindings`
- `POST /secrets`
- `GET /secrets`
- `POST /secrets/{id}/rotate`
- `POST /secrets/{id}/revoke`
- `POST /control/runs` (tool execution entrypoint)

## Integration Links

- Uses `/docsv2/16-agent-control-plane.md` for execution governance.
- Must align with `/docsv2/04-auth-tenancy-and-identity.md` for scope and policy.
- Must align with `/docsv2/13-security-observability-costs.md` for audit and SRE controls.
- Applies to `/docsv2/09-mcp-integration.md` and `/docsv2/11-system-integration-cli-and-browser.md`.

## Non-Goals (Initial Release)

- End-user scriptable policy language.
- Direct model access to secret plaintext.
- Tool execution paths that bypass control plane checks.

## Open Questions

- Should secret scopes support task-level granularity, or stop at project scope?
- Do we allow one-time approval grants for high-risk secret use?
- Which external secret backends ship in v1 of V2 (if any)?
