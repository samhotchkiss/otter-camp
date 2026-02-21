DROP POLICY IF EXISTS compliance_rules_org_isolation ON compliance_rules;
DROP TRIGGER IF EXISTS compliance_rules_updated_at_trg ON compliance_rules;
DROP INDEX IF EXISTS compliance_rules_project_idx;
DROP INDEX IF EXISTS compliance_rules_org_idx;
DROP TABLE IF EXISTS compliance_rules;
