CREATE TABLE IF NOT EXISTS openclaw_agent_memory_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    agent_slug TEXT NOT NULL,
    source_file_path TEXT,
    memory_md TEXT NOT NULL,
    memory_md_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS openclaw_agent_memory_snapshots_org_slug_uidx
    ON openclaw_agent_memory_snapshots (org_id, agent_slug);

CREATE INDEX IF NOT EXISTS openclaw_agent_memory_snapshots_org_updated_idx
    ON openclaw_agent_memory_snapshots (org_id, updated_at DESC);

DROP TRIGGER IF EXISTS openclaw_agent_memory_snapshots_updated_at_trg ON openclaw_agent_memory_snapshots;
CREATE TRIGGER openclaw_agent_memory_snapshots_updated_at_trg
BEFORE UPDATE ON openclaw_agent_memory_snapshots
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE openclaw_agent_memory_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE openclaw_agent_memory_snapshots FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS openclaw_agent_memory_snapshots_org_isolation ON openclaw_agent_memory_snapshots;
CREATE POLICY openclaw_agent_memory_snapshots_org_isolation ON openclaw_agent_memory_snapshots
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
