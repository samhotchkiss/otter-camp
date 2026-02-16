DROP TRIGGER IF EXISTS ellie_project_docs_updated_at_trg ON ellie_project_docs;
DROP INDEX IF EXISTS ellie_project_docs_embedding_idx;
DROP INDEX IF EXISTS ellie_project_docs_hash_idx;
DROP INDEX IF EXISTS ellie_project_docs_org_project_active_idx;
DROP POLICY IF EXISTS ellie_project_docs_org_isolation ON ellie_project_docs;
DROP TABLE IF EXISTS ellie_project_docs;
