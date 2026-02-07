CREATE TABLE project_issue_review_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    document_path TEXT NOT NULL,
    review_commit_sha TEXT NOT NULL,
    reviewer_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    addressed_in_commit_sha TEXT,
    addressed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (issue_id, review_commit_sha)
);

CREATE INDEX project_issue_review_versions_issue_created_idx
    ON project_issue_review_versions (issue_id, created_at DESC, review_commit_sha DESC);

CREATE TRIGGER project_issue_review_versions_updated_at_trg
BEFORE UPDATE ON project_issue_review_versions
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE project_issue_review_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issue_review_versions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_issue_review_versions_org_isolation ON project_issue_review_versions;
CREATE POLICY project_issue_review_versions_org_isolation ON project_issue_review_versions
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
