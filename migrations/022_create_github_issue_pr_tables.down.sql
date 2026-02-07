DROP INDEX IF EXISTS project_github_pull_requests_merged_idx;
DROP INDEX IF EXISTS project_github_pull_requests_issue_id_idx;
DROP INDEX IF EXISTS project_github_pull_requests_repo_number_idx;
DROP INDEX IF EXISTS project_github_pull_requests_org_project_idx;
DROP TABLE IF EXISTS project_github_pull_requests;

DROP INDEX IF EXISTS project_github_issues_repo_number_idx;
DROP INDEX IF EXISTS project_github_issues_org_project_idx;
DROP TABLE IF EXISTS project_github_issues;
