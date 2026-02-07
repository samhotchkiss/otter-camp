CREATE TABLE IF NOT EXISTS connection_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'info'
        CHECK (severity IN ('info', 'warning', 'error')),
    message TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_connection_events_org_created
    ON connection_events (org_id, created_at DESC);

ALTER TABLE connection_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE connection_events FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS connection_events_org_isolation ON connection_events;
CREATE POLICY connection_events_org_isolation ON connection_events
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
