-- Normalize project assignment role storage from legacy "pm" to canonical "project_manager".
WITH ranked_pm AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY project_id
            ORDER BY assigned_at DESC, id DESC
        ) AS rank_in_project
    FROM agent_project_assignment
    WHERE is_active = true
      AND role IN ('pm', 'project_manager')
)
UPDATE agent_project_assignment AS assignment
SET
    is_active = false,
    deactivated_at = COALESCE(assignment.deactivated_at, now())
FROM ranked_pm
WHERE assignment.id = ranked_pm.id
  AND ranked_pm.rank_in_project > 1;

UPDATE agent_project_assignment
SET role = 'project_manager'
WHERE role = 'pm';

UPDATE agent
SET
    agent_class = 'staff',
    temp_project_id = NULL,
    temp_ttl_seconds = NULL,
    temp_expires_at = NULL,
    updated_at = now()
WHERE agent_class = 'temp'
  AND EXISTS (
      SELECT 1
      FROM agent_project_assignment AS assignment
      WHERE assignment.agent_id = agent.id
        AND assignment.role = 'project_manager'
        AND assignment.is_active = true
  );

ALTER TABLE agent_project_assignment
    DROP CONSTRAINT IF EXISTS agent_project_assignment_role_check;

ALTER TABLE agent_project_assignment
    ADD CONSTRAINT agent_project_assignment_role_check
    CHECK (role IN ('project_manager', 'worker', 'reviewer', 'observer'));

DROP INDEX IF EXISTS agent_project_assignment_active_pm_unique_idx;

CREATE UNIQUE INDEX agent_project_assignment_active_pm_unique_idx
    ON agent_project_assignment (project_id)
    WHERE role = 'project_manager' AND is_active = true;

CREATE OR REPLACE FUNCTION enforce_project_manager_staff_class()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    assigned_class text;
BEGIN
    IF NEW.role <> 'project_manager' THEN
        RETURN NEW;
    END IF;

    SELECT agent_class
    INTO assigned_class
    FROM agent
    WHERE id = NEW.agent_id;

    IF assigned_class IS DISTINCT FROM 'staff' THEN
        RAISE EXCEPTION 'project_manager assignments require staff-class agents'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS agent_project_assignment_project_manager_staff_chk ON agent_project_assignment;

CREATE TRIGGER agent_project_assignment_project_manager_staff_chk
BEFORE INSERT OR UPDATE OF agent_id, role
ON agent_project_assignment
FOR EACH ROW
EXECUTE FUNCTION enforce_project_manager_staff_class();

UPDATE tool_definition
SET
    description = 'Assign an agent to a project with a role (project_manager, worker, reviewer, observer)',
    input_schema = '{
      "type": "object",
      "required": ["agent_id", "project_id", "role"],
      "additionalProperties": false,
      "properties": {
        "agent_id": {"type": "string", "format": "uuid"},
        "project_id": {"type": "string", "format": "uuid"},
        "role": {"type": "string", "enum": ["project_manager", "worker", "reviewer", "observer"]}
      }
    }'::jsonb
WHERE name = 'agent.assign_project';
