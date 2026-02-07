CREATE TABLE knowledge_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    tags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX knowledge_entries_org_updated_idx
    ON knowledge_entries (org_id, updated_at DESC);

ALTER TABLE knowledge_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_entries FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS knowledge_entries_org_isolation ON knowledge_entries;
CREATE POLICY knowledge_entries_org_isolation ON knowledge_entries
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
