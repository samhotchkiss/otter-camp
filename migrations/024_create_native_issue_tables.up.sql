CREATE TABLE project_issues (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    issue_number BIGINT NOT NULL,
    title TEXT NOT NULL,
    body TEXT,
    state TEXT NOT NULL CHECK (state IN ('open', 'closed')),
    origin TEXT NOT NULL CHECK (origin IN ('local', 'github')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at TIMESTAMPTZ,
    UNIQUE (project_id, issue_number)
);

CREATE INDEX project_issues_org_project_state_origin_idx
    ON project_issues (org_id, project_id, state, origin, updated_at DESC);
CREATE INDEX project_issues_project_origin_idx
    ON project_issues (project_id, origin, updated_at DESC);

CREATE TRIGGER project_issues_updated_at_trg
BEFORE UPDATE ON project_issues
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE project_issues ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issues FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_issues_org_isolation ON project_issues;
CREATE POLICY project_issues_org_isolation ON project_issues
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

CREATE TABLE project_issue_github_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    repository_full_name TEXT NOT NULL,
    github_number BIGINT NOT NULL,
    github_url TEXT,
    github_state TEXT NOT NULL CHECK (github_state IN ('open', 'closed')),
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (issue_id),
    UNIQUE (org_id, repository_full_name, github_number)
);

CREATE INDEX project_issue_github_links_issue_idx
    ON project_issue_github_links (issue_id);
CREATE INDEX project_issue_github_links_repo_number_idx
    ON project_issue_github_links (repository_full_name, github_number);

ALTER TABLE project_issue_github_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issue_github_links FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_issue_github_links_org_isolation ON project_issue_github_links;
CREATE POLICY project_issue_github_links_org_isolation ON project_issue_github_links
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

CREATE TABLE project_issue_sync_checkpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repository_full_name TEXT NOT NULL,
    resource TEXT NOT NULL,
    cursor TEXT,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, repository_full_name, resource)
);

CREATE INDEX project_issue_sync_checkpoints_org_project_idx
    ON project_issue_sync_checkpoints (org_id, project_id, repository_full_name, resource);

ALTER TABLE project_issue_sync_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issue_sync_checkpoints FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_issue_sync_checkpoints_org_isolation ON project_issue_sync_checkpoints;
CREATE POLICY project_issue_sync_checkpoints_org_isolation ON project_issue_sync_checkpoints
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
