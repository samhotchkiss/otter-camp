CREATE TABLE IF NOT EXISTS ellie_project_docs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    title TEXT,
    summary TEXT,
    summary_embedding vector(1536),
    content_hash TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    last_scanned_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, project_id, file_path)
);

CREATE INDEX IF NOT EXISTS ellie_project_docs_org_project_active_idx
    ON ellie_project_docs (org_id, project_id, is_active, file_path);

CREATE INDEX IF NOT EXISTS ellie_project_docs_hash_idx
    ON ellie_project_docs (org_id, project_id, content_hash);

CREATE INDEX IF NOT EXISTS ellie_project_docs_embedding_idx
    ON ellie_project_docs USING ivfflat (summary_embedding vector_cosine_ops) WITH (lists = 100)
    WHERE summary_embedding IS NOT NULL AND is_active = true;

CREATE TRIGGER ellie_project_docs_updated_at_trg
BEFORE UPDATE ON ellie_project_docs
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE ellie_project_docs ENABLE ROW LEVEL SECURITY;
ALTER TABLE ellie_project_docs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS ellie_project_docs_org_isolation ON ellie_project_docs;
CREATE POLICY ellie_project_docs_org_isolation ON ellie_project_docs
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
