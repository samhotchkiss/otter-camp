ALTER TABLE agent_project_assignment
    DROP CONSTRAINT IF EXISTS agent_project_assignment_agent_project_unique;

ALTER TABLE agent_project_assignment
    ADD CONSTRAINT agent_project_assignment_agent_project_role_unique
    UNIQUE (agent_id, project_id, role);
