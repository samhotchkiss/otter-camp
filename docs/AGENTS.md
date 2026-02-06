# Agent Instructions for OtterCamp

> This document defines how agents interact with OtterCamp. All agents (human or AI) producing work that OtterCamp should track must follow these guidelines.

## Core Principles

**OtterCamp is your primary workspace.** Everything you create, modify, or produce gets committed and pushed here. GitHub (when connected) is a downstream sync target, not your workspace.

**Commits = activity.** The OtterCamp activity feed is driven by commits (and linked issues/PRs). If it matters, it must be committed.

**No secrets in repos.** Never commit tokens, credentials, or private keys.

---

## The Commit-Everything Model

All work product must be committed to an OtterCamp project repository:

| Work Type | What to Commit |
|-----------|----------------|
| **Code** | Source files, configs, scripts |
| **Writing** | Blog posts, documentation, drafts (as markdown) |
| **Images** | Generated images, diagrams, assets |
| **Data** | JSON, CSV, any structured output |
| **Research** | Notes, summaries, reference materials |
| **Design** | Mockups, assets, design docs |

### Why?

1. **Visibility** — Sam and the team can see what you're working on
2. **History** — Every change is tracked with context
3. **Collaboration** — Other agents can build on your work
4. **Accountability** — Work that isn't committed didn't happen

---

## Getting Started

Use the `otter` CLI to create/clone projects so the **remote is set correctly**.
Full CLI usage: `docs/CLI.md`.

### Option A — Create + clone (recommended)
```bash
# Create a new OtterCamp project
otter project create "Project Name"

# Clone it locally (sets the correct remote automatically)
otter clone <project-name>

# Work in the repo
cd ~/Documents/OtterCamp/<project-name>
```

### Option B — Project already exists
```bash
# Clone existing project (remote set automatically)
otter clone <project-name>
cd ~/Documents/OtterCamp/<project-name>
```

### Option C — Repo exists locally, add the remote
If you already have a local repo, add the OtterCamp remote:
```bash
otter remote add <project-name>
```

If you need the raw remote URL:
```bash
otter repo info <project-name>
# then: git remote add origin <url>
```

---

## Commit Message Format (Required)

Every commit must follow this format:

```
<type>(<scope>): <short description>

<detailed body explaining what was done and why>

Refs: #<issue-number> (if applicable)
```

### Commit Types

- `feat` — New feature or capability
- `fix` — Bug fix
- `docs` — Documentation only
- `content` — Blog posts, articles, creative writing
- `assets` — Images, media, design files
- `refactor` — Code restructuring (no functional change)
- `chore` — Maintenance, cleanup, config changes

### The Body is Required

OtterCamp's activity stream surfaces the **commit body** as the expanded, human-readable description of work. If the body is empty, the feed becomes low-signal.

Explain:
- What you did
- Why you did it
- Any decisions you made
- Context that would help someone understand the change

**Bad:**
```
feat: add homepage
```

**Good:**
```
feat(web): add homepage with hero section and CTA

Built the initial homepage layout based on Jeff G's mockups.
Includes:
- Hero section with tagline "Your AI Team, Organized"
- Primary CTA button linking to /signup
- Feature grid showing 3 key benefits
- Responsive layout (mobile-first)

Used warm gold accent (#C9A86C) per design spec.
Kept animations subtle to avoid distraction.

Refs: #42
```

---

## Working with Issues

### When You're Assigned an Issue

1. **Read the full issue** — including comments and linked context
2. **Comment when you start** — "Starting work — session `{sessionKey}`"
3. **Update as you go** — check boxes, add progress comments
4. **Commit frequently** — small commits > big commits
5. **Close with a summary** — what was done, any follow-ups needed

### Issue References in Commits

Always include `Refs: #123` (or `Closes: #123`) in your commit body when the work relates to an issue.

---

## Project Structure

Each project in OtterCamp has its own repository. The structure depends on project type:

### Software Projects
```
/src           — source code
/docs          — documentation
/tests         — test files
README.md      — project overview
```

### Content Projects
```
/posts         — blog posts, articles
/drafts        — work in progress
/assets        — images, media
README.md      — project overview
```

### Dedicated Non-Code Repos

Use these repositories when work is primarily content/book/asset production:

| Domain | Repository |
|--------|------------|
| Long-form content, posts, drafts | `The-Trawl/content` |
| Book manuscripts, editorial notes | `The-Trawl/three-stones-book` |
| Logos, illustrations, social assets | `The-Trawl/brand-assets` |

If your output belongs in one of these domains, commit there so activity appears in OtterCamp and history stays project-scoped.

---

## Push Workflow

```bash
git add -A
git commit -m "type(scope): short description" -m "Verbose body here..."
git push
```

Commit early and often. Push when done so OtterCamp can ingest the activity.

---

## Syncing with GitHub

Some projects sync with GitHub. When they do:

- **Code flows both ways** — but OtterCamp is the source of truth
- **Issues are pulled from GitHub** — external contributors can report bugs
- **Closing an OtterCamp issue** closes the linked GitHub issue

You don't need to think about GitHub directly. Just commit to OtterCamp.

### Manual Re-Sync (When Needed)

Agents may trigger a re-sync via API:

```
POST /api/projects/:id/repo/sync
POST /api/projects/:id/issues/import
```

Use sparingly (after a batch of commits or when GitHub updated externally).

---

## What NOT to Commit

- **Secrets** — API keys, tokens, passwords (use environment variables)
- **Large binaries** — videos, large datasets (link to external storage)
- **Node modules / vendor deps** — these are installed, not tracked
- **Personal notes** — use your own memory files, not project repos

---

## Communication

### Status Updates

When working on significant tasks, provide updates in the relevant Slack channel:
- What you've done
- What you're doing next
- Any blockers

### Asking for Help

If you're stuck:
1. Check the issue for context
2. Check related issues/PRs
3. Search the repo for similar patterns
4. **Then** ask in Slack with specific context

---

## Quality Standards

- **Test your changes** — if the project has tests, run them
- **Follow existing patterns** — match the code style already in use
- **Keep commits atomic** — one logical change per commit
- **Review your own work** — read the diff before committing

---

## Summary

1. **Commit everything** — all work goes into OtterCamp
2. **Write good commit messages** — verbose bodies, clear intent
3. **Reference issues** — connect your work to the task
4. **Small commits** — commit often, push frequently
5. **Stay visible** — update issues and Slack as you work

---

*Questions or updates? Open a PR or issue in `otter-camp` and tag the maintainers.*
