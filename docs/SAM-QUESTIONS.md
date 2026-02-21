# Sam Questions

> Summary: Questions requiring product or operational decisions discovered during docs consolidation.
> Last updated: 2026-02-16
> Audience: Sam + implementation agents.

1. Canonical hosted URL pattern
- Should docs and UI standardize on `{site}.otter.camp` as the default user-facing pattern, or keep explicit hostnames like `sam.otter.camp` in examples?

2. Notification API contract
- Do you want us to implement full `/api/notifications` endpoints (matching frontend), or rewire frontend to current `/api/settings/notifications` behavior and simplify notification UX?

3. Duplicate file cleanup scope
- Do you want an immediate cleanup PR for duplicate `* 2`, `* 3`, etc. files/folders in repo root/web tree, or should we first produce a strict keep/delete proposal for review?

4. Legacy design artifact policy
- Should `docs/prototype` and `docs/wireframes` remain in this repo as historical references, or move to an archive path outside canonical docs?

5. Production CORS policy
- Should we lock production CORS to explicit allowlists now, while keeping permissive local defaults?

6. Memory taxonomy unification
- Do you want agent-memory and Ellie-memory kind/status taxonomies merged into one contract, or intentionally kept separate?

7. Bridge-first expectation in hosted mode
- In hosted environments, should bridge disconnection block certain actions by policy, or stay as a warning/degraded mode only?

8. Gemini API key rotation needed
- Key `AIzaSyBzM9km4mqu1yGu3OO1vKomp0TS_ogq2Bw` was hardcoded in `scripts/generate-avatars.sh` as a default value.
- Stripped from script (now requires env var). Key should be rotated in Google Cloud console since it was committed to repo history.

## Change Log

- 2026-02-16: Added Gemini API key rotation reminder (#8).

- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
