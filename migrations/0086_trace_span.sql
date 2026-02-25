CREATE TABLE trace_span (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    trace_id uuid NOT NULL,
    parent_id uuid,
    organization_id uuid REFERENCES organization(id) ON DELETE CASCADE,
    span_name text NOT NULL,
    service text NOT NULL DEFAULT 'ottercamp',
    kind text NOT NULL CHECK (kind IN ('internal', 'server', 'client', 'producer', 'consumer')),
    status text NOT NULL CHECK (status IN ('unset', 'ok', 'error')),
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    events jsonb NOT NULL DEFAULT '[]'::jsonb,
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    duration_ms integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

DO $$
DECLARE
    partition_start timestamptz := date_trunc('day', now());
    partition_end timestamptz := date_trunc('day', now()) + interval '7 days';
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS trace_span_p_default PARTITION OF trace_span FOR VALUES FROM (%L) TO (%L)',
        partition_start,
        partition_end
    );
END $$;

CREATE INDEX IF NOT EXISTS trace_span_p_default_trace_id_idx
    ON trace_span_p_default (trace_id);

CREATE INDEX IF NOT EXISTS trace_span_p_default_org_created_idx
    ON trace_span_p_default (organization_id, created_at);
