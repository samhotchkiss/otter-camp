ALTER TABLE project_flow_template_steps
    ADD COLUMN node_type TEXT NOT NULL DEFAULT 'work',
    ADD COLUMN objective TEXT,
    ADD COLUMN actor_type TEXT NOT NULL DEFAULT 'role',
    ADD COLUMN actor_value TEXT,
    ADD COLUMN next_step_key TEXT,
    ADD COLUMN reject_step_key TEXT;

WITH ordered AS (
    SELECT
        id,
        role,
        step_key,
        LAG(step_key) OVER (PARTITION BY flow_template_id ORDER BY step_order) AS prev_step_key,
        LEAD(step_key) OVER (PARTITION BY flow_template_id ORDER BY step_order) AS next_key
    FROM project_flow_template_steps
)
UPDATE project_flow_template_steps AS steps
SET
    node_type = CASE
        WHEN ordered.role = 'reviewer' THEN 'review'
        ELSE 'work'
    END,
    objective = COALESCE(NULLIF(BTRIM(steps.label), ''), steps.step_key),
    actor_type = CASE
        WHEN ordered.role IN ('planner', 'worker', 'reviewer') THEN 'role'
        WHEN ordered.role = 'human' THEN 'human'
        ELSE 'agent'
    END,
    actor_value = CASE
        WHEN ordered.role IN ('planner', 'worker', 'reviewer') THEN ordered.role
        ELSE NULL
    END,
    next_step_key = ordered.next_key,
    reject_step_key = CASE
        WHEN ordered.role = 'reviewer' THEN ordered.prev_step_key
        ELSE NULL
    END
FROM ordered
WHERE steps.id = ordered.id;

ALTER TABLE project_flow_template_steps
    ALTER COLUMN objective SET NOT NULL;

ALTER TABLE project_flow_template_steps
    ADD CONSTRAINT project_flow_template_steps_node_type_check
    CHECK (node_type IN ('work', 'review')),
    ADD CONSTRAINT project_flow_template_steps_actor_type_check
    CHECK (actor_type IN ('role', 'project_manager', 'human', 'agent'));

CREATE INDEX project_flow_template_steps_next_key_idx
    ON project_flow_template_steps (flow_template_id, next_step_key);

CREATE INDEX project_flow_template_steps_reject_key_idx
    ON project_flow_template_steps (flow_template_id, reject_step_key);

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_work_status_check;

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_work_status_check
    CHECK (work_status IN ('queued', 'in_progress', 'blocked', 'on_hold', 'review', 'done', 'cancelled'));

CREATE TABLE project_issue_flow_blockers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    raised_by_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    assigned_project_manager_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    escalation_level TEXT NOT NULL DEFAULT 'project_manager'
        CHECK (escalation_level IN ('project_manager', 'human')),
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'resolved', 'cancelled')),
    summary TEXT NOT NULL,
    detail TEXT,
    resolution_note TEXT,
    raised_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    escalated_to_human_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX project_issue_flow_blockers_pending_idx
    ON project_issue_flow_blockers (org_id, escalation_level, status, raised_at DESC);

CREATE INDEX project_issue_flow_blockers_issue_status_idx
    ON project_issue_flow_blockers (issue_id, status, raised_at DESC);

ALTER TABLE project_issue_flow_blockers ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issue_flow_blockers FORCE ROW LEVEL SECURITY;

CREATE POLICY project_issue_flow_blockers_org_isolation ON project_issue_flow_blockers
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
