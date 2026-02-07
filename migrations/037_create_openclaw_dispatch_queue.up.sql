CREATE TABLE IF NOT EXISTS openclaw_dispatch_queue (
    id BIGSERIAL PRIMARY KEY,
    org_id UUID NOT NULL,
    event_type TEXT NOT NULL,
    dedupe_key TEXT NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'delivered')),
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    claim_token TEXT,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS openclaw_dispatch_queue_pending_idx
    ON openclaw_dispatch_queue (status, available_at, created_at);

CREATE INDEX IF NOT EXISTS openclaw_dispatch_queue_org_idx
    ON openclaw_dispatch_queue (org_id, created_at);
