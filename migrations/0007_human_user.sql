CREATE TABLE human_user (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    email text NOT NULL,
    display_name text NOT NULL,
    password_hash text,
    role text NOT NULL CHECK (role IN ('admin', 'member')),
    is_active boolean NOT NULL DEFAULT true,
    failed_login_attempts integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    last_login_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    settings jsonb NOT NULL DEFAULT '{}',
    UNIQUE (organization_id, email)
);

CREATE TRIGGER human_user_set_updated_at
BEFORE UPDATE ON human_user
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
