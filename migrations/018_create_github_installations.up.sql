CREATE TABLE github_installations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    installation_id BIGINT NOT NULL UNIQUE,
    account_login TEXT NOT NULL,
    account_type TEXT NOT NULL,
    permissions JSONB NOT NULL DEFAULT '{}'::jsonb,
    connected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id)
);

CREATE INDEX github_installations_org_id_idx ON github_installations(org_id);
CREATE INDEX github_installations_connected_at_idx ON github_installations(connected_at DESC);

CREATE TRIGGER github_installations_updated_at_trg
BEFORE UPDATE ON github_installations
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();
