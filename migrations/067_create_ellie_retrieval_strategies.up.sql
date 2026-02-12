CREATE TABLE IF NOT EXISTS ellie_retrieval_strategies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version INT NOT NULL CHECK (version > 0),
    name TEXT NOT NULL,
    rules JSONB NOT NULL DEFAULT '{}'::jsonb,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, version)
);

CREATE INDEX IF NOT EXISTS ellie_retrieval_strategies_org_active_idx
    ON ellie_retrieval_strategies (org_id, is_active, version DESC)
    WHERE is_active = true;

CREATE TRIGGER ellie_retrieval_strategies_updated_at_trg
BEFORE UPDATE ON ellie_retrieval_strategies
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE ellie_retrieval_strategies ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_retrieval_strategies FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_retrieval_strategies_org_isolation ON ellie_retrieval_strategies;
CREATE POLICY ellie_retrieval_strategies_org_isolation ON ellie_retrieval_strategies
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
