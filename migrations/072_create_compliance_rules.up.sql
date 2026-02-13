CREATE TABLE IF NOT EXISTS compliance_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    check_instruction TEXT NOT NULL,
    category TEXT NOT NULL CHECK (category IN (
        'code_quality',
        'security',
        'scope',
        'style',
        'process',
        'technical'
    )),
    severity TEXT NOT NULL DEFAULT 'required' CHECK (severity IN (
        'required',
        'recommended',
        'informational'
    )),
    enabled BOOLEAN NOT NULL DEFAULT true,
    source_conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS compliance_rules_org_idx
    ON compliance_rules (org_id, enabled)
    WHERE enabled = true;

CREATE INDEX IF NOT EXISTS compliance_rules_project_idx
    ON compliance_rules (project_id, enabled)
    WHERE project_id IS NOT NULL AND enabled = true;

DROP TRIGGER IF EXISTS compliance_rules_updated_at_trg ON compliance_rules;
CREATE TRIGGER compliance_rules_updated_at_trg
BEFORE UPDATE ON compliance_rules
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE compliance_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE compliance_rules FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS compliance_rules_org_isolation ON compliance_rules;
CREATE POLICY compliance_rules_org_isolation ON compliance_rules
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
