ALTER TABLE project_issues
    ADD COLUMN IF NOT EXISTS ellie_context_gate_status TEXT,
    ADD COLUMN IF NOT EXISTS ellie_context_gate_error TEXT,
    ADD COLUMN IF NOT EXISTS ellie_context_gate_checked_at TIMESTAMPTZ;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_ellie_context_gate_status_chk;

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_ellie_context_gate_status_chk
    CHECK (
        ellie_context_gate_status IS NULL
        OR ellie_context_gate_status IN ('succeeded', 'failed', 'bypassed')
    );

CREATE INDEX IF NOT EXISTS project_issues_ellie_context_gate_idx
    ON project_issues (project_id, ellie_context_gate_status, ellie_context_gate_checked_at DESC);
