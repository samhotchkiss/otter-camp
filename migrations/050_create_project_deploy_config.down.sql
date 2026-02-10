DROP POLICY IF EXISTS project_deploy_config_org_isolation ON project_deploy_config;
DROP TRIGGER IF EXISTS project_deploy_config_updated_at_trg ON project_deploy_config;
DROP INDEX IF EXISTS project_deploy_config_org_project_idx;
DROP TABLE IF EXISTS project_deploy_config;
