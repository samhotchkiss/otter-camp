-- NOTE: task 038 assumes memory_entity exists from an earlier layer. It does not
-- exist in the current migration history, so this compatibility table is created here.
CREATE TABLE IF NOT EXISTS memory_entity (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    canonical_name text NOT NULL,
    entity_type text NOT NULL DEFAULT 'general',
    synthesis_memory_id uuid,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS memory_entity_org_name_idx
    ON memory_entity (organization_id, canonical_name);

CREATE TRIGGER memory_entity_set_updated_at
BEFORE UPDATE ON memory_entity
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TABLE memory (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid REFERENCES project(id) ON DELETE SET NULL,
    project_task_id uuid REFERENCES project_task(id) ON DELETE SET NULL,
    agent_id uuid REFERENCES agent(id) ON DELETE SET NULL,
    memory_type text NOT NULL CHECK (memory_type IN ('episodic','semantic','procedural','preference','entity_profile','execution_summary')),
    scope text NOT NULL CHECK (scope IN ('org','project','task','agent')),
    content text NOT NULL,
    content_hash text NOT NULL,
    embedding vector(1536),
    confidence numeric(4,3) NOT NULL DEFAULT 0.500 CHECK (confidence >= 0 AND confidence <= 1),
    utility_score numeric(4,3) NOT NULL DEFAULT 0.500 CHECK (utility_score >= 0 AND utility_score <= 1),
    extraction_score integer CHECK (extraction_score >= 0 AND extraction_score <= 100),
    status text NOT NULL DEFAULT 'candidate' CHECK (status IN ('candidate','active','superseded','archived')),
    is_hardened boolean NOT NULL DEFAULT false,
    sensitivity text NOT NULL DEFAULT 'normal' CHECK (sensitivity IN ('normal','restricted')),
    trust_tier numeric(3,2) NOT NULL DEFAULT 0.80 CHECK (trust_tier >= 0 AND trust_tier <= 1),
    file_backed boolean NOT NULL DEFAULT false,
    file_path text,
    file_last_scanned_at timestamptz,
    superseded_by uuid REFERENCES memory(id),
    superseded_at timestamptz,
    archived_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN memory.embedding IS 'OpenAI-compatible embedding dimensions (1536).';
COMMENT ON COLUMN memory.scope IS 'Retrieval scope; provenance FKs may remain set after scope promotion.';

CREATE INDEX memory_org_status_scope_idx
    ON memory (organization_id, status, scope);

CREATE INDEX memory_project_status_idx
    ON memory (project_id, status)
    WHERE project_id IS NOT NULL;

CREATE INDEX memory_agent_status_idx
    ON memory (agent_id, status)
    WHERE agent_id IS NOT NULL;

CREATE INDEX memory_content_hash_org_idx
    ON memory (content_hash, organization_id);

CREATE INDEX memory_status_file_backed_idx
    ON memory (status, file_backed)
    WHERE file_backed = true;

-- HNSW_INDEX: must run outside transaction; migration runner handles this automatically
CREATE INDEX CONCURRENTLY memory_embedding_hnsw_idx
    ON memory USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE TRIGGER memory_set_updated_at
BEFORE UPDATE ON memory
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
