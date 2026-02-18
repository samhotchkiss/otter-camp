# Frontend Foundation (Spec 500)

This folder defines the shared design foundation for follow-up redesign specs.

## Canonical Tokens

- Use `--oc-*` variables from `design-tokens.css` as the single source of truth.
- Legacy variables (`--bg`, `--surface`, `--text`, etc.) are compatibility aliases and should not be the default choice for new code.

## Shared Primitives

Use classes from `primitives.css` before creating page-local style patterns:

- `oc-panel`: framed container for loading/empty/error boxes
- `oc-card` + `oc-card-interactive`: card surfaces and hover affordance
- `oc-chip` (+ variants): inline status/context pills
- `oc-status-dot` (+ variants): compact status indicator
- `oc-toolbar`: horizontal control group layout
- `oc-toolbar-input`: input-like toolbar control
- `oc-toolbar-button` / `oc-toolbar-button--primary`: toolbar action controls

## Migration Rules

Do:
- Prefer `--oc-*` tokens in new CSS.
- Layer primitive classes onto existing markup during incremental migrations.
- Keep legacy class hooks when tests or existing pages depend on them.
- Add/extend tests when introducing a new primitive dependency in a component.

Don't:
- Introduce new one-off color/spacing values when a token already exists.
- Replace page behavior while doing style-foundation work.
- Remove legacy aliases until all dependent pages have been migrated.
- Add page-specific utility classes to global files when a primitive solves it.
