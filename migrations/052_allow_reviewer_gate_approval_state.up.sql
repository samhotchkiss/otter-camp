ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_approval_state_check;

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_approval_state_check
    CHECK (approval_state IN ('draft', 'ready_for_review', 'needs_changes', 'approved_by_reviewer', 'approved'));
