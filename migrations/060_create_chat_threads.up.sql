CREATE TABLE IF NOT EXISTS chat_threads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    issue_id UUID REFERENCES project_issues(id) ON DELETE SET NULL,
    thread_key TEXT NOT NULL,
    thread_type TEXT NOT NULL CHECK (thread_type IN ('dm', 'project', 'issue')),
    title TEXT NOT NULL DEFAULT '',
    last_message_preview TEXT NOT NULL DEFAULT '',
    archived_at TIMESTAMPTZ,
    auto_archived_reason TEXT CHECK (
        auto_archived_reason IN ('issue_closed', 'project_archived')
    ),
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, user_id, thread_key)
);

CREATE INDEX chat_threads_user_active_idx
    ON chat_threads (org_id, user_id, last_message_at DESC, id DESC)
    WHERE archived_at IS NULL;

CREATE INDEX chat_threads_user_archived_idx
    ON chat_threads (org_id, user_id, last_message_at DESC, id DESC)
    WHERE archived_at IS NOT NULL;

CREATE INDEX chat_threads_issue_idx
    ON chat_threads (org_id, issue_id)
    WHERE issue_id IS NOT NULL;

CREATE INDEX chat_threads_project_idx
    ON chat_threads (org_id, project_id)
    WHERE project_id IS NOT NULL;

CREATE TRIGGER chat_threads_updated_at_trg
BEFORE UPDATE ON chat_threads
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE chat_threads ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_threads FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS chat_threads_org_isolation ON chat_threads;
CREATE POLICY chat_threads_org_isolation ON chat_threads
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
