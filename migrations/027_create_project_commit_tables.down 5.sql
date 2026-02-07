DROP POLICY IF EXISTS project_commits_org_isolation ON project_commits;
DROP TRIGGER IF EXISTS project_commits_updated_at_trg ON project_commits;
DROP INDEX IF EXISTS project_commits_org_project_idx;
DROP INDEX IF EXISTS project_commits_project_sha_idx;
DROP INDEX IF EXISTS project_commits_project_branch_authored_idx;
DROP TABLE IF EXISTS project_commits;
