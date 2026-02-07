CREATE TABLE project_issue_review_checkpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    last_review_commit_sha TEXT NOT NULL CHECK (length(btrim(last_review_commit_sha)) > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (issue_id)
);

CREATE INDEX project_issue_review_checkpoints_org_issue_idx
    ON project_issue_review_checkpoints (org_id, issue_id);

CREATE TRIGGER project_issue_review_checkpoints_updated_at_trg
BEFORE UPDATE ON project_issue_review_checkpoints
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE project_issue_review_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issue_review_checkpoints FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_issue_review_checkpoints_org_isolation ON project_issue_review_checkpoints;
CREATE POLICY project_issue_review_checkpoints_org_isolation ON project_issue_review_checkpoints
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
