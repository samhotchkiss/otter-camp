DROP TRIGGER IF EXISTS project_repo_active_branches_updated_at_trg ON project_repo_active_branches;
DROP INDEX IF EXISTS project_repo_active_branches_project_idx;
DROP INDEX IF EXISTS project_repo_active_branches_org_idx;
DROP TABLE IF EXISTS project_repo_active_branches;

DROP TRIGGER IF EXISTS project_repo_bindings_updated_at_trg ON project_repo_bindings;
DROP INDEX IF EXISTS project_repo_bindings_conflict_idx;
DROP INDEX IF EXISTS project_repo_bindings_repo_idx;
DROP INDEX IF EXISTS project_repo_bindings_org_idx;
DROP TABLE IF EXISTS project_repo_bindings;
