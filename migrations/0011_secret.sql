CREATE TABLE secret (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    display_name text NOT NULL,
    description text,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
    key_version integer NOT NULL DEFAULT 1 CHECK (key_version > 0),
    created_by_type text NOT NULL CHECK (created_by_type IN ('human', 'agent', 'system')),
    created_by_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug)
);

CREATE INDEX secret_organization_id_idx
    ON secret (organization_id);

CREATE TRIGGER secret_set_updated_at
BEFORE UPDATE ON secret
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
