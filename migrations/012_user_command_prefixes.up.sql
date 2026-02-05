CREATE TABLE user_command_prefixes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    prefix TEXT NOT NULL,
    command TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, prefix)
);

CREATE INDEX user_command_prefixes_user_id_idx ON user_command_prefixes(user_id);
CREATE INDEX user_command_prefixes_org_id_idx ON user_command_prefixes(org_id);

CREATE TRIGGER user_command_prefixes_updated_at_trg
BEFORE UPDATE ON user_command_prefixes
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();
