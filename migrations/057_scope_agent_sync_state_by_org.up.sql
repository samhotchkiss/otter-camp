ALTER TABLE agent_sync_state
    ADD COLUMN IF NOT EXISTS org_id UUID;

DELETE FROM agent_sync_state
WHERE org_id IS NULL;

ALTER TABLE agent_sync_state
    ALTER COLUMN org_id SET NOT NULL;

ALTER TABLE agent_sync_state
    DROP CONSTRAINT IF EXISTS agent_sync_state_pkey;

ALTER TABLE agent_sync_state
    ADD CONSTRAINT agent_sync_state_pkey PRIMARY KEY (org_id, id);

CREATE INDEX IF NOT EXISTS idx_agent_sync_state_org_id
    ON agent_sync_state (org_id);

CREATE INDEX IF NOT EXISTS idx_agent_sync_state_org_status
    ON agent_sync_state (org_id, status);

ALTER TABLE agent_sync_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_sync_state FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agent_sync_state_org_isolation ON agent_sync_state;
CREATE POLICY agent_sync_state_org_isolation ON agent_sync_state
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
