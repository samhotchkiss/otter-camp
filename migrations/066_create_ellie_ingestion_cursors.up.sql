CREATE TABLE IF NOT EXISTS ellie_ingestion_cursors (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    source_type TEXT NOT NULL CHECK (source_type IN ('room', 'session_log')),
    source_id TEXT NOT NULL,
    last_message_id UUID,
    last_message_created_at TIMESTAMPTZ,
    file_path TEXT,
    file_offset BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, source_type, source_id)
);

CREATE INDEX IF NOT EXISTS ellie_ingestion_cursors_org_source_idx
    ON ellie_ingestion_cursors (org_id, source_type, source_id);

CREATE TRIGGER ellie_ingestion_cursors_updated_at_trg
BEFORE UPDATE ON ellie_ingestion_cursors
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE ellie_ingestion_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_ingestion_cursors FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_ingestion_cursors_org_isolation ON ellie_ingestion_cursors;
CREATE POLICY ellie_ingestion_cursors_org_isolation ON ellie_ingestion_cursors
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
