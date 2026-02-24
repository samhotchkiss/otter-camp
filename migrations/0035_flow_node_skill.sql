CREATE TABLE flow_node_skill (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    flow_node_id uuid NOT NULL REFERENCES flow_node(id) ON DELETE CASCADE,
    skill_id uuid NOT NULL REFERENCES skill(id) ON DELETE CASCADE,
    position integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT flow_node_skill_unique UNIQUE (flow_node_id, skill_id)
);

CREATE INDEX flow_node_skill_node_position_idx
    ON flow_node_skill (flow_node_id, position);
