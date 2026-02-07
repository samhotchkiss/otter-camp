CREATE TABLE exec_approval_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    external_id TEXT,
    agent_id UUID,
    task_id UUID,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'approved', 'denied', 'cancelled', 'expired')),
    command TEXT NOT NULL,
    cwd TEXT,
    shell TEXT,
    args JSONB,
    env JSONB,
    message TEXT,
    callback_url TEXT,
    request JSONB,
    response JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX exec_approval_requests_org_status_created_idx
    ON exec_approval_requests (org_id, status, created_at DESC);

CREATE UNIQUE INDEX exec_approval_requests_org_external_id_idx
    ON exec_approval_requests (org_id, external_id)
    WHERE external_id IS NOT NULL;

ALTER TABLE exec_approval_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE exec_approval_requests FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS exec_approval_requests_org_isolation ON exec_approval_requests;
CREATE POLICY exec_approval_requests_org_isolation ON exec_approval_requests
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

