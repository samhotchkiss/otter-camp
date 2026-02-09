CREATE TABLE project_deploy_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    deploy_method TEXT NOT NULL DEFAULT 'none'
        CHECK (deploy_method IN ('none', 'github_push', 'cli_command')),
    github_repo_url TEXT,
    github_branch TEXT NOT NULL DEFAULT 'main',
    cli_command TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id)
);

CREATE INDEX project_deploy_config_org_project_idx
    ON project_deploy_config (org_id, project_id);

CREATE TRIGGER project_deploy_config_updated_at_trg
BEFORE UPDATE ON project_deploy_config
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE project_deploy_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_deploy_config FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_deploy_config_org_isolation ON project_deploy_config;
CREATE POLICY project_deploy_config_org_isolation ON project_deploy_config
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
