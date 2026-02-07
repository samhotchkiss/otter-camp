CREATE TABLE project_issue_review_notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    notification_type TEXT NOT NULL CHECK (
        notification_type IN ('review_saved_for_owner', 'review_addressed_for_reviewer')
    ),
    target_agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    review_commit_sha TEXT NOT NULL,
    addressed_in_commit_sha TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (issue_id, notification_type, target_agent_id, review_commit_sha, addressed_in_commit_sha)
);

CREATE INDEX project_issue_review_notifications_org_issue_idx
    ON project_issue_review_notifications (org_id, issue_id, created_at DESC);

ALTER TABLE project_issue_review_notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_issue_review_notifications FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_issue_review_notifications_org_isolation ON project_issue_review_notifications;
CREATE POLICY project_issue_review_notifications_org_isolation ON project_issue_review_notifications
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
