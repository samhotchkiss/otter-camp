CREATE TABLE IF NOT EXISTS ellie_dedup_reviewed (
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    memory_id_a UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    memory_id_b UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    decision TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    reviewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, memory_id_a, memory_id_b),
    CHECK (memory_id_a <> memory_id_b),
    CHECK (memory_id_a < memory_id_b)
);

CREATE INDEX IF NOT EXISTS ellie_dedup_reviewed_org_reviewed_idx
    ON ellie_dedup_reviewed (org_id, reviewed_at DESC);

ALTER TABLE ellie_dedup_reviewed ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_dedup_reviewed FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_dedup_reviewed_org_isolation ON ellie_dedup_reviewed;
CREATE POLICY ellie_dedup_reviewed_org_isolation ON ellie_dedup_reviewed
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

CREATE TABLE IF NOT EXISTS ellie_dedup_cursors (
    org_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    last_cluster_key TEXT,
    processed_clusters INT NOT NULL DEFAULT 0,
    total_clusters INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DROP TRIGGER IF EXISTS ellie_dedup_cursors_updated_at_trg ON ellie_dedup_cursors;
CREATE TRIGGER ellie_dedup_cursors_updated_at_trg
BEFORE UPDATE ON ellie_dedup_cursors
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE ellie_dedup_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_dedup_cursors FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_dedup_cursors_org_isolation ON ellie_dedup_cursors;
CREATE POLICY ellie_dedup_cursors_org_isolation ON ellie_dedup_cursors
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
