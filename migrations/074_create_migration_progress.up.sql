CREATE TABLE IF NOT EXISTS migration_progress (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    migration_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN (
        'pending',
        'running',
        'paused',
        'completed',
        'failed'
    )),
    total_items INT,
    processed_items INT NOT NULL DEFAULT 0,
    failed_items INT NOT NULL DEFAULT 0,
    current_label TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS migration_progress_org_type_uidx
    ON migration_progress (org_id, migration_type);

CREATE INDEX IF NOT EXISTS migration_progress_org_status_idx
    ON migration_progress (org_id, status, updated_at DESC);

DROP TRIGGER IF EXISTS migration_progress_updated_at_trg ON migration_progress;
CREATE TRIGGER migration_progress_updated_at_trg
BEFORE UPDATE ON migration_progress
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE migration_progress ENABLE ROW LEVEL SECURITY;
ALTER TABLE migration_progress FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS migration_progress_org_isolation ON migration_progress;
CREATE POLICY migration_progress_org_isolation ON migration_progress
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
