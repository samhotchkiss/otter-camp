CREATE TABLE git_access_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX git_access_tokens_org_user_idx ON git_access_tokens (org_id, user_id);
CREATE INDEX git_access_tokens_org_revoked_idx ON git_access_tokens (org_id, revoked_at);

CREATE TABLE git_access_token_projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_id UUID NOT NULL REFERENCES git_access_tokens(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    permission TEXT NOT NULL CHECK (permission IN ('read', 'write')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (token_id, project_id)
);

CREATE INDEX git_access_token_projects_token_idx ON git_access_token_projects (token_id);
CREATE INDEX git_access_token_projects_project_idx ON git_access_token_projects (project_id);
CREATE INDEX git_access_token_projects_org_project_idx ON git_access_token_projects (org_id, project_id);

CREATE TABLE git_ssh_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    public_key TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    UNIQUE (org_id, fingerprint)
);

CREATE INDEX git_ssh_keys_org_user_idx ON git_ssh_keys (org_id, user_id);
CREATE INDEX git_ssh_keys_org_revoked_idx ON git_ssh_keys (org_id, revoked_at);

CREATE TABLE git_ssh_key_projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key_id UUID NOT NULL REFERENCES git_ssh_keys(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    permission TEXT NOT NULL CHECK (permission IN ('read', 'write')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (key_id, project_id)
);

CREATE INDEX git_ssh_key_projects_key_idx ON git_ssh_key_projects (key_id);
CREATE INDEX git_ssh_key_projects_project_idx ON git_ssh_key_projects (project_id);
CREATE INDEX git_ssh_key_projects_org_project_idx ON git_ssh_key_projects (org_id, project_id);
