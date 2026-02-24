CREATE TABLE domain_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    seq bigserial NOT NULL,
    event_type text NOT NULL,
    actor_type text NOT NULL CHECK (actor_type IN ('human', 'agent', 'system', 'supervisor')),
    actor_id uuid,
    payload jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (actor_type IN ('system', 'supervisor') AND actor_id IS NULL)
        OR (actor_type IN ('human', 'agent') AND actor_id IS NOT NULL)
    )
);

CREATE INDEX domain_event_org_seq_idx
    ON domain_event (organization_id, seq);

CREATE INDEX domain_event_org_type_created_idx
    ON domain_event (organization_id, event_type, created_at);

CREATE INDEX domain_event_seq_idx
    ON domain_event (seq);
