CREATE TABLE audit_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    principal_type text NOT NULL CHECK (principal_type IN ('human', 'agent', 'system')),
    principal_id uuid NOT NULL,
    delegated_by_type text CHECK (delegated_by_type IN ('human', 'agent', 'system')),
    delegated_by_id uuid,
    target_type text,
    target_id uuid,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((delegated_by_type IS NULL) = (delegated_by_id IS NULL))
);

CREATE INDEX audit_event_org_created_idx
    ON audit_event (organization_id, created_at DESC);

CREATE INDEX audit_event_org_principal_idx
    ON audit_event (organization_id, principal_id);

CREATE INDEX audit_event_org_target_idx
    ON audit_event (organization_id, target_type, target_id);

CREATE INDEX audit_event_org_event_type_created_idx
    ON audit_event (organization_id, event_type, created_at DESC)
    WHERE event_type IS NOT NULL;
