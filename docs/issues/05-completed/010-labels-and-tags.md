# Issue #10: Labels & Tags for Projects and Issues

## Summary

Add a freeform labeling system to both projects and issues so Sam can organize, filter, and sort his growing project board and issue lists. Labels are colored tags with a name — simple, flexible, and filterable everywhere.

## The Problem

As the project board grows (currently 8 projects, heading toward 20+), there's no way to slice it:

- **Projects** can only be filtered by `status` (active/archived/completed) — no way to distinguish "product" from "content" from "infrastructure" from "personal"
- **Issues** have `work_status` and `priority` but no freeform categorization — can't tag things as "bug", "feature", "design-needed", "blocked-on-sam", etc.
- **No cross-cutting views** — can't ask "show me all issues labeled 'needs-review' across all projects"

## Design

### Label Model

Labels are org-scoped (shared across all projects) with a name and color:

```go
type Label struct {
    ID        string    `json:"id"`
    OrgID     string    `json:"org_id"`
    Name      string    `json:"name"`       // e.g. "bug", "feature", "infrastructure"
    Color     string    `json:"color"`      // hex color, e.g. "#e11d48"
    CreatedAt time.Time `json:"created_at"`
}
```

Labels are applied to projects and issues via join tables:

```go
type ProjectLabel struct {
    ProjectID string    `json:"project_id"`
    LabelID   string    `json:"label_id"`
    CreatedAt time.Time `json:"created_at"`
}

type IssueLabel struct {
    IssueID   string    `json:"issue_id"`
    LabelID   string    `json:"label_id"`
    CreatedAt time.Time `json:"created_at"`
}
```

**Key design decisions:**
- Labels are **org-scoped**, not project-scoped — the same "bug" label works everywhere
- Labels have **colors** — makes them visually scannable in lists
- Labels are **many-to-many** — a project can have multiple labels, a label can be on multiple projects
- **No hierarchy** — flat labels, not nested categories. Keep it simple.
- Labels are **created on-the-fly** — type a new label name while tagging, it gets created automatically if it doesn't exist

### Preset Labels

On first use (or org creation), seed a starter set:

| Name | Color | Intended Use |
|------|-------|-------------|
| product | `#3b82f6` (blue) | Product projects (Otter Camp, ItsAlive, Pearl) |
| content | `#8b5cf6` (purple) | Content projects (Technonymous, Three Stones) |
| infrastructure | `#6b7280` (gray) | Infra (OpenClaw, hosting, tooling) |
| personal | `#f59e0b` (amber) | Personal projects |
| bug | `#ef4444` (red) | Bug issues |
| feature | `#22c55e` (green) | Feature issues |
| design | `#ec4899` (pink) | Needs design work |
| blocked | `#f97316` (orange) | Blocked on something |
| needs-review | `#eab308` (yellow) | Waiting for human review |
| quick-win | `#06b6d4` (cyan) | Small, fast to implement |

These are just starting suggestions — Sam can rename, recolor, or delete them.

## Database Schema

### Migration

```sql
-- Labels table (org-scoped)
CREATE TABLE labels (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    color      TEXT NOT NULL DEFAULT '#6b7280',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(org_id, name)
);

CREATE INDEX idx_labels_org ON labels(org_id);

-- Project labels (many-to-many)
CREATE TABLE project_labels (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    label_id   UUID NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, label_id)
);

CREATE INDEX idx_project_labels_label ON project_labels(label_id);

-- Issue labels (many-to-many)
CREATE TABLE issue_labels (
    issue_id   UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    label_id   UUID NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (issue_id, label_id)
);

CREATE INDEX idx_issue_labels_label ON issue_labels(label_id);
CREATE INDEX idx_issue_labels_issue ON issue_labels(issue_id);
```

## API Endpoints

### Label CRUD

```
GET    /api/labels                    — List all labels for the org
POST   /api/labels                    — Create a label { name, color }
PATCH  /api/labels/{id}               — Update label { name?, color? }
DELETE /api/labels/{id}               — Delete label (removes from all projects/issues)
```

### Project Labels

```
GET    /api/projects/{id}/labels       — List labels on a project
POST   /api/projects/{id}/labels       — Add label(s) to project { label_ids: [...] }
DELETE /api/projects/{id}/labels/{lid}  — Remove label from project
```

### Issue Labels

```
GET    /api/projects/{pid}/issues/{iid}/labels       — List labels on an issue
POST   /api/projects/{pid}/issues/{iid}/labels       — Add label(s) { label_ids: [...] }
DELETE /api/projects/{pid}/issues/{iid}/labels/{lid}  — Remove label from issue
```

### Enhanced List Endpoints

Extend existing list endpoints with label filtering:

```
GET /api/projects?label=infrastructure&label=product    — filter by label(s) (AND)
GET /api/projects/{id}/issues?label=bug&label=blocked   — filter by label(s) (AND)
```

Also return labels in the project/issue list responses:

```json
{
  "id": "...",
  "name": "Otter Camp",
  "labels": [
    { "id": "...", "name": "product", "color": "#3b82f6" },
    { "id": "...", "name": "infrastructure", "color": "#6b7280" }
  ]
}
```

## CLI Support

```bash
# Label management
otter label list                                  # list all org labels
otter label create "bug" --color "#ef4444"        # create label
otter label delete "bug"                          # delete label

# Project labels
otter project label <project> add "product"       # add label to project
otter project label <project> remove "product"    # remove label
otter project list --label product                # filter projects by label

# Issue labels
otter issue label <project> <number> add "bug"    # add label to issue
otter issue label <project> <number> remove "bug" # remove label
otter issue list --project <name> --label bug     # filter issues by label
```

**Auto-create**: If a label name doesn't exist when adding, create it with a default color. This keeps the workflow frictionless — just type the tag name, don't worry about creating it first.

## Frontend Implementation

### Label Pill Component

Reusable colored pill that renders a label:

```tsx
// web/src/components/LabelPill.tsx
<LabelPill label={{ name: "bug", color: "#ef4444" }} />
// Renders: [🔴 bug] as a small colored pill
```

- Rounded pill shape
- Background is the label color at 15% opacity
- Text is the label color at full opacity
- Small "×" button when in edit mode (to remove)
- Click opens label management

### Label Picker Component

Dropdown/popover for adding labels to a project or issue:

```tsx
// web/src/components/LabelPicker.tsx
<LabelPicker
  selected={["bug", "feature"]}
  onAdd={(labelId) => ...}
  onRemove={(labelId) => ...}
  onCreate={(name) => ...}    // auto-create new label
/>
```

- Shows all org labels with checkmarks on selected ones
- Search/filter box at top
- "Create new label" option when search doesn't match existing
- Color picker for new labels (small palette of preset colors)

### Projects Page Enhancement

- **Label pills** displayed on each project card (below description)
- **Label filter bar** at top — click a label to filter, click again to remove
- **Multi-label filtering** — select multiple labels, shows projects matching ALL selected
- Labels are visually compact — don't overwhelm the card

### Project Detail — Issues List Enhancement

- **Label pills** on each issue row (alongside existing status/priority badges)
- **Label filter** in issue list header — filter issues by label
- **Bulk label** — select multiple issues, apply label to all (stretch goal)

### Issue Thread Panel Enhancement

- **Labels section** in issue header area — shows current labels with add/remove
- Labels editable inline — click "+" to open label picker

### Label Management Page

Accessible from Settings:

- List all org labels
- Edit name/color inline
- Delete (with confirmation: "This label is on X projects and Y issues")
- Bulk operations (merge two labels, etc.) — stretch goal

## Backend Implementation

### New Files

```
internal/store/label_store.go               — Label CRUD + join table operations
internal/store/label_store_test.go
internal/api/labels.go                      — Label HTTP handlers
internal/api/labels_test.go
migrations/041_create_labels.up.sql
migrations/041_create_labels.down.sql
```

### Modified Files

```
internal/api/router.go                      — Register label routes
internal/api/projects.go                    — Include labels in project list/detail responses, label filter param
internal/api/issues.go                      — Include labels in issue list/detail responses, label filter param
internal/store/project_store.go             — Label join queries in project list
internal/store/project_issue_store.go       — Label join queries in issue list, filter support
web/src/pages/ProjectsPage.tsx              — Label pills + filter bar
web/src/components/project/ProjectIssuesList.tsx — Label pills + filter
web/src/components/project/IssueThreadPanel.tsx  — Labels in issue header
web/src/pages/SettingsPage.tsx              — Label management section
cmd/otter/main.go                           — CLI label commands
internal/ottercli/client.go                 — Label API client methods
```

### Frontend New Files

```
web/src/components/LabelPill.tsx            — Colored label pill
web/src/components/LabelPill.test.tsx
web/src/components/LabelPicker.tsx          — Label selection dropdown
web/src/components/LabelPicker.test.tsx
web/src/components/LabelFilter.tsx          — Label filter bar for lists
web/src/components/LabelFilter.test.tsx
```

## Store Methods

```go
type LabelStore interface {
    // Label CRUD
    List(ctx context.Context) ([]Label, error)
    GetByID(ctx context.Context, id string) (*Label, error)
    GetByName(ctx context.Context, name string) (*Label, error)
    Create(ctx context.Context, name, color string) (*Label, error)
    Update(ctx context.Context, id string, name *string, color *string) (*Label, error)
    Delete(ctx context.Context, id string) error
    EnsureByName(ctx context.Context, name, defaultColor string) (*Label, error) // get-or-create

    // Project labels
    ListForProject(ctx context.Context, projectID string) ([]Label, error)
    AddToProject(ctx context.Context, projectID, labelID string) error
    RemoveFromProject(ctx context.Context, projectID, labelID string) error

    // Issue labels
    ListForIssue(ctx context.Context, issueID string) ([]Label, error)
    AddToIssue(ctx context.Context, issueID, labelID string) error
    RemoveFromIssue(ctx context.Context, issueID, labelID string) error

    // Batch lookups (for list views)
    MapForProjects(ctx context.Context, projectIDs []string) (map[string][]Label, error)
    MapForIssues(ctx context.Context, issueIDs []string) (map[string][]Label, error)
}
```

## Testing

### Backend
- Label CRUD: create, list, update, delete, uniqueness constraint
- EnsureByName: creates if missing, returns existing if present
- Project labels: add, remove, list, cascade delete when label deleted
- Issue labels: add, remove, list, cascade delete
- Project list with label filter: returns only matching projects
- Issue list with label filter: returns only matching issues
- Batch map queries: efficient N+1 avoidance for list views
- Migration: up/down, indexes, constraints

### Frontend
- LabelPill: renders with correct color, shows remove button in edit mode
- LabelPicker: search, select, deselect, create new
- LabelFilter: multi-select, clears, updates list
- ProjectsPage: labels render on cards, filter works
- IssuesList: labels render on rows, filter works
- Integration: add label → appears on card → filter works

### CLI
- `otter label list` returns labels
- `otter label create` creates with color
- `otter project label add` applies label
- `otter issue list --label` filters correctly
- Auto-create on add when label doesn't exist

## Rollout

1. **Migration + store** — Create tables, store methods, tests
2. **API endpoints** — Label CRUD + project/issue label endpoints
3. **Project list enhancement** — Include labels in response, add filter param
4. **Issue list enhancement** — Include labels in response, add filter param
5. **Frontend: LabelPill + LabelPicker** — Reusable components
6. **Frontend: ProjectsPage** — Label pills on cards + filter bar
7. **Frontend: IssuesList + IssueThread** — Label pills + filter + inline edit
8. **CLI commands** — label list/create/delete, project/issue label add/remove, --label filter
9. **Settings: Label management** — Edit/delete/recolor labels
10. **Seed preset labels** — On first access or via migration

## Success Criteria

- Projects page shows colored label pills on each card
- Can filter project list by one or more labels
- Issues show labels, can filter issue list by label
- Creating a label is frictionless — type a name, pick a color, done
- Auto-create labels when tagging (no need to pre-create)
- CLI supports full label workflow
- Labels are org-scoped — same labels work across all projects

## Execution Log
- [2026-02-08 13:45 MST] Issue spec #010 | Commit n/a | in-progress | Moved spec from `01-ready` to `02-in-progress` for full planning and implementation | Tests: n/a
- [2026-02-08 13:46 MST] Issue spec #010 | Commit n/a | branch-selected | Created branch `codex/spec-010-labels-and-tags` from `origin/main` for isolated Spec010 implementation (local `main` is attached to another worktree) | Tests: n/a
- [2026-02-08 13:48 MST] Issue #350 | Commit n/a | opened | Planned Spec010 foundation migration and label store primitives with explicit store/migration tests | Tests: go test ./internal/store -run 'TestLabelStore' -count=1
- [2026-02-08 13:48 MST] Issue #351 | Commit n/a | opened | Planned label CRUD API handlers + router wiring with validation/auth test coverage | Tests: go test ./internal/api -run 'TestLabelsHandler' -count=1
- [2026-02-08 13:48 MST] Issue #352 | Commit n/a | opened | Planned project/issue label assignment endpoints and idempotent add/remove/list API tests | Tests: go test ./internal/api -run 'Test(Project|Issue)LabelsHandler' -count=1
- [2026-02-08 13:48 MST] Issue #353 | Commit n/a | opened | Planned projects API/store label embedding and repeated `label` AND-filter support | Tests: go test ./internal/store -run 'TestProjectStoreListWithLabels' -count=1; go test ./internal/api -run 'TestProjectsHandlerLabelFilter' -count=1
- [2026-02-08 13:48 MST] Issue #354 | Commit n/a | opened | Planned issues API/store label embedding and repeated `label` AND-filter support | Tests: go test ./internal/store -run 'TestProjectIssueStoreListWithLabels' -count=1; go test ./internal/api -run 'TestProjectIssuesHandlerLabelFilter' -count=1
- [2026-02-08 13:49 MST] Issue #355 | Commit n/a | opened | Planned CLI label commands + label-aware list filters and ottercli/client test coverage | Tests: go test ./internal/ottercli -run 'TestLabel' -count=1; go test ./cmd/otter -run 'TestLabel' -count=1
- [2026-02-08 13:49 MST] Issue #356 | Commit n/a | opened | Planned frontend reusable label primitives (`LabelPill`, `LabelPicker`, `LabelFilter`) and component tests | Tests: cd web && npm test -- src/components/LabelPill.test.tsx src/components/LabelPicker.test.tsx src/components/LabelFilter.test.tsx --run
- [2026-02-08 13:49 MST] Issue #357 | Commit n/a | opened | Planned Projects page label pill rendering and multi-label filter integration tests | Tests: cd web && npm test -- src/pages/ProjectsPage.test.tsx --run
- [2026-02-08 13:49 MST] Issue #358 | Commit n/a | opened | Planned issue list/thread label UX (render/filter/inline edit) with component tests | Tests: cd web && npm test -- src/components/project/ProjectIssuesList.test.tsx src/components/project/IssueThreadPanel.test.tsx --run
- [2026-02-08 13:49 MST] Issue #359 | Commit n/a | opened | Planned settings label management UI and preset label seed path with backend/frontend tests | Tests: go test ./internal/store -run 'TestLabelPresetSeed' -count=1; cd web && npm test -- src/pages/SettingsPage.test.tsx --run
- [2026-02-08 13:49 MST] Issue spec #010 | Commit n/a | plan-complete | Full micro-issue set (#350-#359) created and logged before implementation begins | Tests: n/a
- [2026-02-08 13:56 MST] Issue #350 | Commit 387703e | closed | Added label schema migration (`046`) with RLS + join tables and implemented `LabelStore` CRUD/ensure/assignment/map methods with store tests | Tests: go test ./internal/store -run 'TestLabelStore' -count=1; go test ./internal/store -run 'TestLabelStore(EnsureByName|ProjectLabels|IssueLabels|MapLookups)' -count=1
- [2026-02-08 14:01 MST] Issue #351 | Commit 218e370 | closed | Added label CRUD API handlers (`GET/POST/PATCH/DELETE /api/labels`) with workspace scoping, router wiring, and CRUD/validation tests | Tests: go test ./internal/api -run 'TestLabelsHandler' -count=1; go test ./internal/api -run 'TestLabelsHandler(Create|List|Update|Delete)' -count=1
- [2026-02-08 14:00 MST] Issue #352 | Commit 287599b | closed | Added project/issue label assignment endpoints with UUID + ownership validation and idempotent add/remove/list behavior; expanded API coverage for workspace, invalid IDs, mismatch, and cross-org paths | Tests: go test ./internal/api -run 'TestProjectLabelsHandler' -count=1; go test ./internal/api -run 'TestIssueLabelsHandler' -count=1; go test ./internal/api -run 'TestLabelsHandler' -count=1; go test ./internal/api -count=1
- [2026-02-08 14:06 MST] Issue #353 | Commit fd9a38d | closed | Extended project store/API to embed label objects and support repeated `label` AND-filter semantics with UUID validation in project list/get responses | Tests: go test ./internal/store -run 'TestProjectStoreListWithLabels' -count=1; go test ./internal/api -run 'TestProjectsHandlerLabelFilter' -count=1; go test ./internal/store -run 'TestProjectStore' -count=1; go test ./internal/api -run 'TestProjectsHandler' -count=1; go test ./internal/store ./internal/api -count=1
- [2026-02-08 14:09 MST] Issue #354 | Commit 3dd4502 | closed | Added issue store/API label embedding and repeated `label` AND-filter support for `/api/issues`, including UUID validation and list/detail label payload coverage | Tests: go test ./internal/store -run 'TestProjectIssueStoreListWithLabels' -count=1; go test ./internal/api -run 'TestProjectIssuesHandlerLabelFilter' -count=1; go test ./internal/store -run 'TestProjectIssueStore' -count=1; go test ./internal/api -run 'TestIssuesHandler|TestProjectIssuesHandlerLabelFilter' -count=1; go test ./internal/store ./internal/api -count=1
- [2026-02-08 14:15 MST] Issue #355 | Commit 9bf44dc | closed | Added otter label/project-label/issue-label command surfaces, label-aware `--label` filters for project/issue list, and client-side auto-create-on-add label flows with CLI/client test coverage | Tests: go test ./internal/ottercli -run 'TestLabel' -count=1; go test ./cmd/otter -run 'TestLabel' -count=1; go test ./internal/ottercli -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli ./cmd/otter -count=1
- [2026-02-08 14:20 MST] Issue #356 | Commit 5a8e2e8 | closed | Added reusable frontend label primitives (`LabelPill`, `LabelPicker`, `LabelFilter`) with search/select/create/remove component coverage for label UX foundations | Tests: cd web && npm test -- src/components/LabelPill.test.tsx src/components/LabelPicker.test.tsx src/components/LabelFilter.test.tsx --run; cd web && npm run build:typecheck (pre-existing unrelated failure: `src/pages/AgentDetailPage.test.tsx` uses `.at()` with ES2020 target)
- [2026-02-08 14:24 MST] Issue #357 | Commit 99c3678 | closed | Enhanced `ProjectsPage` with `LabelPill` rendering on cards and multi-label `LabelFilter` controls wired to repeated `label` query params for AND-filtered project fetches | Tests: cd web && npm test -- src/pages/ProjectsPage.test.tsx --run; cd web && npm test -- src/components/LabelPill.test.tsx src/components/LabelPicker.test.tsx src/components/LabelFilter.test.tsx src/pages/ProjectsPage.test.tsx --run
- [2026-02-08 14:30 MST] Issue #358 | Commit d76a3cd | closed | Added issue-surface label UX: `ProjectIssuesList` label pills + label filtering and `IssueThreadPanel` inline label add/remove/create controls with lazy label catalog loading and optimistic updates | Tests: cd web && npm test -- src/components/project/ProjectIssuesList.test.tsx src/components/project/IssueThreadPanel.test.tsx --run; cd web && npm test -- src/components/LabelPill.test.tsx src/components/LabelPicker.test.tsx src/components/LabelFilter.test.tsx src/pages/ProjectsPage.test.tsx src/components/project/ProjectIssuesList.test.tsx src/components/project/IssueThreadPanel.test.tsx --run
- [2026-02-08 14:36 MST] Issue #359 | Commit ef5f5e4 | closed | Added settings label management workflows (create/edit/recolor/delete) and backend preset label seeding path (`seed=true` + `EnsurePresetLabels`) with store/API/frontend coverage | Tests: go test ./internal/store -run 'TestLabelPresetSeed' -count=1; go test ./internal/api -run 'TestLabelsHandler' -count=1; cd web && npm test -- src/pages/SettingsPage.test.tsx --run; cd web && npm test -- src/components/LabelPicker.test.tsx src/components/project/IssueThreadPanel.test.tsx src/components/project/ProjectIssuesList.test.tsx src/pages/ProjectsPage.test.tsx src/pages/SettingsPage.test.tsx --run
- [2026-02-08 14:36 MST] Issue spec #010 | Commit ef5f5e4 | moved-to-needs-review | Closed all planned implementation issues (#350-#359) and moved spec from `02-in-progress` to `03-needs-review`; awaiting external reviewer sign-off before any move to `05-completed` | Tests: n/a
