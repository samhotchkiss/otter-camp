CREATE TABLE project_issue_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'collaborator')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    removed_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX project_issue_participants_active_unique_idx
    ON project_issue_participants (issue_id, agent_id)
    WHERE removed_at IS NULL;

CREATE UNIQUE INDEX project_issue_participants_owner_unique_idx
    ON project_issue_participants (issue_id)
    WHERE role = 'owner' AND removed_at IS NULL;

CREATE INDEX project_issue_participants_issue_active_idx
    ON project_issue_participants (issue_id, role, joined_at)
    WHERE removed_at IS NULL;

ALTER TABLE project_issue_participants ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issue_participants FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_issue_participants_org_isolation ON project_issue_participants;
CREATE POLICY project_issue_participants_org_isolation ON project_issue_participants
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

CREATE TABLE project_issue_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    author_agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    body TEXT NOT NULL CHECK (char_length(trim(body)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX project_issue_comments_issue_created_idx
    ON project_issue_comments (issue_id, created_at, id);

CREATE TRIGGER project_issue_comments_updated_at_trg
BEFORE UPDATE ON project_issue_comments
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE project_issue_comments ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issue_comments FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_issue_comments_org_isolation ON project_issue_comments;
CREATE POLICY project_issue_comments_org_isolation ON project_issue_comments
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
