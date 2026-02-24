CREATE TABLE flow_node (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    flow_template_id uuid NOT NULL REFERENCES flow_template(id) ON DELETE CASCADE,
    display_name text NOT NULL,
    node_type text NOT NULL CHECK (node_type IN ('work', 'review', 'decision', 'parallel', 'merge')),
    position integer NOT NULL DEFAULT 0,
    actor_type text CHECK (actor_type IN ('agent', 'role', 'human')),
    actor_id uuid,
    next_node_id uuid REFERENCES flow_node(id) ON DELETE SET NULL,
    reject_node_id uuid REFERENCES flow_node(id) ON DELETE SET NULL,
    mcp_tools jsonb NOT NULL DEFAULT '[]'::jsonb,
    tool_domains jsonb NOT NULL DEFAULT '[]'::jsonb,
    requires_human_review boolean NOT NULL DEFAULT false,
    max_visits integer NOT NULL DEFAULT 10,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX flow_node_template_position_idx
    ON flow_node (flow_template_id, position);

CREATE INDEX flow_node_next_node_idx
    ON flow_node (next_node_id)
    WHERE next_node_id IS NOT NULL;

CREATE INDEX flow_node_reject_node_idx
    ON flow_node (reject_node_id)
    WHERE reject_node_id IS NOT NULL;

ALTER TABLE flow_template
    ADD CONSTRAINT fk_flow_template_start_node
    FOREIGN KEY (start_node_id)
    REFERENCES flow_node(id)
    ON DELETE SET NULL;

CREATE TRIGGER flow_node_set_updated_at
BEFORE UPDATE ON flow_node
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
