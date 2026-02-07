ALTER TABLE project_issues
    ADD COLUMN document_path TEXT;

ALTER TABLE project_issues
    ADD COLUMN approval_state TEXT NOT NULL DEFAULT 'draft';

UPDATE project_issues
SET approval_state = CASE
    WHEN state = 'closed' THEN 'approved'
    ELSE 'draft'
END;

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_document_path_check
    CHECK (document_path IS NULL OR document_path ~ '^/posts/.+\\.md$');

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_approval_state_check
    CHECK (approval_state IN ('draft', 'ready_for_review', 'needs_changes', 'approved'));

CREATE INDEX project_issues_project_approval_state_idx
    ON project_issues (project_id, approval_state, updated_at DESC);
