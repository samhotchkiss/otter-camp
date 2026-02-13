CREATE TABLE IF NOT EXISTS ellie_retrieval_quality_events (
    id BIGSERIAL PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    room_id UUID REFERENCES rooms(id) ON DELETE SET NULL,
    query TEXT NOT NULL DEFAULT '',
    tier_used SMALLINT NOT NULL CHECK (tier_used BETWEEN 1 AND 5),
    injected_count INT NOT NULL DEFAULT 0 CHECK (injected_count >= 0),
    referenced_count INT NOT NULL DEFAULT 0 CHECK (referenced_count >= 0),
    missed_count INT NOT NULL DEFAULT 0 CHECK (missed_count >= 0),
    no_information BOOLEAN NOT NULL DEFAULT false,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ellie_retrieval_quality_events_org_project_idx
    ON ellie_retrieval_quality_events (org_id, project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ellie_retrieval_quality_events_org_created_idx
    ON ellie_retrieval_quality_events (org_id, created_at DESC);

ALTER TABLE ellie_retrieval_quality_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_retrieval_quality_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_retrieval_quality_events_org_isolation ON ellie_retrieval_quality_events;
CREATE POLICY ellie_retrieval_quality_events_org_isolation ON ellie_retrieval_quality_events
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
