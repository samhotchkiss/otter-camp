# Instructions: Project Workflow

> How to create, clone, commit, and collaborate on OtterCamp projects.
> Last updated: 2026-02-16

## Every Piece of Work Is a Project

OtterCamp projects are git repos. All work product — content, code, designs, research — lives in a project. If it's work product, it belongs here.

## CLI Basics

```bash
# Auth check
otter whoami

# List projects
otter project list

# Create a project
otter project create "My Project" --description "What it's for"

# Clone a project
otter clone <project-name>
# Clones to ~/Documents/OtterCamp/<project-name>
```

## Working on a Project

```bash
cd ~/Documents/OtterCamp/<project>

# Do your work — edit files, create docs, write code

# Commit frequently
git add -A
git commit -m "descriptive message about what changed"
git push   # pushes to otter.camp
```

### Commit Discipline
- **Small commits.** One logical change per commit. Not a batch of 10 things.
- **Descriptive messages.** "Add vector embedding benchmark results" not "update docs".
- **Commit after every meaningful change.** Don't accumulate uncommitted work.

## Tasks

Every piece of work gets a task with acceptance criteria.
Projects can define a default flow template; tasks inherit it unless explicitly assigned another flow.
Each project should assign a project manager agent (`primary_agent_id`) as full-context owner for escalations.

```bash
# List tasks
otter issue list --project <name>

# Create a task
otter issue create --project <name> "Task title" --body "Description" --priority P1

# View, comment, close
otter issue view --project <name> <number>
otter issue comment --project <name> <number> "Comment text"
otter issue close --project <name> <number>
```

### Blocker Escalation Rules
- Sub-agent blocker -> escalate in task chat to project manager agent.
- If unresolved by project manager -> escalate to human.
- Human escalation moves task to inbox and `on_hold` until response.

## Documentation

Every project has a `docs/` dir. See `project-docs-spec.md` for the full spec. The short version:

1. `docs/START-HERE.md` is mandatory — it's the index
2. One domain dir per subsystem, each with an `overview.md`
3. One topic, one file
4. Change log at the bottom of every doc
5. Update START-HERE.md when you add docs

## What Goes Where

| Type | Location |
|---|---|
| Work product (code, content, designs) | OtterCamp project |
| Project documentation | `<project>/docs/` |
| Personal agent notes, memory files | `~/Documents/SamsBrain/Agents/<name>/` |
| Tasks and tracking | OtterCamp tasks |

## Change Log

- 2026-02-21: Documented project manager ownership and blocker escalation path (sub-agent -> PM -> human inbox/on-hold).
- 2026-02-21: Renamed project work terminology from issues/subissues to tasks/subtasks in workflow guidance.
- 2026-02-21: Documented default flow templates for tasks.
- 2026-02-16: Created project workflow instructions.
