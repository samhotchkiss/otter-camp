CREATE TABLE project_chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    author TEXT NOT NULL,
    body TEXT NOT NULL,
    search_document tsvector GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(author, '') || ' ' || coalesce(body, ''))
    ) STORED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX project_chat_messages_org_project_created_idx
    ON project_chat_messages (org_id, project_id, created_at DESC, id DESC);

CREATE INDEX project_chat_messages_search_idx
    ON project_chat_messages
    USING GIN (search_document);

CREATE TRIGGER project_chat_messages_updated_at_trg
BEFORE UPDATE ON project_chat_messages
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE project_chat_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_chat_messages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS project_chat_messages_org_isolation ON project_chat_messages;
CREATE POLICY project_chat_messages_org_isolation ON project_chat_messages
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
