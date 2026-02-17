CREATE TABLE IF NOT EXISTS openclaw_history_import_failures (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    migration_type TEXT NOT NULL,
    batch_id TEXT NOT NULL DEFAULT '',
    agent_slug TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    event_id TEXT NOT NULL,
    session_path TEXT NOT NULL DEFAULT '',
    line INT NOT NULL DEFAULT 0,
    message_id_candidate TEXT NOT NULL DEFAULT '',
    error_reason TEXT NOT NULL,
    error_message TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempt_count INT NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS openclaw_history_import_failures_identity_uidx
    ON openclaw_history_import_failures (
        org_id,
        migration_type,
        agent_slug,
        session_id,
        event_id,
        session_path,
        line
    );

CREATE INDEX IF NOT EXISTS openclaw_history_import_failures_org_recent_idx
    ON openclaw_history_import_failures (org_id, migration_type, last_seen_at DESC, updated_at DESC);

DROP TRIGGER IF EXISTS openclaw_history_import_failures_updated_at_trg ON openclaw_history_import_failures;
CREATE TRIGGER openclaw_history_import_failures_updated_at_trg
BEFORE UPDATE ON openclaw_history_import_failures
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE openclaw_history_import_failures ENABLE ROW LEVEL SECURITY;
ALTER TABLE openclaw_history_import_failures FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS openclaw_history_import_failures_org_isolation ON openclaw_history_import_failures;
CREATE POLICY openclaw_history_import_failures_org_isolation ON openclaw_history_import_failures
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
