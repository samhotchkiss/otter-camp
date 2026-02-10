ALTER TABLE agent_sync_state DISABLE ROW LEVEL SECURITY;
ALTER TABLE agent_sync_state NO FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agent_sync_state_org_isolation ON agent_sync_state;

DELETE FROM agent_sync_state a
USING agent_sync_state b
WHERE a.id = b.id
  AND (
    a.updated_at < b.updated_at
    OR (a.updated_at = b.updated_at AND a.org_id::text > b.org_id::text)
  );

DROP INDEX IF EXISTS idx_agent_sync_state_org_status;
DROP INDEX IF EXISTS idx_agent_sync_state_org_id;

ALTER TABLE agent_sync_state
    DROP CONSTRAINT IF EXISTS agent_sync_state_pkey;

ALTER TABLE agent_sync_state
    ADD CONSTRAINT agent_sync_state_pkey PRIMARY KEY (id);

ALTER TABLE agent_sync_state
    DROP COLUMN IF EXISTS org_id;
