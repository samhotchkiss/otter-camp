CREATE TABLE IF NOT EXISTS ellie_taxonomy_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    parent_id UUID REFERENCES ellie_taxonomy_nodes(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    display_name TEXT NOT NULL,
    description TEXT,
    depth INT NOT NULL DEFAULT 0 CHECK (depth >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, parent_id, slug)
);

CREATE UNIQUE INDEX IF NOT EXISTS ellie_taxonomy_nodes_org_root_slug_uidx
    ON ellie_taxonomy_nodes (org_id, slug)
    WHERE parent_id IS NULL;

CREATE INDEX IF NOT EXISTS ellie_taxonomy_nodes_org_parent_idx
    ON ellie_taxonomy_nodes (org_id, parent_id, slug);

CREATE INDEX IF NOT EXISTS ellie_taxonomy_nodes_parent_idx
    ON ellie_taxonomy_nodes (parent_id);

CREATE TABLE IF NOT EXISTS ellie_memory_taxonomy (
    memory_id UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    node_id UUID NOT NULL REFERENCES ellie_taxonomy_nodes(id) ON DELETE CASCADE,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 1.0 CHECK (confidence >= 0 AND confidence <= 1),
    classified_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (memory_id, node_id)
);

CREATE INDEX IF NOT EXISTS ellie_memory_taxonomy_node_idx
    ON ellie_memory_taxonomy (node_id, classified_at DESC);

CREATE INDEX IF NOT EXISTS ellie_memory_taxonomy_memory_idx
    ON ellie_memory_taxonomy (memory_id, classified_at DESC);

ALTER TABLE ellie_taxonomy_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_taxonomy_nodes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_taxonomy_nodes_org_isolation ON ellie_taxonomy_nodes;
CREATE POLICY ellie_taxonomy_nodes_org_isolation ON ellie_taxonomy_nodes
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE ellie_memory_taxonomy ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_memory_taxonomy FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_memory_taxonomy_org_isolation ON ellie_memory_taxonomy;
CREATE POLICY ellie_memory_taxonomy_org_isolation ON ellie_memory_taxonomy
    USING (
        EXISTS (
            SELECT 1
            FROM memories m
            WHERE m.id = memory_id
              AND m.org_id = current_org_id()
        )
        AND EXISTS (
            SELECT 1
            FROM ellie_taxonomy_nodes n
            WHERE n.id = node_id
              AND n.org_id = current_org_id()
        )
    )
    WITH CHECK (
        EXISTS (
            SELECT 1
            FROM memories m
            WHERE m.id = memory_id
              AND m.org_id = current_org_id()
        )
        AND EXISTS (
            SELECT 1
            FROM ellie_taxonomy_nodes n
            WHERE n.id = node_id
              AND n.org_id = current_org_id()
        )
    );
