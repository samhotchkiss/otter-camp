CREATE TABLE project_repo_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repository_full_name TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT 'main',
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    sync_mode TEXT NOT NULL DEFAULT 'sync' CHECK (sync_mode IN ('sync', 'push')),
    auto_sync BOOLEAN NOT NULL DEFAULT TRUE,
    last_synced_sha TEXT,
    last_synced_at TIMESTAMPTZ,
    conflict_state TEXT NOT NULL DEFAULT 'none' CHECK (conflict_state IN ('none', 'needs_decision', 'resolved')),
    conflict_details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id)
);

CREATE INDEX project_repo_bindings_org_idx ON project_repo_bindings(org_id);
CREATE INDEX project_repo_bindings_repo_idx ON project_repo_bindings(repository_full_name);
CREATE INDEX project_repo_bindings_conflict_idx ON project_repo_bindings(conflict_state) WHERE conflict_state <> 'none';

CREATE TRIGGER project_repo_bindings_updated_at_trg
BEFORE UPDATE ON project_repo_bindings
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE project_repo_active_branches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    branch_name TEXT NOT NULL,
    last_synced_sha TEXT,
    last_synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, branch_name)
);

CREATE INDEX project_repo_active_branches_org_idx ON project_repo_active_branches(org_id);
CREATE INDEX project_repo_active_branches_project_idx ON project_repo_active_branches(project_id);

CREATE TRIGGER project_repo_active_branches_updated_at_trg
BEFORE UPDATE ON project_repo_active_branches
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();
