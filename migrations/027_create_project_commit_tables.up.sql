CREATE TABLE project_commits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repository_full_name TEXT NOT NULL,
    branch_name TEXT NOT NULL,
    sha TEXT NOT NULL,
    parent_sha TEXT,
    author_name TEXT NOT NULL,
    author_email TEXT,
    authored_at TIMESTAMPTZ NOT NULL,
    subject TEXT NOT NULL,
    body TEXT,
    message TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, repository_full_name, branch_name, sha)
);

CREATE INDEX project_commits_project_branch_authored_idx
    ON project_commits (project_id, branch_name, authored_at DESC, created_at DESC);
CREATE INDEX project_commits_project_sha_idx
    ON project_commits (project_id, sha);
CREATE INDEX project_commits_org_project_idx
    ON project_commits (org_id, project_id);

CREATE TRIGGER project_commits_updated_at_trg
BEFORE UPDATE ON project_commits
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE project_commits ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_commits FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_commits_org_isolation ON project_commits;
CREATE POLICY project_commits_org_isolation ON project_commits
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
