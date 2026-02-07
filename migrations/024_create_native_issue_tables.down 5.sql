DROP POLICY IF EXISTS project_issue_sync_checkpoints_org_isolation ON project_issue_sync_checkpoints;
DROP INDEX IF EXISTS project_issue_sync_checkpoints_org_project_idx;
DROP TABLE IF EXISTS project_issue_sync_checkpoints;

DROP POLICY IF EXISTS project_issue_github_links_org_isolation ON project_issue_github_links;
DROP INDEX IF EXISTS project_issue_github_links_repo_number_idx;
DROP INDEX IF EXISTS project_issue_github_links_issue_idx;
DROP TABLE IF EXISTS project_issue_github_links;

DROP POLICY IF EXISTS project_issues_org_isolation ON project_issues;
DROP TRIGGER IF EXISTS project_issues_updated_at_trg ON project_issues;
DROP INDEX IF EXISTS project_issues_project_origin_idx;
DROP INDEX IF EXISTS project_issues_org_project_state_origin_idx;
DROP TABLE IF EXISTS project_issues;
