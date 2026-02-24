CREATE TABLE agent_skill_attachment (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    skill_id uuid NOT NULL REFERENCES skill(id) ON DELETE CASCADE,
    priority integer NOT NULL DEFAULT 100,
    attached_by_type text NOT NULL CHECK (attached_by_type IN ('human_user', 'agent', 'system')),
    attached_by_id uuid,
    is_active boolean NOT NULL DEFAULT true,
    attached_at timestamptz NOT NULL DEFAULT now(),
    deactivated_at timestamptz,
    CONSTRAINT agent_skill_attachment_agent_skill_unique UNIQUE (agent_id, skill_id)
);

CREATE INDEX agent_skill_attachment_agent_active_priority_idx
    ON agent_skill_attachment (agent_id, is_active, priority);

CREATE INDEX agent_skill_attachment_skill_idx
    ON agent_skill_attachment (skill_id);
