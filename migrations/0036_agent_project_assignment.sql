CREATE TABLE agent_project_assignment (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('pm', 'worker', 'reviewer', 'observer')),
    assigned_by_type text NOT NULL CHECK (assigned_by_type IN ('human_user', 'agent', 'system')),
    assigned_by_id uuid,
    is_active boolean NOT NULL DEFAULT true,
    assigned_at timestamptz NOT NULL DEFAULT now(),
    deactivated_at timestamptz,
    CONSTRAINT agent_project_assignment_agent_project_unique UNIQUE (agent_id, project_id)
);

CREATE UNIQUE INDEX agent_project_assignment_active_pm_unique_idx
    ON agent_project_assignment (project_id)
    WHERE role = 'pm' AND is_active = true;

CREATE INDEX agent_project_assignment_project_role_active_idx
    ON agent_project_assignment (project_id, role, is_active);

CREATE INDEX agent_project_assignment_agent_active_idx
    ON agent_project_assignment (agent_id, is_active);
