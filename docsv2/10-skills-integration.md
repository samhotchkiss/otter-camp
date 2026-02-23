---
## Summary

Skills are the mechanism by which agents in OtterCamp receive behavioral instructions for specific types of work. A skill is simply a markdown file with YAML frontmatter (metadata like name, slug, scope, category) and a markdown body (the actual instructions). Skills are not code -- they contain no executable logic, no templating, no imports. They are documents that get injected into an agent's prompt at runtime. Examples include coding standards, review checklists, API design conventions, and agent identity definitions (e.g., what makes Frank "Frank"). Skills live as files in git repos: org-level skills in a dedicated org skills repo, project-level skills in the project's repo, both under a `skills/` directory.

Skills attach at four levels of specificity: org defaults (apply everywhere), project defaults (apply project-wide), agent-level (define an agent's identity and core competencies), and flow node-level (activated only for a specific workflow step). The activation system is designed for token budget efficiency -- when a flow node declares specific skills, only those (plus defaults and agent identity) are loaded, keeping prompts focused. When no node-level skills are declared, the agent gets its full skill set as a fallback. Conflict resolution follows specificity: flow node skills override agent-level, which override project defaults, which override org defaults. Skills always take precedence over procedural memory (from the memory system), which is advisory rather than prescriptive.

The database schema consists of three tables: `skill` (the registry, pointing to file paths in repos), `agent_skill` (links agents to their profile-level skills with priority ordering), and `flow_node_skill` (links flow template nodes to required skills). Skill content is read from the git repo at prompt assembly time -- not cached in the database -- so changes take effect on the next turn after commit. Skills occupy layer 4 of the 7-layer prompt assembly pipeline, between scope context and memory. The PM agent manages all skill CRUD through conversation and git commits; there is no skill editor UI. V2 ships with bootstrap skills including identity skills for the starter trio (Frank, Lori, Ellie), a PM identity skill, org-wide safety/communication policies, and a library of project template skills available for adoption.

---

# 10. Skills Integration

## Goal

Skills are markdown instruction documents that get loaded into an agent's prompt when relevant. They shape how the agent behaves for a specific type of work.

Skills are simple by design. They are not code packages, they don't have scripts or hooks, and they don't need signing or sandboxing. They're documents. A skill is a markdown file with YAML frontmatter for metadata and markdown content for instructions. This can be refined as we learn from practice.

## What a Skill Is

A skill is a markdown file containing instructions for a specific type of work. Examples:

- "Blog Writing Guidelines" -- voice, structure, length, style rules.
- "Go Coding Standards" -- conventions, error handling patterns, testing expectations.
- "Code Review Checklist" -- what to look for, how to give feedback.
- "API Design Conventions" -- naming, versioning, error response format.
- "Frank Identity" -- who Frank is, how he talks, his role and responsibilities.
- "PM Planning" -- how the project manager scopes tasks, designs flows, triages blockers.

A skill has:

- **Metadata**: name, slug, scope, author, category. Encoded as YAML frontmatter.
- **Content**: the markdown instructions themselves. This is what gets loaded into the agent's prompt.

That's it. No executable code. No templating. No conditional logic. No imports or inheritance. A skill is a document that tells an agent how to do something.

## Skill Format

Skills are plain markdown files with YAML frontmatter. The frontmatter carries metadata; the body carries instructions.

```markdown
---
name: Go Coding Standards
slug: go-coding-standards
scope: project
category: engineering
description: Conventions and patterns for Go code in this project.
---

## Error Handling

Always return errors rather than panicking. Use `fmt.Errorf` with `%w` for wrapping.
Never silently discard errors. If an error is intentionally ignored, add a comment
explaining why.

## Naming

Use camelCase for unexported identifiers, PascalCase for exported.
Package names are lowercase, single-word. No underscores.

## Testing

Every exported function has at least one test. Table-driven tests for functions
with multiple input cases. Test files live alongside the code they test.

## Dependencies

Prefer stdlib over third-party packages. If a third-party package is needed,
justify it in the PR description. No vendoring -- use Go modules.
```

### Frontmatter Fields

| Field | Required | Type | Description |
|---|---|---|---|
| `name` | yes | string | Human-readable name. Displayed in skill catalog and management UI. |
| `slug` | yes | string | URL-safe identifier, unique within its scope (org or project). Used for references in flow nodes and agent profiles. |
| `scope` | yes | string | `org` or `project`. Where this skill lives and how broadly it applies. |
| `category` | no | string | Organizational grouping for the skill catalog. Examples: `engineering`, `content`, `operations`, `identity`, `review`. |
| `description` | no | string | One-line description. Helps agents and humans understand what the skill covers without reading the full content. |
| `default` | no | boolean | `true` if this skill should always be activated for its scope level. Default: `false`. See Org/Project Defaults. |

### Format Constraints

- **No executable code.** Skills are documents, not programs. No shell scripts, no code blocks intended for execution, no template variables, no conditionals.
- **No templating.** The skill content is loaded as-is. There is no `{{project_name}}` substitution or dynamic content generation. If a skill needs project-specific details, write a project-level skill that references the project's conventions directly.
- **No imports or inheritance.** Skills don't reference or include other skills. If two skills share content, factor it into a separate skill and attach both where needed.
- **Plain markdown only.** Standard markdown formatting (headers, lists, bold, code blocks for examples). No custom directives, no frontmatter-driven behavior beyond metadata.

## Skill Storage

Skills are files in git repos. This is consistent with "every project is a git repo" (doc 03) -- skills are versioned artifacts managed through the same mechanics as everything else.

### Where Skills Live

- **Org-level skills**: stored in a dedicated org-level skills repository. This repo is created during org bootstrap and is not tied to any project. Path: `skills/` directory at the repo root. All org-wide defaults and org-scoped skills live here.
- **Project-level skills**: stored in the project's git repo. Path: `skills/` directory at the project repo root. Project-specific conventions, standards, and agent identity skills for project-scoped agents live here.

### Directory Convention

```
skills/
  go-coding-standards.md
  code-review-checklist.md
  api-design-conventions.md
  blog-writing-guidelines.md
  identities/
    frank.md
    lori.md
    ellie.md
```

The `identities/` subdirectory is a convention for agent identity skills, not a system requirement. All `.md` files in the `skills/` directory tree are recognized as skills. The system scans for YAML frontmatter to identify valid skill files.

### Version History

No skill versioning beyond git history. The latest committed version on the default branch (`main`) is the active version. Git tracks every change naturally -- who changed what, when, and why. There is no separate skill version counter, no rollback mechanism beyond `git revert`, and no draft/published lifecycle. This is intentional simplicity.

If a skill change breaks something, the PM or human reverts the commit through conversation. The git history provides a complete audit trail.

## Skill Lifecycle

### Creation

Skills are created through conversation with the PM or the human working directly. There is no skill editor UI. The PM writes the skill file and commits it to the appropriate repo.

**Typical creation flow:**

1. Human tells the PM: "We need coding standards for this project."
2. PM drafts the skill content based on the conversation, project context, and any existing conventions it knows about.
3. PM presents the draft to the human for review.
4. Human approves or requests changes.
5. PM writes the file to `skills/` in the project repo and commits.

The PM is opinionated -- it doesn't ask "what format do you want?" It proposes a well-structured skill document based on the type of work. The human adjusts from there.

Frank can also create org-level skills when the human identifies cross-project standards. Lori may suggest identity skills when staffing new agents.

### Editing

Same flow as creation. The human or PM edits the skill file and commits the change. The PM can proactively suggest skill updates when it observes repeated feedback or corrections that indicate the skill is incomplete or outdated.

For example: if a code reviewer keeps rejecting PRs for the same reason that isn't covered by the coding standards skill, the PM should suggest updating the skill rather than repeating the feedback every time.

### Deletion

Delete the file and commit. The PM confirms with the human before deleting skills that are referenced by flow nodes or agent profiles. Removing a skill that's still referenced doesn't cause a runtime error -- the system simply doesn't find the file and skips it, logging a warning.

### Discovery

The PM knows what skills exist in its project. When designing flows, it can reference available skills by slug. When a human asks "what skills do we have?", the PM lists them from the repo.

## Skill Attachment Points

Skills can be attached at four levels, from broadest to narrowest:

- **Org default skills**: apply to all work across all projects in the org. Set via `default: true` in the frontmatter of an org-scoped skill. Examples: safety policies, communication guidelines, org-wide coding conventions.
- **Project default skills**: apply to all work within a project. Set via `default: true` in the frontmatter of a project-scoped skill. Examples: project-specific coding standards, architecture conventions, style guides.
- **Agent-level skills**: part of the agent's profile. Define the agent's identity and core competencies. Linked in the `agent_skill` table. These are the skills that make Frank "Frank" and the PM "the PM."
- **Flow node skills**: declared on a flow node in the flow template. Only activated when the agent is executing that specific node. Linked in the `flow_node_skill` table. These are the most targeted -- an agent writing a blog post gets blog-writing skills, not deployment skills.

## Activation vs Availability

An agent may have many skills available across its profile, project, and org. But only skills relevant to the current work are **activated** and loaded into the prompt. This is critical for token budget management -- loading every available skill would waste context window budget on irrelevant instructions.

### Activation Rules

**When a flow node declares skills** (the flow node has entries in `flow_node_skill`):

1. Load the flow node's declared skills.
2. Load org default skills (`default: true`, org scope).
3. Load project default skills (`default: true`, project scope).
4. Load agent identity skills (agent-level skills from `agent_skill`).
5. That's it. Other agent-level skills that aren't defaults or declared on the node are NOT loaded.

**When a flow node does NOT declare skills** (no `flow_node_skill` entries):

1. Load ALL agent-level skills from the agent's profile.
2. Load org default skills.
3. Load project default skills.
4. This is the fallback -- the agent gets its full skill set because the flow doesn't specify what's needed.

**In sync sessions** (human-in-the-loop chat, not tied to a flow node):

1. Load ALL agent-level skills from the agent's profile.
2. Load org default skills.
3. Load project default skills (if the session is project- or task-scoped).

### Why This Matters

An agent assigned as a reviewer might have skills for code review, security review, and performance review. When the flow node says "do a code review," only the code review skill is loaded -- the agent doesn't waste context on security review instructions for this step. But the agent is still capable of security review when assigned to a node that declares it.

This keeps agents versatile (many skills available) while keeping prompts focused (only relevant skills activated).

## Resolution Order (for Conflicts)

When multiple activated skills contain conflicting instructions, precedence follows specificity:

1. **Flow node skills** (most specific -- explicitly chosen for this step)
2. **Agent-level skills** (part of this agent's identity)
3. **Project default skills** (project-wide conventions)
4. **Org default skills** (broadest, least specific)

More specific wins. If a flow node skill says "use tabs for indentation" and the org default says "use spaces," the flow node skill wins for that step. The agent is told the resolution order in its prompt assembly so it knows which instruction takes priority.

In practice, conflicts should be rare. Org defaults set broad conventions, project defaults refine them, and flow node skills address the specific work. If conflicts are frequent, the skill set needs reorganization -- the PM should flag this.

## Skills in Prompt Assembly

Skills are layer 4 of the 7-layer prompt assembly pipeline (see 05-agents-staff-and-temps.md):

1. Agent identity (never cut)
2. Policies and constraints (never cut)
3. Scope context
4. **Skills instructions** (this layer)
5. Memory
6. Conversation history
7. Tool descriptions

### Assembly Mechanics

During prompt assembly (see 05-agents-staff-and-temps.md, Assembly Process):

1. **Resolve active skills**: determine which skills are activated for this turn based on the activation rules above.
2. **Fetch content**: read the skill files from the repo. The content (markdown body, excluding frontmatter) is what gets injected.
3. **Order by precedence**: arrange skills in resolution order (org defaults first, flow node skills last). Later content has higher precedence -- agents give more weight to instructions that appear later in their prompt.
4. **Budget check**: if the total skill content exceeds the token allocation for layer 4, apply truncation rules (see Token Budget).
5. **Inject**: include the assembled skill content in the prompt between scope context (layer 3) and memory (layer 5).

Each skill is injected with a header identifying it:

```
=== Skill: Go Coding Standards (project default) ===

[skill content here]

=== Skill: Code Review Checklist (flow node) ===

[skill content here]
```

The header tells the agent where the instructions came from and at what level, so it can reason about conflicts if they arise.

## Token Budget

Skills compete for prompt space alongside scope context, memory, conversation history, and tool descriptions. The prompt assembly pipeline allocates a budget to each layer and skills must fit within their allocation.

### Budget Behavior

- **Org and project default skills are never cut.** They are treated as near-mandatory (same priority tier as policies). If defaults alone exceed the skill budget, the system logs a warning -- this indicates the defaults are too large and need trimming.
- **Agent identity skills are high priority.** These define who the agent is. Cut only as a last resort.
- **Flow node skills are medium priority.** They are specific to the current step and highly relevant, but can be summarized if budget is extremely tight.
- **Non-default, non-identity agent skills** (only loaded in the fallback case where the flow node doesn't declare skills) are the first to be cut.

### Truncation Strategy

When the total activated skill content exceeds the layer 4 budget:

1. Remove non-default, non-identity agent-level skills (lowest priority first, by attachment order).
2. If still over budget, summarize flow node skills (LLM-powered, Haiku-class -- condense the instructions to their essentials).
3. If still over budget, summarize agent identity skills.
4. Never cut org/project defaults. If defaults alone exceed the budget, log a warning and compress conversation history or memory to make room.

This should rarely be needed in practice. The maximum recommended skill size exists to prevent this situation.

### Maximum Recommended Skill Size

**~4,000 tokens per skill document.** Larger skills should be split into focused, independent skills.

This is a guideline, not a hard limit. The system doesn't reject skills over 4,000 tokens. But a single 8,000-token skill consumes a disproportionate share of the skill budget and crowds out other skills. Splitting it into two 4,000-token skills allows the system to load only the relevant one for a given flow node.

The PM should flag skills that exceed this recommendation and suggest splitting them during skill creation or review.

## Skills vs Procedural Memory

Skills and procedural memory both tell an agent "how to do things," but they differ in origin, authority, and lifecycle:

| Dimension | Skills | Procedural Memory |
|---|---|---|
| **Origin** | Human-authored. Someone deliberately wrote these instructions. | System-extracted. Ellie observed what worked in practice and stored the pattern. |
| **Authority** | Prescriptive. Treated as directives in the prompt. | Advisory. The agent can use it or ignore it. |
| **Prompt layer** | Layer 4 (skills). Higher priority in assembly. | Layer 5 (memory). Lower priority, budget-dependent. |
| **Lifecycle** | Versioned in git. Updated through conversation + commit. Persistent until changed. | Decays if not reinforced by successful outcomes. Emerges from experience. |
| **Conflict resolution** | Skills win. Always. | Procedural memory defers to skills. |
| **Management** | PM or human creates and maintains. | Ellie extracts, consolidates, and manages autonomously. |

**If they conflict, the skill wins.** It is the explicit policy. Procedural memory fills the gaps that skills don't cover.

### The Bridge: Skill Synthesis from Procedural Memory

When procedural memories cluster around a workflow -- multiple learned patterns all pointing to the same best practice -- Ellie can surface these as **skill candidates**. The PM reviews the candidate and, if it's sound, writes a proper skill document. This is the bridge from learned experience to codified practice. It is a human-in-the-loop promotion, never automatic. See doc 06, Future Enhancements.

## Default Skills

V2 ships with a set of default skills in the bootstrap dataset. These are loaded into the org-level skills repo during initial setup.

### Starter Trio Identity Skills

Each of the three bootstrap agents (Frank, Lori, Ellie) has an identity skill that defines who they are, how they communicate, and what they're responsible for. These are agent-level skills, not org defaults -- they apply only to that specific agent.

**Frank (Chief of Staff)**
- Role: primary human touchpoint, org-level coordination, cross-project oversight.
- Communication style: direct, concise, proactive. Summarizes rather than rambles.
- Responsibilities: triage, escalation, daily summaries, org-level task management.
- Explicitly NOT a project manager.

**Lori (Agent Relations)**
- Role: staffing, hiring/retiring agents, capability assessment.
- Communication style: thoughtful, thorough on agent capabilities and fit.
- Responsibilities: recommending agents for projects, evaluating agent performance, suggesting new agent profiles.

**Ellie (Memory)**
- Role: memory infrastructure AND conversational agent (dual role -- see doc 06).
- Communication style: precise, citation-heavy, transparent about confidence levels.
- Responsibilities: retrieval, capture, synthesis, dedup, taxonomy management.
- Conversational capabilities: answer queries, explain retrieval, show history, explicit capture/forget/correct.

### Project Manager Identity Skill

A default PM identity skill ships with the bootstrap dataset. When a project is created and a PM agent is assigned, this skill defines the PM's core behaviors:

- Opinionated planning: proposes flows, doesn't ask "which pattern?"
- Task scoping: produces tasks ready for execution with description, acceptance criteria, constraints.
- Proactive supervision: monitors active runs for stuck agents.
- Skill management: creates and updates project skills through conversation.
- Flow design: recommends the right workflow based on work type.

### Org Default Skills

These ship as org defaults (`default: true`) and apply to all work across all projects:

**Safety and Communication Policies**
- Rules for handling sensitive information (credentials, PII).
- Escalation behavior: when to escalate and how.
- Communication boundaries: what agents should and shouldn't communicate externally.

**General Work Standards**
- Commit message conventions.
- How to handle ambiguity (ask, don't guess).
- When to file a blocker vs attempt a workaround.
- How to signal "step done" vs "I need help."

### Project Template Skills

These are not installed by default but are available as templates the PM can adopt when setting up a project:

- **Code Review Checklist**: what to look for (correctness, readability, test coverage, error handling, security).
- **Go Coding Standards**: conventions, error handling, testing, dependency management.
- **TypeScript Coding Standards**: same concept, different language.
- **API Design Conventions**: naming, versioning, error response format, pagination.
- **Content Writing Guidelines**: voice, structure, length, SEO considerations.
- **Security Review Checklist**: authentication, authorization, input validation, data handling.

The PM picks from these when setting up a project's skill set, customizing as needed for the project's specific requirements.

## Database Schema

### skill

```sql
create table skill (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  project_id      uuid references project(id),       -- null = org-scoped
  name            text not null,
  slug            text not null,
  scope           text not null check (scope in ('org', 'project')),
  category        text,
  description     text,
  is_default      boolean not null default false,     -- always activated for its scope level
  file_path       text not null,                      -- path within the repo (e.g., skills/go-coding-standards.md)
  created_by_type text not null check (created_by_type in ('human', 'agent')),
  created_by_id   uuid not null,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}'
);

-- Design notes:
-- The skill table is a registry. The actual skill content lives in the git repo as a markdown file.
-- `file_path` points to the file within the repo. For org skills, the repo is the org skills repo.
-- For project skills, the repo is the project's repo.
-- The content is read from the file at prompt assembly time, not stored in the DB.
-- This means skill content is always the latest committed version.
-- `is_default` marks skills that are always activated for their scope level.
-- Slug uniqueness is scoped: org skills unique within the org, project skills unique within the project.

create unique index idx_skill_org_slug on skill(organization_id, slug) where project_id is null;
create unique index idx_skill_project_slug on skill(project_id, slug) where project_id is not null;
create index idx_skill_org on skill(organization_id);
create index idx_skill_project on skill(project_id) where project_id is not null;
create index idx_skill_scope_default on skill(organization_id, scope, is_default) where is_default = true;
```

### agent_skill

```sql
create table agent_skill (
  id         uuid primary key default gen_random_uuid(),
  agent_id   uuid not null references agent(id),
  skill_id   uuid not null references skill(id),
  purpose    text,                                    -- why this skill is attached (e.g., "identity", "core competency")
  priority   int not null default 0,                  -- ordering hint; higher = higher priority within agent skills
  created_at timestamptz not null default now(),

  unique (agent_id, skill_id)
);

-- Design notes:
-- Links agents to their profile-level skills.
-- Identity skills (Frank's identity, PM's planning skill) are agent_skill entries.
-- `purpose` is descriptive metadata, not a system enum. Helps the PM understand
-- why a skill is attached when managing agent profiles.
-- `priority` determines cut order when budget is tight -- lower priority skills
-- are removed first. Identity skills should have the highest priority.

create index idx_agent_skill_agent on agent_skill(agent_id);
create index idx_agent_skill_skill on agent_skill(skill_id);
```

### flow_node_skill

```sql
create table flow_node_skill (
  id           uuid primary key default gen_random_uuid(),
  flow_node_id uuid not null references flow_node(id),
  skill_id     uuid not null references skill(id),
  created_at   timestamptz not null default now(),

  unique (flow_node_id, skill_id)
);

-- Design notes:
-- Links flow nodes to the skills required for that step.
-- When a flow node has entries here, only these skills (plus defaults and identity)
-- are activated. When a flow node has no entries, the agent's full skill set is used.
-- This is the primary mechanism for keeping agent prompts focused.
-- The PM sets these when designing flow templates through conversation.

create index idx_flow_node_skill_node on flow_node_skill(flow_node_id);
create index idx_flow_node_skill_skill on flow_node_skill(skill_id);
```

### Relationship to Existing Schema

The `flow_node` table (doc 03) currently has a `skills` jsonb column for skill references. With the `flow_node_skill` join table, this jsonb column is no longer needed -- the join table provides proper referential integrity and queryability. The `skills` jsonb column on `flow_node` should be dropped in favor of `flow_node_skill`.

The `agent` table (doc 05) does not currently have a skills column. Agent-skill relationships are fully represented by the `agent_skill` join table.

## Skill Resolution at Runtime

When prompt assembly needs to resolve the active skills for a given agent turn, the following query logic applies:

### Step 1: Determine Context

```
inputs:
  agent_id        -- the agent taking this turn
  session_scope   -- org, project, or task
  project_id      -- if project or task scoped (nullable)
  flow_node_id    -- if executing a flow node (nullable)
```

### Step 2: Resolve Active Skills

```
1. flow_node_skills = []
   if flow_node_id is not null:
     flow_node_skills = SELECT s.* FROM skill s
       JOIN flow_node_skill fns ON fns.skill_id = s.id
       WHERE fns.flow_node_id = :flow_node_id

2. agent_skills = SELECT s.* FROM skill s
     JOIN agent_skill AS_ ON AS_.skill_id = s.id
     WHERE AS_.agent_id = :agent_id
     ORDER BY AS_.priority DESC

3. org_defaults = SELECT * FROM skill
     WHERE organization_id = :org_id
       AND project_id IS NULL
       AND is_default = true

4. project_defaults = []
   if project_id is not null:
     project_defaults = SELECT * FROM skill
       WHERE project_id = :project_id
         AND is_default = true

5. if flow_node_skills is not empty:
     active = org_defaults + project_defaults + agent_identity_skills + flow_node_skills
   else:
     active = org_defaults + project_defaults + agent_skills

6. deduplicate by skill_id (if a skill appears at multiple levels, keep the most specific)

7. return active
```

### Step 3: Fetch Content

For each active skill, read the file from the repo at `skill.file_path`. The file content (markdown body after frontmatter) is the skill text injected into the prompt.

Files are read from the default branch (`main`) of the appropriate repo. If the file doesn't exist (deleted but registry not updated), log a warning and skip.

### Step 4: Assemble into Prompt

Order the skill content by resolution precedence (org defaults first, flow node skills last) and inject into layer 4 of the prompt assembly pipeline. Apply token budget constraints.

## Skill Management Tools

The PM and other agents manage skills through standard OtterCamp tools, not through a separate skill management interface.

### Available Operations

- **Create skill**: PM writes the file, commits to repo, creates the `skill` registry entry. One operation from the agent's perspective.
- **Update skill**: PM edits the file, commits. The `skill.updated_at` timestamp is refreshed.
- **Delete skill**: PM deletes the file, commits, removes the `skill` registry entry.
- **Attach to agent**: PM creates an `agent_skill` entry.
- **Detach from agent**: PM removes the `agent_skill` entry.
- **Attach to flow node**: PM creates a `flow_node_skill` entry when designing or updating a flow template.
- **Detach from flow node**: PM removes the `flow_node_skill` entry.
- **List skills**: PM queries the `skill` table filtered by scope.
- **Read skill content**: PM reads the file from the repo.

All of these are conversational -- the human says "add the security review checklist to the code review step," and the PM handles the mechanics.

### Registry Consistency

The `skill` table is a registry that mirrors what's in the repo. The PM is responsible for keeping them in sync -- when it creates a skill file, it also creates the registry entry. If they drift (file exists without registry entry, or registry entry points to a missing file), the system logs a warning but doesn't fail.

A periodic consistency check can scan the `skills/` directory in each repo and reconcile with the registry. This is a background hygiene operation, not a critical path.

## Resolved Decisions

1. **Skills are plain markdown with YAML frontmatter.** No executable code, no templating, no conditional logic. Documents, not programs.
2. **No skill versioning beyond git history.** Latest committed version on `main` wins. Git tracks changes naturally. No draft/published lifecycle.
3. **Maximum recommended skill size: ~4,000 tokens.** Larger skills should be split. Guideline, not a hard limit.
4. **Skills live as files in git repos.** Org skills in the org skills repo, project skills in the project repo. Consistent with "every project is a git repo" from doc 03.
5. **Org and project default skills are never cut from the prompt.** They have the same priority as policies. If defaults alone exceed the skill budget, the system logs a warning.
6. **The PM manages skills within a project.** Creates, edits, deletes, attaches, detaches -- all through conversation. The human can also write/edit skills directly.
7. **Default skills ship as part of the bootstrap dataset.** Starter trio identity skills, PM identity skill, org-wide safety/communication policies, general work standards. Project template skills available but not pre-installed.
8. **Skills win over procedural memory on conflict.** Skills are prescriptive (layer 4), procedural memory is advisory (layer 5). Always.
9. **Flow node skill declarations narrow the active skill set.** When a flow node declares skills, only those skills (plus defaults and agent identity) are loaded. When it doesn't, the agent gets its full skill set.
10. **Resolution order follows specificity.** Flow node > agent-level > project default > org default. More specific wins.
11. **The `skills` jsonb column on `flow_node` is replaced by `flow_node_skill` join table.** Proper referential integrity and queryability.
12. **No skill editor UI.** Skills are created and edited through conversation with agents. The UI can display skills read-only (content from the repo file), but editing always happens through conversation + git commit.
13. **Skill content is read from the repo at prompt assembly time.** Not cached in the database. This means changes take effect on the next turn after commit.
14. **Agent identity skills are high priority in budget allocation.** Cut only as a last resort, after non-default and non-identity skills have been removed.
15. **Skill registry (`skill` table) mirrors repo contents.** PM keeps them in sync. Background consistency check for drift detection.
16. **Skill files use a `skills/` directory convention.** With an optional `identities/` subdirectory for agent identity skills. Convention, not enforcement.
17. **Skill synthesis from procedural memory is human-in-the-loop.** Ellie can surface candidates but the PM writes the actual skill document. Never automatic promotion.

## Open Questions

_None currently outstanding._
