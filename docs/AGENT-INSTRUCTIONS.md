# Agent Instructions for OtterCamp

> This document defines how AI agents interact with OtterCamp. All agents working within this ecosystem must follow these guidelines.

## Core Principle

**OtterCamp is your primary workspace.** Everything you create, modify, or produce gets committed and pushed here. GitHub (when connected) is a downstream sync target, not your workspace.

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

### Why?

1. **Visibility** — Sam and the team can see what you're working on
2. **History** — Every change is tracked with context
3. **Collaboration** — Other agents can build on your work
4. **Accountability** — Work that isn't committed didn't happen

---

## Commit Message Format

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

The commit body should be **verbose**. Explain:
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

### Mixed Projects
Organize logically. When in doubt, create a folder.

### Dedicated Non-Code Repos

Use these repositories when work is primarily content/book/asset production:

- `https://github.com/The-Trawl/content`
  - long-form content, posts, drafts, research notes
- `https://github.com/The-Trawl/three-stones-book`
  - manuscript chapters, editorial notes, book research, exports
- `https://github.com/The-Trawl/brand-assets`
  - logos, illustrations, social assets, and delivery exports

If your output belongs in one of these domains, commit there first so activity appears in OtterCamp and history stays project-scoped.

---

## Syncing with GitHub (When Connected)

Some projects sync with GitHub. When they do:

- **Code flows both ways** — but OtterCamp is the source of truth
- **Issues are pulled from GitHub** — external contributors can report bugs
- **Deployments push to GitHub** — which triggers CI/CD
- **Closing an OtterCamp issue** closes the linked GitHub issue

You don't need to think about GitHub directly. Just commit to OtterCamp.

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

*This document is the source of truth for agent behavior in OtterCamp. If you have suggestions for improvements, open an issue.*
