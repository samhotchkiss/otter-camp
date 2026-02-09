CREATE TABLE questionnaires (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    context_type TEXT NOT NULL CHECK (context_type IN ('issue', 'project_chat', 'template')),
    context_id UUID NOT NULL,
    author TEXT NOT NULL CHECK (char_length(trim(author)) > 0),
    title TEXT,
    questions JSONB NOT NULL,
    responses JSONB,
    responded_by TEXT,
    responded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (responded_at IS NULL AND responses IS NULL AND responded_by IS NULL) OR
        (responded_at IS NOT NULL AND responses IS NOT NULL AND char_length(trim(coalesce(responded_by, ''))) > 0)
    )
);

CREATE INDEX questionnaires_org_context_created_idx
    ON questionnaires (org_id, context_type, context_id, created_at ASC, id ASC);

ALTER TABLE questionnaires ENABLE ROW LEVEL SECURITY;
ALTER TABLE questionnaires FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS questionnaires_org_isolation ON questionnaires;
CREATE POLICY questionnaires_org_isolation ON questionnaires
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
