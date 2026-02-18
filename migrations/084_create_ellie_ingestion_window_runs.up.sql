CREATE TABLE IF NOT EXISTS ellie_ingestion_window_runs (
    id BIGSERIAL PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    window_start_at TIMESTAMPTZ NOT NULL,
    window_end_at TIMESTAMPTZ NOT NULL,
    first_message_id UUID,
    last_message_id UUID,
    message_count INT NOT NULL DEFAULT 0,
    token_count INT NOT NULL DEFAULT 0,
    llm_used BOOLEAN NOT NULL DEFAULT FALSE,
    llm_model TEXT,
    llm_trace_id TEXT,
    llm_attempts INT NOT NULL DEFAULT 0,
    ok BOOLEAN NOT NULL DEFAULT FALSE,
    error TEXT,
    duration_ms INT NOT NULL DEFAULT 0,
    inserted_total INT NOT NULL DEFAULT 0,
    inserted_memories INT NOT NULL DEFAULT 0,
    inserted_projects INT NOT NULL DEFAULT 0,
    inserted_issues INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ellie_ingestion_window_runs_org_end_idx
    ON ellie_ingestion_window_runs (org_id, window_end_at DESC);

CREATE INDEX IF NOT EXISTS ellie_ingestion_window_runs_org_day_idx
    ON ellie_ingestion_window_runs (org_id, (window_end_at::date) DESC);

ALTER TABLE ellie_ingestion_window_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_ingestion_window_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_ingestion_window_runs_org_isolation ON ellie_ingestion_window_runs;
CREATE POLICY ellie_ingestion_window_runs_org_isolation ON ellie_ingestion_window_runs
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

