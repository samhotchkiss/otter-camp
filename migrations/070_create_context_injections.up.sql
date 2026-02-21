CREATE TABLE IF NOT EXISTS context_injections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    memory_id UUID NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    injected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (room_id, memory_id)
);

CREATE INDEX IF NOT EXISTS idx_context_injections_room
    ON context_injections (room_id, injected_at DESC);

ALTER TABLE context_injections ENABLE ROW LEVEL SECURITY;
ALTER TABLE context_injections FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS context_injections_org_isolation ON context_injections;
CREATE POLICY context_injections_org_isolation ON context_injections
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
