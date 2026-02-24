CREATE TABLE idempotency_key (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    key_hash text NOT NULL,
    request_hash text NOT NULL,
    response_status integer NOT NULL,
    response_body jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    UNIQUE (organization_id, key_hash)
);

CREATE INDEX idempotency_key_expires_at_idx
    ON idempotency_key (expires_at);
