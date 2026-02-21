DROP INDEX IF EXISTS project_issues_ellie_context_gate_idx;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_ellie_context_gate_status_chk;

ALTER TABLE project_issues
    DROP COLUMN IF EXISTS ellie_context_gate_checked_at,
    DROP COLUMN IF EXISTS ellie_context_gate_error,
    DROP COLUMN IF EXISTS ellie_context_gate_status;
