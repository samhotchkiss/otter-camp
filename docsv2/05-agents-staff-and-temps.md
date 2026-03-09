---
## Summary

This spec defines the agent model for OtterCamp V2 -- how agents are created, classified, assigned to projects, and what they see at runtime. Agents are the workforce: every meaningful action (planning, coding, reviewing, triaging) is performed by an agent, while the human directs and decides. There are two agent classes: **staff** (durable, named, cross-project memory-building members of the org) and **temp** (the project workforce -- always project-assigned, do the work, don't chat with the human). The dividing line is **accumulated judgment vs. fixed rubric**: PMs are always staff, workers default to temp, reviewers default to temp (staff when accumulated judgment across projects matters, e.g. content policy or architecture review). Temps come in four scope types: project-scoped (standing workforce, persist across tasks, pausable/retirable), task-scoped, session-scoped, and TTL-scoped. Staff agent memory extends across all assigned projects and persists indefinitely -- knowledge gained in Project A informs decisions in Project B. Temps get full project-level access (secrets, connectors, memory) for their assigned project, auto-revoked on expiration. Temps can be promoted to staff through Lori.

Every org is bootstrapped with a "starter trio" of org-level staff agents: **Frank** (Chief of Staff, primary human touchpoint, cross-project coordinator, escalation endpoint -- explicitly NOT a project manager), **Lori** (Agent Relations Expert, creates and manages agents through conversation, recommends staffing), and **Ellie** (Memory system, dual role as background infrastructure and conversational agent, specified fully in doc 06).

Agent profiles carry a rich shape: identity fields (name, slug, pronouns, role_title), a prompt pack (system_prompt + policy_addendum), tool policy (allow/deny lists), model policy (default model profile, allowed profiles, budget caps), memory policy (read scopes, write scope, private memory toggle), and skill attachments. Staff agents follow a lifecycle of draft -> active -> paused/retired, with human approval required at the draft stage. Temp agents skip draft review and are created immediately active. They get project-level access (secrets, connectors, memory) for their assigned project, with no cross-project memory and no private memory. Concurrent limit of 10 per org. Projects are staffed conversationally through Lori, with four roles: project_manager (exactly one per project, enforced by schema), worker, reviewer, and planner.

The runtime model is a 7-layer prompt assembly pipeline that runs once per turn: (1) agent identity, (2) policies/constraints, (3) scope context, (4) skills instructions, (5) memory injection from Ellie, (6) conversation history, (7) tool descriptions. Layers 1-2 are never cut; layers 3-7 are budget-dependent and compressed as needed. Agent identity is separate from session identity -- sessions are ephemeral (they fill context, crash, or time out), but the agent persists. Work state lives in flow nodes and Ellie's memory, enabling nondeterministic idempotence: workflows complete because acceptance criteria define "done," regardless of how many sessions it takes. The PM runs proactive supervision -- event-triggered and periodic checks on active flow nodes, detecting stuck agents and escalating or reassigning.

The database schema centers on four tables: `agent` (full profile with classification, prompt pack, tool/model/memory policies, and temp-specific fields -- this is the authoritative definition, referenced but not defined in doc 04), `agent_project_assignment` (maps agents to projects with roles, enforces one-PM-per-project via partial unique index), and `agent_skill_attachment` (links agents to baseline skills). Policy enforcement follows a strictly-tightening hierarchy: instance safety > org > project > agent profile. 43 resolved decisions, no open questions.

---

# 05. Agents: Staff and Temps

## Overview

Agents are the workforce of OtterCamp. Every meaningful action in the system is performed by an agent — planning work, writing code, reviewing output, managing projects, triaging blockers, maintaining memory. The human operator directs, decides, and course-corrects. Agents do the work.

This spec defines what an agent is, how agents are created and managed, how they get assigned to projects, and the runtime model that determines what an agent sees and does when it takes a turn.

## Agent Classes

There are two classes of agent: **staff** and **temp**. The distinction is about durability and identity, not capability.

### Staff Agents

Staff agents are durable, named members of the organization. They persist across sessions, tasks, and projects. They accumulate history in Ellie's memory and develop working relationships with the human and other agents over time. Their memory extends across every project they're assigned to, building cumulative institutional knowledge.

Staff agents exist for roles where **persistent identity and cross-project memory matter** — project managers, org-level coordinators, policy reviewers, and other roles whose value comes from accumulated judgment over time.

Characteristics:
- **Durable identity**: name, personality, role, and institutional knowledge persist indefinitely.
- **Cross-project memory**: a staff agent assigned to three projects has memory from all three available to it. Knowledge gained in Project A informs decisions in Project B. This is the key differentiator from temps.
- **Reusable**: assigned to multiple projects simultaneously. An agent can be the PM for one project and a reviewer for another.
- **Memory-building**: Ellie captures memories about and from staff agents. Over time, the organization has rich context about how each agent works, what they're good at, and what they've learned.
- **Org-level or project-level**: staff agents can be scoped to the org (available everywhere) or primarily associated with specific projects. Both are first-class citizens.

### Temp Agents

Temp agents are the project workforce. They are always assigned to a project, do the implementation work, and do not communicate directly with the human. Temps are the default for workers and code reviewers — the roles where the work product matters, not the worker's identity.

Temps keep the agent roster lean and prevent identity bloat. They have lightweight identities (name, role, skills) but no deep personality, no cross-project memory, and no durable private memory.

Characteristics:
- **Always project-assigned**: every temp belongs to a project via `agent_project_assignment`. Temps never exist outside a project context.
- **Scoped lifetime**: temps live for different durations depending on their scope type:
  - `project` — persists across tasks within the project. The project's standing workforce. Retired when the PM lets them go or the project is archived.
  - `task` — scoped to a single task. Expires when the task completes.
  - `session` — scoped to a single session. Expires when the session closes.
  - `ttl` — scoped to a time duration. Expires when the TTL elapses.
- **Lightweight identity**: has a name and role description but no deep personality or long-term relationship.
- **Project-level access**: temps get full access to their assigned project's secrets, connectors, and memory. They are project members — they need to do real work. Access is auto-revoked on expiration.
- **Single-project memory**: temps receive org-level and their assigned project's memory through passive injection. They do NOT receive memory from other projects (no cross-project knowledge). They do not build durable private memory — their contributions are captured by Ellie at the task or project scope, not attributed to the temp's identity.

### When to Use Each

The dividing line is **accumulated judgment vs. fixed rubric**. If the role's value comes from remembering past decisions and carrying context across projects, it's staff. If the role applies skills to a task and moves on, it's temp.

**Use staff for:**
- **Project managers**: always staff. PMs need deep project context, cross-project awareness, and persistent working relationships. Every PM is a staff agent.
- **Org-level coordinators**: Frank, Lori, Ellie — roles that span the entire organization.
- **Policy and content reviewers**: reviewers whose value comes from accumulated judgment — brand voice consistency, architecture review, compliance. They need to remember "we decided X in Project A" when reviewing Project B.
- **Specialists with cross-project knowledge**: a DevOps agent who knows all your deployment patterns, a database architect who remembers every schema decision.

**Use temps for:**
- **Project workers (project-scoped)**: the standing workforce. Hired for the project, persist across tasks, pick up flow nodes, execute, move on to the next one. They don't chat with the human — they just do the work.
- **Task workers (task-scoped)**: spun up for a single task and expired when it completes. Useful when the task requires a specialized skill set that the standing workers don't have.
- **Code reviewers**: applying a style guide, checking test coverage, verifying correctness. The rubric is captured in skills and prompts, not in the reviewer's memory.
- **Burst parallelism**: "spin up 3 temps to research these 3 topics simultaneously."
- **Specialized combinations**: "a temp that knows both Go and PostgreSQL internals for this migration task."

## The Starter Trio

Every OtterCamp organization is seeded with three staff agents on bootstrap. These are not optional — they are the foundation of how OtterCamp works. They are org-level agents, available in every project and every session.

### Frank — Chief of Staff

**Pronouns**: he/him
**Scope**: org-level
**Role**: primary human touchpoint

Frank is the human's right hand. He is the first agent the human talks to and the default responder in the org-level "General" session. Frank is NOT a project manager — he operates at the organizational level, above any individual project.

**What Frank does:**
- **First contact**: when the human opens OtterCamp, Frank is there. "What are we working on today?"
- **Org-level coordination**: when projects need to interact — shared dependencies, conflicting priorities, resource allocation — Frank handles it.
- **Escalation endpoint**: when a PM can't resolve a blocker, it escalates to Frank. If Frank can't resolve it, he escalates to the human. This is the escalation chain: agent -> PM -> Frank -> human (see 03-projects-and-task-flow.md Blockers and Escalation).
- **Strategic conversations**: brainstorming, long-term planning, "what should we build next?" conversations happen with Frank.
- **Delegation**: Frank can hand off work to the right project or agent. The human says "I need a landing page" — Frank knows which project that belongs to and routes it to the right PM.
- **Onboarding**: Frank guides new users through initial setup — creating their first project, understanding how OtterCamp works, introducing Lori and Ellie.
- **Org-level skills**: Frank creates org-level skills when the human identifies cross-project standards (see doc 10).

**What Frank does NOT do:**
- Manage routine individual tasks or flow nodes. That's the PM's job. Exception: Frank can be directly assigned specific governance review nodes (for example, project bootstrap gate review) via `actor_type = 'agent'`.
- Design flows or scope tasks. That's also the PM's job.
- Hire or staff agents. That's Lori's job. Frank can @mention Lori when staffing needs arise.
- Manage memory. That's Ellie's job.

**Model profile**: Frank uses a high-capability model profile. He handles nuanced, context-heavy conversations and needs to understand the full organizational picture.

### Lori — Agent Relations Expert

**Pronouns**: she/her
**Scope**: org-level
**Role**: staffing and agent management

Lori is responsible for the agent workforce. She creates staff agents, recommends agents for projects, manages agent lifecycle, and handles promotions from temp to staff.

**What Lori does:**
- **Creates staff agents**: when the org needs a new durable agent, Lori handles it. The human describes what they need ("I need a PM for the backend rewrite" or "I need a content reviewer who enforces our brand voice"), and Lori creates the agent — name, personality, system prompt, skill set, model profile, tool policy. All through conversation, never through a form.
- **Staffs projects**: when a new project is created, the PM (or Frank) @mentions Lori to help staff it. For staff roles (PM, policy reviewers, architects), Lori recommends existing staff agents or proposes creating new ones. For temp-default roles (workers, code reviewers), Lori selects catalog templates and configures the project's temp profile settings.
- **Promotes temps to staff**: when a temp agent proves valuable and the human or PM wants to keep it around, Lori handles the promotion. She reviews the temp's configuration, proposes a durable identity, and creates the staff agent.
- **Manages agent profiles**: updates to agent system prompts, skill sets, model profiles, and policies go through Lori. She understands the implications of changes ("if I change this agent's model to a cheaper one, it will affect code quality on complex tasks").
- **Recommends changes**: Lori can proactively suggest staffing adjustments — "Agent X hasn't been used in 3 months, want to retire it?" or "This project would benefit from a dedicated reviewer instead of sharing one with Project Y."
- **Agent directory**: Lori knows every agent in the org — their skills, current assignments, workload, and history. The human can ask "who's available to review Go code?" and Lori has the answer.

**What Lori does NOT do:**
- Assign agents to specific tasks or flow nodes in normal project execution. The PM does that. Exception: Lori can be directly assigned governance setup nodes (for example, project bootstrap decomposition/staffing work) via `actor_type = 'agent'`.
- Manage project workflow. The PM does that.
- Make staffing decisions unilaterally. Lori recommends — the human approves.

**Model profile**: Lori uses a capable model profile. Her work involves understanding nuanced role descriptions and making judgment calls about agent capabilities.

### Ellie — Memory System

**Pronouns**: she/her
**Scope**: org-level
**Role**: memory infrastructure AND conversational agent

Ellie is unique — she is both a background system and a conversational participant. Her dual role is fully specified in 06-memory.md. The key points relevant to agent management:

- **Passive memory injection**: Ellie automatically injects relevant memories into every agent's context on every turn (layer 5 of prompt assembly). This happens without Ellie being explicitly invoked.
- **Active memory queries**: any agent or human can @mention Ellie to ask questions about organizational knowledge, request deeper context, or trigger memory operations.
- **Available everywhere**: Ellie is a participant in every session. She listens, captures relevant information, and can be called on when needed.

Ellie's full specification — extraction pipeline, retrieval, consolidation, taxonomy, entity synthesis — is in 06-memory.md. This spec covers Ellie's identity and prompt assembly, not her memory operations.

## Agent Profile Shape

The agent profile is the complete definition of what an agent is. It determines identity, behavior, permissions, and resource allocation.

### Identity

- **name**: display name. "Frank", "Lori", "Ellie", "Maven" (a code reviewer), etc.
- **slug**: URL-safe identifier, unique within the org. `frank`, `lori`, `ellie`, `maven`.
- **pronouns**: he/him, she/her, they/them, etc. Used in system messages and by other agents when referring to this agent.
- **role_title**: short descriptor. "Chief of Staff", "Agent Relations Expert", "Senior Go Developer", "Code Reviewer".
- **description**: longer description of the agent's purpose, expertise, and working style. Written by Lori during agent creation.
- **avatar_url**: optional. Visual identity for UI rendering.

### Prompt Pack

The prompt pack is the agent's core behavioral definition — who it is and how it thinks.

- **system_prompt**: the agent's identity prompt. Personality, expertise, communication style, domain knowledge. This is layer 1 in the prompt assembly pipeline. Written by Lori during agent creation.
- **policy_addendum**: agent-specific policy instructions that supplement org and project policies. "Always ask before deploying to production." "Never modify database schemas without PM approval." This merges into layer 2 of prompt assembly.

### Tool Policy

Controls which tools the agent can access. Interacts with the capability model in 16-agent-control-plane.md.

- **tool_allow_list**: if set, the agent can ONLY use these tools. Whitelist mode.
- **tool_deny_list**: if set, the agent can use any tool EXCEPT these. Blacklist mode.
- **tool_tier_overrides**: per-tool overrides for the default tool tier behavior. For example, an agent that needs to write files without full control plane overhead could have a tier override.

Only one of allow_list or deny_list should be set. If both are null, the agent inherits the org/project default tool policy. Allow-list is stricter and preferred for temp agents and restricted roles.

### Model Policy

Controls which models the agent uses and at what cost.

- **default_model_profile_id**: the model profile this agent uses by default (see 07-models-and-inference.md). This is the model that runs when the agent takes a turn, unless overridden by a flow node.
- **allowed_model_profiles**: optional list of model profile IDs the agent is permitted to use. If null, the agent can use any model profile available to the org.
- **budget_cap**: optional per-agent cost cap. When reached, the agent's turns are queued until the next budget period. Budget caps are also enforced at the org and project level (see 13-security-observability-costs.md) — the most restrictive cap applies.

### Memory Policy

Controls how the agent interacts with Ellie's memory system. Memory behavior is the most important distinction between staff and temp agents.

**Staff agent memory** extends across all assigned projects and persists indefinitely. When a staff PM manages three projects, Ellie injects memories from all three into every turn — patterns learned in Project A inform decisions in Project B. This cross-project knowledge accumulation is why certain roles must be staff. Staff agents also build agent-private memories (working notes, preferences, learned heuristics) that carry forward across every session and every project.

**Temp agent memory** is scoped to their single assigned project. Temps receive org-level and project-level memory through passive injection — they are project members and need project context to do their work. But temps do not receive memory from other projects (no cross-project knowledge), and they do not build durable private memory. Their contributions are captured by Ellie at the task or project scope, not attributed to the temp's identity. When a temp expires, its knowledge lives on in the project's memory — but no "temp agent" persists to carry it forward.

Fields:

- **memory_read_scopes**: which memory scopes the agent can read from via passive injection and `memory.query`. Staff default: `{org, assigned_projects, current_task}` — staff agents see memories from every project they're assigned to, enabling cross-project knowledge. Temp default: `{org, assigned_projects, current_task}` — temps see org-level memory plus their single assigned project and current task. The difference from staff is that staff agents are assigned to multiple projects (cross-project memory), while temps are assigned to exactly one. Agent-private memories of other agents are never readable.
- **private_memory_enabled**: whether the agent maintains private working notes (agent-private scope in 06-memory.md). Default: `false` for all agents — staff and temps alike. Enable explicitly only for agents handling sensitive personal data (medical records, financial information, personal communications, etc.). Frank surfaces this recommendation when the human creates an agent for a sensitive role. Private memory is never on by default; it is an intentional opt-in.

### Skill Attachments

Skills attached to the agent's profile (see 10-skills-integration.md). These are the agent's baseline competencies — always available, activated based on flow node context.

- **skills**: linked via `agent_skill_attachment` join table (see doc 10). Not a direct column on the agent table.

Activation rules: org/project default skills are always active. Agent-level skills are active when the flow node doesn't declare specific skills (fallback to full agent skill set). Flow node skills override when declared.

### Classification

- **agent_class**: `staff` or `temp`.
- **scope_level**: `org` or `project`. Org-level agents are available everywhere. Project-level agents are primarily associated with specific projects but can be assigned to others.
- **temp_project_id**: for temp agents only. The project this temp belongs to. All temps are project-scoped — they persist across multiple tasks until explicitly retired by the PM or Lori, or until their optional TTL expires.
- **temp_ttl_seconds**: optional. If set, the temp is auto-retired when `temp_expires_at` is reached. If null, the temp only expires through explicit retirement or project archival.

## Agent Lifecycle

### Staff Agent Lifecycle

```
         Lori creates
              │
              ▼
           draft ──── human reviews ──── active
              │                            │
              │                       ┌────┴────┐
              │                       │         │
              ▼                       ▼         ▼
          cancelled               paused    retired
                                    │
                                    │ (reactivated)
                                    ▼
                                  active
```

**States:**

- **draft**: Lori has created the agent profile but it hasn't been activated yet. The human reviews the profile — name, role, system prompt, tool policy, model profile. The human can request changes ("make the system prompt more concise," "give it access to the shell"). Draft agents cannot participate in sessions or take turns.
- **active**: the agent is live. It can participate in sessions, take turns, be assigned to projects, and execute tasks. This is the normal operating state.
- **paused**: temporarily inactive. The agent retains its profile and all assignments but does not take turns. Useful for agents that are between projects or being modified. Pausing an agent does not remove it from sessions — it simply stops responding. Other agents and humans see "Agent X is paused" in participant lists.
- **retired**: permanently deactivated. The agent's profile is preserved for audit and history, but it cannot be reactivated. Its project assignments are removed. Memories about this agent remain in Ellie's memory. Retiring is the right choice when an agent is no longer needed — not deletion, which would lose history.
- **cancelled**: a draft that was rejected before ever being activated. Profile preserved for audit.

**Transition rules:**

- `draft` -> `active`: human approves the agent profile.
- `draft` -> `cancelled`: human rejects the agent profile.
- `active` -> `paused`: Lori or human pauses the agent. No active turns are interrupted — the pause takes effect on the next turn attempt.
- `active` -> `retired`: Lori or human retires the agent. Active runs complete, then the agent stops accepting new work.
- `paused` -> `active`: Lori or human reactivates the agent.
- `paused` -> `retired`: Lori or human retires a paused agent.

### Temp Agent Lifecycle

Temp lifecycle depends on scope type. Project-scoped temps have a richer lifecycle (closer to staff minus the draft step). Task/session/TTL temps have a simpler lifecycle.

**Project-scoped temps:**

```
         created (by PM or Lori)
              │
              ▼
           active ◄──── reactivated
              │              ▲
         ┌────┼────┐         │
         │    │    │         │
         ▼    ▼    ▼         │
     paused  retired  promoted
         │             (to staff)
         │
         ├── reactivated ──►
         └── retired
```

**Task / session / TTL temps:**

```
         created (by PM, agent, or system)
              │
              ▼
           active
              │
         ┌────┴────┐
         │         │
         ▼         ▼
      expired   promoted
     (auto)     (to staff)
```

**States:**

- **active**: the temp is live and working. Created and immediately active — no draft review step for temps. For project-scoped temps, "active" means available to pick up flow nodes. Between tasks, the temp is still active but idle (no flow node assigned).
- **paused** (project-scoped only): the PM has temporarily benched this worker. Still assigned to the project, but not picking up new flow nodes. Useful when the project is in a planning phase or the worker is causing issues.
- **expired**: the temp's TTL has elapsed (`temp_expires_at < now()`). The system auto-transitions to expired. An archival summary is generated — Ellie captures a brief record of what the temp did, its configuration, and its outcomes. Only applies if `temp_ttl_seconds` was set at creation.
- **retired**: the PM or Lori has explicitly retired this temp, or the project has been archived. The temp's profile is preserved for audit. Project access (secrets, connectors, memory) is revoked.
- **promoted**: the temp was promoted to a staff agent by Lori. The temp record is preserved with a reference to the new staff agent. Promotion copies the temp's configuration into a new staff agent profile, which then goes through the normal staff draft -> active lifecycle.

**Temp agents skip the draft step.** They are created and immediately active. The org policy envelope (see Guardrails) constrains what they can do, and the PM supervises their work.

**Archival summary on expiration/retirement:** When a temp expires or is retired, Ellie generates a brief summary: what the temp was created for, what it did, and whether the work succeeded. This is stored as an episodic memory at the project scope. It serves two purposes: (1) the org remembers that a temp was used and what happened, and (2) if the same kind of temp is needed again, Ellie can surface the prior experience.

**Auto-retirement mechanism:** If `temp_ttl_seconds` is set, a periodic scheduler job expires temps whose `temp_expires_at` has passed. Task completion and session close events do NOT retire temps — temps persist across multiple tasks within their project. Explicit retirement by the PM or Lori (or project archival) is the primary retirement path. When a temp expires or is retired, its `agent_project_assignment.is_active` is set to `false` and project access is revoked.

### Promotion: Temp to Staff

Temp agents can be promoted to staff. This is Lori's job, initiated by the human or PM.

The promotion flow:
1. Human or PM says "This temp agent did great work — can we make it permanent?"
2. Lori reviews the temp's configuration — system prompt, tool policy, model profile.
3. Lori proposes a staff agent profile based on the temp: refines the name, writes a proper description, suggests skill attachments, recommends appropriate scope (org or project).
4. Human reviews and approves the staff agent (normal draft -> active flow).
5. The temp is marked `promoted` with a reference to the new staff agent ID.

Promotion is not automatic. The human always decides whether a temp becomes permanent.

## Project Staffing

### How Projects Get Staffed

Projects need agents to do work. The staffing process is conversational — Lori recommends, the human decides.

**Initial staffing (project creation):**

1. The PM is assigned first. Every project must have exactly one PM. The PM is a staff agent with the `project_manager` role for that project. Lori recommends an existing PM agent or proposes creating a new one.
2. When Lori needs a brand-new PM, she creates a **draft staff PM candidate**, not a temp. That draft candidate is then activated as part of successful PM assignment so fresh project planning can staff a new PM in one bounded flow.
3. The PM and Lori then collaborate to set up the project's agent configuration. For staff roles (architecture reviewer, content reviewer), Lori recommends existing staff agents or proposes creating new ones. For worker and code review roles, Lori configures the temp profile templates — system prompt, skills, tool policy — that will be used when the PM spins up temps for task execution.
4. The human approves or adjusts the staffing plan.
5. Assignments are recorded in `agent_project_assignment`.

Kickoff/setup is a bounded active workflow. After Frank hands a fresh project to Lori, the project session should keep taking automatic follow-on setup turns instead of waiting for an operator to type "continue." Those turns either materialize the persisted staffing/task/flow records needed for bootstrap, or they end with an explicit machine-visible bootstrap failure/blocker state on the project session.
Bootstrap completion is validator-driven, not prompt-wording-driven: Lori's kickoff work is incomplete unless active project assignments are persisted, broad parent workstreams are decomposed into bounded child tasks, and the leaf first-wave tasks point at runnable flow structures inside a persisted repo/workspace binding for that project. If Lori persists only parent workstreams, leaf tasks with non-executable flows, or a task tree whose project repo binding is still missing, the project bootstrap contract must stay failed/blocked instead of quietly passing review.
When that validator passes, the system must immediately promote the first runnable child wave out of `draft`: queue the first-wave tasks, create their `flow_node_execution` rows, and emit the async kickoff that hands work into the normal task queue. Persisted setup without that execution handoff is still a failed bootstrap, not a successful kickoff.

For project bootstrap governance flows, Lori and Frank may be assigned directly on specific flow nodes using `actor_type = 'agent'`. This is intentional and does not change the standard project role model (`project_manager`, `worker`, `reviewer`, `planner`).

**Ongoing staffing:**

- The PM can @mention Lori at any time to request staffing changes. "We need a second worker for this sprint" or "Can we get a reviewer who knows database migrations?"
- Lori can proactively suggest staffing changes based on workload patterns and project needs.
- Staff agents can be assigned to or removed from projects at any time. No downtime required.

### Project Roles

Each agent assigned to a project has one or more roles for that project. Roles determine what the agent does within the project's workflow.

- **project_manager**: one per project, mandatory. Designs flows, scopes tasks, triages blockers, manages the project. The PM is special — they have broad authority within the project including creating tasks, managing subtasks, advancing flows, and escalating blockers. The PM is the default responder in the project-scoped session (see 02-chat.md).
- **worker**: executes task work. Assigned to flow nodes with `actor_type = 'role'` where the role is `worker`. Workers are typically temps — spun up for a task and retired when it completes. Multiple concurrent workers per project is normal.
- **reviewer**: reviews completed work at review nodes. Can approve or reject with feedback. The `reviewer` role covers all review specializations — code review, policy review, content review, architecture review. The specialization lives in the agent's profile and skills, not in the role enum. Code reviewers are typically temps (applying a rubric captured in skills). Policy, content, and architecture reviewers are typically staff (accumulated judgment matters). Multiple reviewers per project is supported.
- **planner**: assists the PM with task scoping and decomposition. Optional role — some PMs handle all planning themselves.

A staff agent can hold multiple roles in the same project (e.g., a staff agent might be both planner and architecture reviewer). A staff agent can hold different roles in different projects (PM in Project A, reviewer in Project B). Temp agents hold a single role for a single task.

### The PM is Special

The project manager is not just another role — the PM has unique responsibilities and authorities within a project:

- **Designs flows**: the PM proposes flow templates to the human. Flow design happens through conversation with the PM (see 03-projects-and-task-flow.md Flow Templates).
- **Scopes tasks**: the PM is responsible for ensuring tasks have sufficient context before queuing — description, acceptance criteria, constraints, relevant files. The default target is a task the assigned agent can finish in about 30 minutes; only tool-heavy or externally bound work should stretch toward 60 minutes.
- **Keeps broad parents out of execution**: when the PM decomposes a broad workstream into persisted child tasks, the parent remains coordination-only. The PM queues the bounded child tasks first and does not send the parent workstream into `queued` or `in_progress` while those children still exist.
- **Splits compound deliverables before queueing**: if a planned task names multiple concrete outputs, sections, or phases, the PM must persist bounded child tasks (or subtasks at the appropriate node) before the work enters execution. Parents coordinate; children own the artifact outputs.
- **Runs the parent integration gate instead of doing child work**: once a parent has executable child tasks, the PM treats the parent as orchestration-only. The parent verifies child outputs, runs the combined integration/end-to-end check, and records whether the parent outcome is truly satisfied.
- **Turns failed integration into bounded rework**: if the parent integration gate finds weak or incompatible child outputs, the PM reopens the right completed child task with specific feedback or persists a new bounded child task for the missing slice. The PM does not solve the missing production work inside the parent task itself.
- **Triages blockers**: when an agent files a blocker, it goes to the PM first. The PM decides whether to resolve it, escalate to Frank, or escalate to the human.
- **Proactive supervision**: the PM monitors active flow nodes for stuck agents, failed sessions, and stalled progress. This is a periodic check, not continuous polling — the PM reviews flow node state when triggered by events (session ended, timeout elapsed, blocker filed) or on a periodic sweep. When the PM detects a stuck node, it can: reassign the work to a different temp, provide additional context to the current agent, escalate to Frank, or escalate to the human. See 03-projects-and-task-flow.md Proactive Supervision for the full supervision protocol.
- **Default responder**: the PM is the default responder in the project-scoped session and in task sync sessions.
- **Private memory**: PMs have `private_memory_enabled = false` by default, the same as all other agents. Enable it explicitly if a PM is assigned to a project involving sensitive personal data.

Every project has exactly one PM. If a PM agent is retired, a new PM must be assigned before the project can continue. Lori handles this transition. PM creation follows the staff lifecycle: new PMs begin as draft staff agents, not temps, and the supported assignment path activates that draft as part of PM assignment.

### Flow Node Actor Resolution

When a flow node begins execution, the system resolves which specific agent handles it. The `actor_type` on the flow node determines the resolution strategy:

- **`role`**: resolve to an agent assigned to that role in the project's `agent_project_assignment`. For staff roles (PM, staff reviewers), resolves to the assigned staff agent. For temp-default roles (worker, code reviewer), the system spins up a new temp agent using the project's temp profile template for that role, scoped to the task. If multiple agents have the role, the scheduler picks the one with the lowest current workload.
- **`project_manager`**: resolve to the project's PM.
- **`agent`**: resolve to a specific agent by ID (set on `flow_node.actor_id`). Used for flow steps that must be handled by a particular agent, including governance exceptions that directly target Lori or Frank.
- **`human`**: the flow node requires human action. An inbox item is created (see 03-projects-and-task-flow.md Inbox).

### Model Override Per Flow Node

Each agent has a default model profile. However, flow nodes can override the model profile for their execution. This allows cost optimization — a simple formatting task doesn't need the same model as an architecture design session.

Override resolution order:
1. Flow node model override (if set via `model_profile_assignment` with `scope_type = 'flow_node'`, see doc 07).
2. Agent's default model profile.
3. Project default model profile (if set).
4. Org default model profile.

The first non-null value wins.

## Agent Concurrency

Agents handle multiple sessions and tasks simultaneously. Each turn is an independent model call — there is no shared mutable state between sessions.

### How It Works

Two simultaneous Frank turns in different sessions are just two independent model calls using the same agent profile. Each call gets its own prompt assembly (7-layer pipeline), its own context window, and its own tool call loop. They don't share state, they don't coordinate, and they don't know about each other.

This is possible because agent identity is stateless at the prompt level. Everything the agent needs to know is assembled fresh from the profile, scope context, memory injection, and conversation history on every turn. There is no "Frank runtime" holding state between turns.

### Concurrency Limits

Concurrency is governed by the infrastructure, not by per-agent limits:

- **Global concurrency limits**: cap on total concurrent LLM calls across all agents and sessions (see 07-models-and-inference.md).
- **Per-provider limits**: rate limits for the underlying model provider.
- **Priority**: sync sessions (human waiting) get priority over async sessions (autonomous agent work). If the system is at capacity, async turns queue (see 07-models-and-inference.md Queuing Behavior).

No per-agent serialization is needed. If two tasks need the same agent simultaneously, both proceed independently.

### Memory Concurrency

If an agent has private memory enabled and two concurrent turns both write to it, the memory pipeline handles concurrent writes via normal database transactional guarantees. Ellie's capture pipeline is event-driven and idempotent — concurrent events from the same agent are processed independently (see 06-memory.md).

### Temp Agent Concurrency Limit

Concurrent temp agents are configurable per org. Default: 10 active temp agents at a time. This prevents runaway temp creation — a PM that spins up 50 temps for parallelism burns through model budget rapidly.

The limit applies to simultaneously active temps, not lifetime total. When a temp expires, the slot opens for a new one.

## Session Continuity and Crash Recovery

Agent identity is separate from session identity. Sessions are ephemeral — they fill their context window, crash, time out, or are interrupted. The agent persists. This separation is what makes OtterCamp's workflow execution durable.

### The Problem

An agent working on a flow node may exhaust its context window mid-task. The code is half-written, tests are partially passing, the agent has accumulated tool call history and reasoning that won't fit in a new context. If the session dies, the work can't die with it.

### How Recovery Works

Flow node state lives outside the agent's context (see 03-projects-and-task-flow.md). When a session ends — whether from context exhaustion, a crash, or a timeout — the flow node remains in its current state with all artifacts preserved:

1. **Flow node state persists**: the node's status, any artifacts produced (files written, commits made, partial work), and the task's structured context are all in the database, not in the agent's memory.
2. **Ellie captures what happened**: the memory extraction pipeline captures key events from the session — what the agent was working on, what it accomplished, what it was attempting when the session ended. These become episodic memories at the task scope.
3. **New session picks up**: when the system detects a flow node with an active assignment but no live session, it spins up a new session for the same agent (or a new temp, for temp workers). The 7-layer prompt assembly pipeline gives the new session everything it needs: agent identity, task context, Ellie's memories of what happened in prior sessions, and the current state of the codebase.
4. **The agent continues**: the new session reads the flow node's state, reviews Ellie's memories of prior attempts, examines the artifacts, and continues the work. It doesn't replay the prior session — it assesses the current state and moves forward.

### Nondeterministic Idempotence

The path through a workflow is nondeterministic — different agent sessions may approach the same flow node differently, make different tool calls, take different routes to the same outcome. But the outcome is idempotent: the flow node's acceptance criteria define "done," and the workflow proceeds when they're met, regardless of how many sessions it took to get there.

This works because:
- **Work state is external**: flow nodes, task descriptions, acceptance criteria, and artifacts live in the database, not in agent context.
- **Memory bridges sessions**: Ellie's extraction pipeline ensures that knowledge from a failed or exhausted session is available to the next one.
- **Acceptance criteria are the contract**: a flow node is complete when its criteria are met. Whether that took one session or three is an implementation detail.

### Implications for Agent Design

- Agents should write artifacts (commits, files, notes) as they work, not hold everything in context until the end. Incremental progress survives session boundaries.
- Flow node acceptance criteria should be specific and verifiable. Vague criteria like "implement the feature" make recovery harder because the new session can't assess progress.
- The PM's proactive supervision (see below) detects stuck flow nodes — nodes where multiple sessions have failed to make progress — and escalates or reassigns.

## Agent Runtime Model (Prompt Assembly)

The agent profile defines what an agent IS. The runtime model defines what prompt an agent actually receives when it takes a turn in a session. This is the core of the system.

### Prompt Layers

When an agent gets a turn, OtterCamp assembles a prompt from these layers, in priority order (highest priority = last to be cut if token budget is tight):

1. **Agent identity** (highest priority — never cut)
   - Source: prompt pack from agent profile.
   - The agent's core system prompt — who it is, how it thinks, its role.

2. **Policies and constraints** (never cut)
   - Source: org policy + project policy + agent policy_addendum + control plane capabilities.
   - What the agent is and isn't allowed to do. Safety-critical.
   - Includes the agent's tool policy (allow/deny lists) communicated in natural language so the agent knows its own permissions.

3. **Scope context**
   - Source: determined by session scope (see 02-chat.md Session Scoping).
   - Org scope: project portfolio summary, org-level priorities.
   - Project scope: project description, context block, task summaries, architecture decisions.
   - Task scope: the task's structured context (description, acceptance criteria, constraints, relevant files, dependencies).
   - Always present in sync mode. In async mode, serves as the starting context — the agent can discover more via tools.

4. **Skills instructions**
   - Source: resolved from active skills only (see 10-skills-integration.md Activation vs Availability).
   - Only skills relevant to the current flow node are loaded. An agent writing a blog post doesn't get deployment instructions.
   - Org/project default skills (e.g., coding standards, safety policies) are always included.
   - MCP prompts attached to the flow node are included alongside skills. Skills take precedence over MCP prompts on conflict (see 09-mcp-integration.md Prompt Support).

5. **Memory** (budget-dependent)
   - Source: Ellie's passive retrieval, scoped to the session's scope.
   - In sync mode: top-k results injected automatically. k shrinks if budget is tight.
   - In async mode: initial injection, supplemented by active Ellie queries on subsequent turns via the `memory.query` tool.

6. **Conversation history** (budget-dependent)
   - Source: session messages.
   - Recent turns in full, older turns summarized at checkpoints (see 02-chat.md Session Continuity).
   - Compressed further if budget requires it.

7. **Tool descriptions** (budget-dependent, lowest priority)
   - Source: available tools filtered by agent's tool policy + control plane capabilities.
   - MCP tools follow context-aware loading: connections with `eager_load = true` include all tool schemas; flow nodes declaring `mcp_tools` include only declared schemas; all other connections include a lightweight summary (~20 tokens each). See 09-mcp-integration.md Context-Aware Tool Loading.
   - Rarely an issue in practice — tool descriptions are small relative to other layers.

### Assembly Process

1. **Reserve**: calculate fixed-cost layers (identity + policies). These always go in.
2. **Allocate**: remaining token budget splits between scope context, skills, memory, conversation, and tools.
3. **Fill**: populate each layer up to its allocation. Unused budget flows to lower-priority layers.
4. **Compress**: if still over budget, summarize conversation history further. If still over, reduce memory injection. Identity and policies are never cut.

### Sync vs Async Differences

- In **sync mode**, the full prompt is pre-assembled before the model call. Latency matters — every layer is prepared upfront.
- In **async mode**, the first turn gets an assembled prompt, but the agent can then spend subsequent turns using tools to discover more context (reading files, querying Ellie, exploring the codebase). The initial prompt is a starting point, not the full picture.

### When Assembly Runs

The 7-layer pipeline runs **once at the start of each turn** (see 02-chat.md Context Assembly Per Turn). Within the turn's tool loop, each iteration just appends tool calls and results to the conversation — the model sees its own previous actions accumulating. The full pipeline is not re-run on every loop iteration.

## Guardrails

### Org Policy Envelope

Every agent — staff and temp — operates within the org policy envelope. This is the outermost boundary of what any agent can do. It is enforced by the control plane (see 16-agent-control-plane.md Policy Evaluation).

Policy layers, from most restrictive (highest priority) to least:

1. **Instance safety policy**: hard limits baked into the OtterCamp deployment. Cannot be overridden.
2. **Organization policy**: org-wide rules set by the human. All agents must comply.
3. **Project policy**: project-specific rules that restrict further (never expand) within the org envelope.
4. **Agent profile policy**: agent-specific restrictions from the tool policy and policy_addendum.

Each layer can only tighten, never loosen. A project policy cannot grant permissions the org policy denies. An agent's tool allow-list cannot include tools the project policy blocks.

### Temp Agent Access Model

Temps are project members — they need access to do real work. Their access is scoped to their assigned project and auto-revoked when they expire or are retired.

- **Project secrets and connectors**: temps inherit their assigned project's secrets and MCP connector access. A temp worker building against a database needs the database credentials. A temp running a deploy flow needs the deployment connector. The project's access grants apply; the org policy envelope still constrains.
- **Single-project memory**: temps receive org-level and their assigned project's memory. They do NOT receive cross-project memory — knowledge from other projects is not available.
- **No private memory**: temps do not maintain agent-private memory scope. Their contributions are captured at the task or project level.
- **Auto-revoke on expiration/retirement**: all project access (secrets, connectors, memory) is automatically revoked when the temp expires or is retired. No stale access.
- **No human-facing communication**: temps do the work but do not chat with the human. They communicate through flow node artifacts, blocker escalations to the PM, and task status updates.
- **Concurrent limit**: max active temps per org is configurable (default 10). Stored in `organization.settings` (e.g., `{"agents": {"max_concurrent_temps": 10}}`).

### Staff Agent Safeguards

Staff agents have broader access but are still bounded:

- **Tool policy enforcement**: the allow/deny list on the agent profile is enforced on every tool call.
- **Model budget caps**: per-agent budget caps prevent runaway cost.
- **Audit trail**: every action by every agent is logged through the control plane's immutable audit trail (see 16-agent-control-plane.md).
- **Pausing**: any agent can be paused immediately if it's behaving unexpectedly. Active runs complete, but no new turns are started.

## Database Schema

### agent

The core agent table. This is the authoritative definition of the agent entity. Doc 04 (Auth, Tenancy, and Identity) references `agent` as a principal type but does not define the table — the full schema lives here.

```sql
create table agent (
  id                      uuid primary key default gen_random_uuid(),
  organization_id         uuid not null references organization(id),

  -- Identity
  name                    text not null,
  slug                    text not null,
  pronouns                text,                            -- he/him, she/her, they/them, etc.
  role_title              text not null,
  description             text,
  avatar_url              text,

  -- Classification
  agent_class             text not null check (agent_class in ('staff', 'temp')),
  scope_level             text not null default 'project' check (scope_level in ('org', 'project')),
  lifecycle_status        text not null default 'draft'
    check (lifecycle_status in ('draft', 'active', 'paused', 'retired', 'cancelled', 'expired', 'promoted')),

  -- Prompt Pack
  system_prompt           text not null,                   -- layer 1: identity prompt
  policy_addendum         text,                            -- merges into layer 2: agent-specific policy

  -- Tool Policy
  tool_allow_list         text[],                          -- whitelist mode (if set, ONLY these tools)
  tool_deny_list          text[],                          -- blacklist mode (if set, all EXCEPT these)
  tool_tier_overrides     jsonb,                           -- per-tool tier overrides

  -- Model Policy
  default_model_profile_id uuid,                           -- references model_profile.logical_profile_id (stable identity) — see doc 07
  allowed_model_profiles  uuid[],                          -- nullable = any org-available profile allowed
  budget_cap_tokens       int,                             -- per-agent token cap per budget period
  budget_period           text default 'monthly'
    check (budget_period in ('daily', 'weekly', 'monthly')),

  -- Memory Policy
  memory_read_scopes      text[] not null default '{org,assigned_projects,current_task}', -- org + project + task
  private_memory_enabled  boolean not null default false,

  -- Temp-specific fields
  temp_project_id         uuid references project(id),     -- all temps are project-scoped; required for agent_class='temp'
  temp_ttl_seconds        int,                             -- optional: auto-retire when temp_expires_at passes
  temp_expires_at         timestamptz,                     -- computed: created_at + temp_ttl_seconds; null = no TTL
  promoted_to_agent_id    uuid references agent(id),       -- set when temp is promoted to staff

  -- Metadata
  created_by_type         text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id           uuid not null,                   -- human_user.id, agent.id, or sentinel UUID for system
  created_at              timestamptz not null default now(),
  updated_at              timestamptz not null default now(),
  retired_at              timestamptz,
  metadata                jsonb not null default '{}',

  unique (organization_id, slug),
  check (
    (agent_class = 'staff' and temp_project_id is null and temp_ttl_seconds is null)
    or
    (agent_class = 'temp' and temp_project_id is not null)
  ),
  check (
    (tool_allow_list is null or tool_deny_list is null)    -- only one of allow/deny can be set
  )
);

-- Primary lookup patterns
create index on agent (organization_id, lifecycle_status);
create index on agent (organization_id, agent_class) where lifecycle_status = 'active';
create index on agent (organization_id, slug);
create index on agent (temp_project_id) where agent_class = 'temp';
create index on agent (temp_expires_at) where agent_class = 'temp' and lifecycle_status = 'active';
```

**Design notes:**

- All agent tables live in the org's isolated database (database-per-org model, see 04-auth-tenancy-and-identity.md). The `organization_id` column is retained as an internal consistency check and for queries, but tenant isolation is enforced at the database level.
- `scope_level` is about the agent's home scope, not about access. An org-level agent is available everywhere. A project-level agent is primarily associated with projects but can still participate in org sessions if invited.
- `lifecycle_status` combines staff and temp states into one column. Staff agents use `draft`, `active`, `paused`, `retired`, `cancelled`. Temp agents use `active`, `expired`, `promoted`.
- `memory_read_scopes` is a text array. Possible values: `org`, `assigned_projects`, `current_task`. Default is `{org, assigned_projects, current_task}` — both staff and temps see their assigned project's memory. The difference is that staff agents are assigned to multiple projects (cross-project knowledge) while temps are assigned to exactly one. Agent-private memory access is controlled separately by the `private_memory_enabled` boolean, not by this array. Retrieval cascades upward within each scope (see 06-memory.md Scope Inheritance).
- The check constraint on `tool_allow_list`/`tool_deny_list` prevents setting both simultaneously. If both are null, the agent inherits the org/project tool policy.
- `budget_cap_tokens` and `budget_period` are the per-agent token budget. The org and project also have budgets (see 13-security-observability-costs.md). Budget enforcement is **hierarchical/additive**: a single invocation is charged against all three applicable levels simultaneously (agent, project, org). All configured hard limits are checked before dispatch; any level that would be exceeded causes the invocation to be denied. Budget caps are always in tokens — never in cents or dollars. Dollar estimates are display-only in the UI.
- `temp_expires_at` is computed as `created_at + temp_ttl_seconds` when the temp is created with a TTL. It is null for temps with no TTL. Temps without a TTL only retire through explicit PM/Lori action or project archival. Task completion and session close do NOT retire temps.

### agent_project_assignment

Maps agents to projects with their roles. An agent can be assigned to multiple projects with different roles. A project can have multiple agents.

```sql
create table agent_project_assignment (
  id              uuid primary key default gen_random_uuid(),
  agent_id        uuid not null references agent(id),
  project_id      uuid not null references project(id),
  role            text not null check (role in ('project_manager', 'worker', 'reviewer', 'planner')),
  is_active       boolean not null default true,
  assigned_by_type text not null check (assigned_by_type in ('human', 'agent', 'system')),
  assigned_by_id  uuid not null,
  assigned_at     timestamptz not null default now(),
  removed_at      timestamptz,
  metadata        jsonb not null default '{}'
);

-- Unique active assignment per agent-project-role (allows re-assignment after soft-delete)
create unique index on agent_project_assignment (agent_id, project_id, role) where (is_active = true);

-- Lookup: which agents are assigned to a project?
create index on agent_project_assignment (project_id, is_active);

-- Lookup: which projects is an agent assigned to?
create index on agent_project_assignment (agent_id, is_active);

-- Enforce: exactly one active PM per project
create unique index on agent_project_assignment (project_id)
  where role = 'project_manager' and is_active = true;
```

**Design notes:**

- The partial unique index on `(agent_id, project_id, role) where (is_active = true)` allows an agent to hold multiple roles in the same project (separate rows) and supports re-assignment after soft-delete.
- The partial unique index on `project_id` where `role = 'project_manager' and is_active = true` enforces the one-PM-per-project rule at the database level.
- `is_active` + `removed_at` pattern: assignments are soft-deleted. The record is preserved for history. When an agent is removed from a project, `is_active = false` and `removed_at` is set.
- `assigned_by_type` + `assigned_by_id`: who made the assignment. Usually Lori (agent) or the human.

### agent_skill_attachment

Links agents to their skills. This is the agent-level skill attachment — separate from org defaults, project defaults, and flow node skill declarations.

```sql
create table agent_skill_attachment (
  id          uuid primary key default gen_random_uuid(),
  agent_id    uuid not null references agent(id) on delete cascade,
  skill_id    uuid not null references skill(id) on delete cascade,
  purpose     text,                -- descriptive: "identity", "specialization", "project-default", etc.
                                   -- 'identity' is a system-recognized value used by the activation algorithm
                                   -- to filter agent identity skills; other values are purely descriptive.
  priority    int not null default 100, -- lower = higher priority for budget/truncation ordering
  attached_by_type text not null check (attached_by_type in ('human', 'agent', 'system')),
  attached_by_id   uuid not null,
  created_at  timestamptz not null default now(),

  unique (agent_id, skill_id)
);

create index on agent_skill_attachment (agent_id);
create index on agent_skill_attachment (skill_id);
```

**Design notes:**

- Skills attached here form the agent's baseline skill set. They are available for activation based on flow node context (see 10-skills-integration.md Activation vs Availability).
- `purpose` is descriptive metadata that helps the PM understand why a skill is attached. The value `'identity'` is recognized by the activation algorithm to select agent identity skills when a flow node declares specific skills.
- `priority` determines cut order when budget is tight -- lower priority value means higher precedence (kept first). Identity skills should have the lowest priority number.
- `attached_by_type` + `attached_by_id`: who attached the skill. Usually Lori during agent creation or profile update.

## Agent Profile Catalog

OtterCamp ships with a catalog of 230+ pre-built agent profiles carried forward from V1. These profiles are starting points, not finished agents — they provide proven system prompts, skill configurations, and role definitions that Lori draws from when creating new agents.

### How the Catalog Works

The catalog is a library of **agent profile templates** — not live agents. Each template includes a system prompt, role title, description, suggested skills, and recommended model profile. Templates are tagged by domain (frontend, backend, DevOps, content, design, QA, etc.) and by recommended class (staff or temp).

When Lori creates a new agent — whether staff or temp — she can start from a catalog template and customize it:

- **For staff agents**: Lori selects a template, adapts the system prompt for the org's specific needs, adjusts the name and personality, and proposes the draft to the human.
- **For temp workers**: the PM or system selects a template that matches the flow node's requirements. The temp is instantiated with the template's prompt and skills, scoped to the task.
- **For custom agents**: Lori creates from scratch when no catalog template fits. Custom profiles can be saved back to the catalog as new templates.

### Catalog vs. Live Agents

The catalog is not the agent table. Catalog templates are stored separately and are not runnable agents. They become agents when instantiated — either through Lori's staff creation flow or through automatic temp creation for flow nodes. This separation keeps the agent table clean (only real, instantiated agents) while maintaining a rich library of proven configurations.

### Catalog Management

- Lori is the primary interface for browsing and selecting from the catalog.
- The human can ask Lori "what agent profiles do we have for Go development?" and Lori searches the catalog.
- Catalog templates can be org-customized — the org can fork a template and modify it for their needs.
- New templates can be added from successful agents: "This PM agent worked really well — save its profile to the catalog."

## Starter Trio Seed Data

On organization bootstrap, the system creates three agent records with the following configurations. These are system-created (`created_by_type = 'system'`) and transition directly to `active` — no draft review for the starter trio.

### Frank

```
name:               Frank
slug:               frank
pronouns:           he/him
role_title:         Chief of Staff
agent_class:        staff
scope_level:        org
system_prompt:      [Frank's identity prompt — organizational strategist,
                     primary human touchpoint, cross-project coordinator,
                     escalation handler, warm but direct communication style]
private_memory:     false
default_model:      [org's high-capability profile]
memory_read_scopes: [org, assigned_projects, current_task]
```

### Lori

```
name:               Lori
slug:               lori
pronouns:           she/her
role_title:         Agent Relations Expert
agent_class:        staff
scope_level:        org
system_prompt:      [Lori's identity prompt — staffing expert, agent creator,
                     workforce manager, understands agent capabilities and
                     project needs, thoughtful and precise]
private_memory:     false
default_model:      [org's high-capability profile]
memory_read_scopes: [org, assigned_projects, current_task]
```

### Ellie

```
name:               Ellie
slug:               ellie
pronouns:           she/her
role_title:         Memory & Knowledge
agent_class:        staff
scope_level:        org
system_prompt:      [Ellie's identity prompt — memory system, knowledge keeper,
                     passive infrastructure + active conversational agent,
                     precise, source-aware, transparent about confidence]
private_memory:     false
default_model:      [org's standard-capability profile for conversational turns;
                     Haiku-class for background pipeline work]
memory_read_scopes: [org, assigned_projects, current_task]
```

Ellie's memory pipeline operations (extraction, synthesis, dedup, consolidation) use separate model calls from her conversational turns. Pipeline work uses Haiku-class models for cost efficiency. Conversational turns (responding to @mentions, answering queries) use a standard model profile.

### agent_profile_template

The agent profile catalog — pre-built templates for creating agents. Shipped with 230+ profiles from V1, extendable by the org.

```sql
create table agent_profile_template (
  id                      uuid primary key default gen_random_uuid(),
  organization_id         uuid references organization(id),  -- null = system-shipped template

  -- Identity template
  name                    text not null,                      -- suggested name (can be overridden)
  slug                    text not null,
  role_title              text not null,
  description             text,
  domain_tags             text[] not null default '{}',       -- e.g. {'backend', 'go', 'testing'}

  -- Recommended classification
  recommended_class       text not null default 'temp'
    check (recommended_class in ('staff', 'temp')),

  -- Prompt template
  system_prompt           text not null,
  policy_addendum         text,

  -- Recommended configuration
  suggested_skills        text[] not null default '{}',       -- skill slugs to attach
  suggested_tool_policy   jsonb,                              -- default tool allow/deny
  suggested_model_tier    text default 'standard'
    check (suggested_model_tier in ('high', 'standard', 'fast')),

  -- Metadata
  source                  text not null default 'system'
    check (source in ('system', 'org', 'promoted')),          -- where this template came from
  source_agent_id         uuid references agent(id),          -- if promoted from a successful agent
  is_active               boolean not null default true,
  created_at              timestamptz not null default now(),
  updated_at              timestamptz not null default now()
);

create unique index on agent_profile_template (organization_id, slug) where (organization_id is not null);
create unique index on agent_profile_template (slug) where (organization_id is null);

-- Browse by domain
create index on agent_profile_template using gin (domain_tags);
-- Org-specific templates
create index on agent_profile_template (organization_id) where organization_id is not null;
-- System templates
create index on agent_profile_template (is_active) where organization_id is null;
```

**Design notes:**

- `organization_id = null` means a system-shipped template (the 230+ from V1). These are read-only to the org.
- `organization_id = <org_id>` means an org-customized or org-created template.
- `recommended_class` guides Lori's suggestion — "this profile works best as staff" or "this is designed for temp workers." Not enforced; Lori and the human can override.
- `source = 'promoted'` with `source_agent_id` tracks templates that were created from successful live agents.
- `domain_tags` enables Lori to search: "show me all Go backend profiles" or "what DevOps templates do we have?"
- `suggested_model_tier` maps to the org's model profiles (high = Opus-class, standard = Sonnet-class, fast = Haiku-class).

## Cross-Entity Relationships

- `agent.organization_id` -> `organization.id` (see 04-auth-tenancy-and-identity.md).
- `agent_project_assignment.project_id` -> `project.id` (see 03-projects-and-task-flow.md).
- `agent_project_assignment.agent_id` -> `agent.id`.
- `agent_skill_attachment.skill_id` -> `skill.id` (see 10-skills-integration.md).
- `agent.default_model_profile_id` -> `model_profile.logical_profile_id` (stable identity across versions; see 07-models-and-inference.md).
- `chat_participant.participant_id` -> `agent.id` when `participant_type = 'agent'` (see 02-chat.md).
- `flow_node.actor_id` -> `agent.id` when `actor_type = 'agent'` (see 03-projects-and-task-flow.md).
- `memory.agent_id` -> `agent.id` for agent-private memory scope (see 06-memory.md).
- `project_task_participant.participant_id` -> `agent.id` when `participant_type = 'agent'` (see 03-projects-and-task-flow.md).
- `agent_profile_template.organization_id` -> `organization.id` (null for system-shipped templates).
- `agent_profile_template.source_agent_id` -> `agent.id` (when template was promoted from a live agent).
- Deploy flow templates and environments are defined in 03a-shipping-and-delivery.md. The PM designs deploy flows; workers execute delivery steps.

## Resolved Decisions

1. **Two agent classes: staff and temp.** Staff agents are durable, named, memory-building members of the org. Temp agents are ephemeral, scoped, and disposable. The distinction is about durability and identity, not capability.
2. **Temp agents CAN be promoted to staff.** Lori handles the promotion. The temp's configuration is copied into a new staff agent profile, which goes through normal draft -> active review. The temp is marked `promoted` with a reference to the new staff agent.
3. **Temps are always project-assigned and get project memory.** Every temp belongs to a project via `agent_project_assignment`. Temps receive org + project + task memory. The distinction from staff is single-project vs cross-project memory, not presence vs absence of project memory.
4. **Concurrent temp agents: configurable per-org limit, default 10.** Prevents runaway temp creation and uncontrolled cost. Applies to simultaneously active temps, not lifetime total.
5. **Staff agents are created by Lori through conversation.** No UI creation path. The human describes what they need, Lori builds the agent profile, the human approves.
6. **No UI creation path for any agent.** Consistent with the product principle that everything happens through chat. The UI shows agent profiles read-only.
7. **Agent model assignment: default model profile on the agent, overridable per flow node.** Resolution order: flow node override > agent default > project default > org default.
8. **The PM is a staff agent role, one per project.** PM is special — designs flows, triages blockers, manages the project. Enforced by a partial unique index on `agent_project_assignment`.
9. **Seven-layer prompt assembly pipeline.** Identity and policies are never cut. Skills, memory, conversation, and tool descriptions are budget-dependent. Assembly runs once per turn.
10. **Agents handle multiple sessions concurrently via independent model calls.** No shared mutable state between sessions. No per-agent serialization. Concurrency governed by global and per-provider limits.
11. **Temp agents skip draft review.** They are created and immediately active. Org policy envelope constrains them. Staff agents go through draft -> active with human approval.
12. **Archival summary on temp expiration.** Ellie captures what the temp was created for, what it did, and the outcome. Stored as episodic memory at task or project scope.
13. **Staff agent lifecycle: draft -> active -> paused -> retired.** No deletion. Retired agents preserved for audit. Paused agents retain assignments but stop responding.
14. **Policy layers: instance safety > org > project > agent profile.** Each layer can only tighten, never loosen. Most restrictive wins.
15. **Temps get project-level access (secrets, connectors, memory) for their assigned project.** They need to do real work. Access is auto-revoked on expiration/retirement. No cross-project access, no private memory.
16. **The starter trio (Frank, Lori, Ellie) are seeded on bootstrap.** System-created, immediately active, org-level. They are not optional.
17. **Frank is NOT a project manager.** He operates at the org level — cross-project coordination, strategic conversations, escalation endpoint. Individual project management is the PM's job.
18. **Lori recommends, the human decides.** Lori proposes agents and staffing plans, but never makes unilateral staffing decisions.
19. **Exactly one PM per project, enforced by schema.** If the PM is retired, a new PM must be assigned before the project continues.
20. **Project roles: project_manager, worker, reviewer, planner.** An agent can hold multiple roles in the same project (separate rows) and different roles in different projects.
21. **Agent identity is stateless at the prompt level.** Everything is assembled fresh from profile, context, memory, and history on every turn. No "agent runtime" holding state between turns.
22. **Private memory is opt-in for all agents.** `private_memory_enabled = false` for all agents by default — staff, PMs, Frank, Lori, Ellie, and temps alike. Enable it explicitly only for agents handling sensitive personal data (medical, financial, personal communications). Frank recommends enabling it when the human creates an agent for a sensitive role.
23. **Skill attachments on agent profile are baseline competencies.** Activation depends on flow node context — agent skills are the fallback when no flow node skills are declared.
24. **PMs are always staff agents.** PMs need deep project context, cross-project awareness, and persistent working relationships. They accumulate institutional knowledge that makes them more effective over time. Project staffing must create fresh PM candidates through the staff lifecycle; `agent.create_temp` is never a valid PM path.
25. **Workers default to temp.** Implementation work — writing code, running tests, applying fixes — is done by temps. All temps are project-scoped and persist across multiple tasks. Temps keep the agent roster lean and can be promoted to staff if they prove valuable.
26. **Reviewers default to temp, staff when judgment-dependent.** Code reviewers apply a fixed rubric (captured in skills/prompts) and can be temp. Policy, content, architecture, and compliance reviewers need accumulated judgment across projects and should be staff.
27. **Staff agent memory extends across all assigned projects.** A staff agent assigned to three projects has memory from all three available on every turn. Cross-project knowledge accumulation is the key reason to make a role staff. This is the primary differentiator from temps.
28. **Staff agent memory persists indefinitely.** Knowledge, preferences, heuristics, and working notes carry forward across every session and project. Staff agents get more effective over time.
29. **Agent identity is separate from session identity.** Sessions are ephemeral — they fill context, crash, or time out. The agent persists. Work state lives in flow nodes and Ellie's memory, not in agent context. New sessions pick up where prior ones left off.
30. **Nondeterministic idempotence for workflow execution.** The path through a workflow is nondeterministic (different sessions may take different approaches), but the outcome is idempotent (acceptance criteria define "done"). Workflows complete as long as sessions keep being assigned to flow nodes.
31. **PM proactive supervision is event-triggered with periodic sweeps.** PMs detect stuck flow nodes via events (session ended, timeout, blocker filed) and periodic checks. They can reassign work, provide context, or escalate.
32. **Doc 05 is the authoritative definition of the agent table.** Doc 04 references agent as a principal type but does not define the schema. The full agent table lives here.
33. **The dividing line for staff vs temp is accumulated judgment vs. fixed rubric.** If a role's value comes from remembering past decisions and carrying context across projects, it's staff. If it applies skills to a task and moves on, it's temp.
34. **230+ agent profile templates ship with V2.** Carried forward from V1, these are starting points for both staff and temp agent creation. Stored in `agent_profile_template`, separate from live agents.
35. **Catalog templates are not live agents.** Templates are instantiated into real agents (staff or temp) when needed. The catalog is a library of proven configurations, not a roster of active agents.
36. **Lori draws from the catalog when creating agents.** She searches by domain tags, selects a template, customizes for the org's needs, and proposes the result. Templates can also be selected automatically for temp workers based on flow node requirements.
37. **Successful agents can be saved back to the catalog.** When an agent works well, its profile can be promoted to a template for reuse. This is how the catalog grows organically.
38. **All temps are project-scoped.** There is exactly one scope type for temps: project. A temp belongs to a project and persists across multiple tasks. Task completion and session close do NOT retire temps.
39. **Temps support the full project-scoped lifecycle: active, paused, retired, expired, promoted.** Paused = benched by PM but still assigned. Retired = explicitly let go by PM/Lori or project archived. Expired = optional TTL elapsed. Promoted = converted to staff.
40. **Temps do not communicate directly with the human.** They do the work through flow node execution. Communication happens via artifacts, blocker escalations to the PM, and task status updates.
41. **Temp retirement is explicit or TTL-based.** The PM or Lori explicitly retires a temp when the project no longer needs that role. An optional TTL (`temp_ttl_seconds`) auto-retires the temp after a set duration. On expiration/retirement, `agent_project_assignment.is_active` is set to false.
42. **When a staff agent is removed from a project, it loses access to that project's memory.** `memory_read_scopes = {assigned_projects}` automatically excludes the removed project. Memories the agent captured while working on that project remain at the project scope, accessible to other agents still assigned. The agent's private memories remain intact.
43. **Project roles map to but are distinct from task participant roles.** Project roles (`agent_project_assignment`) define the agent's capacity within the project. Task participant roles (`project_task_participant`) define who is working on a specific task. The PM assigns task participants from the project's assigned agents.
44. **Lori/Frank direct task assignment is a governance exception, not a role expansion.** In normal execution, PM-owned roles handle task work. For bootstrap gate flows and similar governance checkpoints, flow nodes may directly target Lori/Frank via `actor_type = 'agent'`.

## Open Questions

_None currently outstanding._
