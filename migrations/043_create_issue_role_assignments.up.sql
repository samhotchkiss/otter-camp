CREATE TABLE issue_role_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('planner', 'worker', 'reviewer')),
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, role)
);

CREATE INDEX issue_role_assignments_org_project_idx
    ON issue_role_assignments (org_id, project_id, role);

CREATE TRIGGER issue_role_assignments_updated_at_trg
BEFORE UPDATE ON issue_role_assignments
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE issue_role_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE issue_role_assignments FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS issue_role_assignments_org_isolation ON issue_role_assignments;
CREATE POLICY issue_role_assignments_org_isolation ON issue_role_assignments
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
