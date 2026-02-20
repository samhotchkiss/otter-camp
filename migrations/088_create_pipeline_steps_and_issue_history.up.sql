CREATE TABLE pipeline_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    step_number INTEGER NOT NULL CHECK (step_number > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    description TEXT NOT NULL DEFAULT '',
    assigned_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    step_type TEXT NOT NULL CHECK (step_type IN ('agent_work', 'agent_review', 'human_review')),
    auto_advance BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, step_number)
);

CREATE INDEX pipeline_steps_org_project_step_idx
    ON pipeline_steps (org_id, project_id, step_number);

CREATE TRIGGER pipeline_steps_updated_at_trg
BEFORE UPDATE ON pipeline_steps
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE pipeline_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE pipeline_steps FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS pipeline_steps_org_isolation ON pipeline_steps;
CREATE POLICY pipeline_steps_org_isolation ON pipeline_steps
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE project_issues
    ADD COLUMN current_pipeline_step_id UUID REFERENCES pipeline_steps(id) ON DELETE SET NULL,
    ADD COLUMN pipeline_started_at TIMESTAMPTZ,
    ADD COLUMN pipeline_completed_at TIMESTAMPTZ;

CREATE INDEX project_issues_project_current_pipeline_step_idx
    ON project_issues (project_id, current_pipeline_step_id, updated_at DESC);

CREATE TABLE issue_pipeline_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    step_id UUID NOT NULL REFERENCES pipeline_steps(id) ON DELETE RESTRICT,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    result TEXT NOT NULL CHECK (result IN ('completed', 'rejected', 'skipped')),
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX issue_pipeline_history_org_issue_started_idx
    ON issue_pipeline_history (org_id, issue_id, started_at DESC);

CREATE INDEX issue_pipeline_history_step_idx
    ON issue_pipeline_history (step_id, created_at DESC);

CREATE TRIGGER issue_pipeline_history_updated_at_trg
BEFORE UPDATE ON issue_pipeline_history
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE issue_pipeline_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE issue_pipeline_history FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS issue_pipeline_history_org_isolation ON issue_pipeline_history;
CREATE POLICY issue_pipeline_history_org_isolation ON issue_pipeline_history
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
