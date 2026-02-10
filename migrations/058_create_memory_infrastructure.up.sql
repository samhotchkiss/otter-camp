CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS memory_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN (
        'summary',
        'decision',
        'action_item',
        'lesson',
        'preference',
        'fact',
        'feedback',
        'context'
    )),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    importance SMALLINT NOT NULL DEFAULT 3 CHECK (importance BETWEEN 1 AND 5),
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (confidence >= 0 AND confidence <= 1),
    sensitivity TEXT NOT NULL DEFAULT 'internal' CHECK (sensitivity IN ('public', 'internal', 'restricted')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'warm', 'archived')),
    content_hash TEXT GENERATED ALWAYS AS (
        encode(digest((kind || ':' || title || ':' || content)::bytea, 'sha256'), 'hex')
    ) STORED,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    source_session TEXT,
    source_project UUID REFERENCES projects(id) ON DELETE SET NULL,
    source_issue TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_entries_org_agent_occurred
    ON memory_entries (org_id, agent_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_entries_org_agent_kind
    ON memory_entries (org_id, agent_id, kind);
CREATE INDEX IF NOT EXISTS idx_memory_entries_org_source_session
    ON memory_entries (org_id, source_session);
CREATE INDEX IF NOT EXISTS idx_memory_entries_metadata
    ON memory_entries USING gin (metadata);
CREATE INDEX IF NOT EXISTS idx_memory_entries_org_agent_status
    ON memory_entries (org_id, agent_id, status, occurred_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_entries_dedup_active
    ON memory_entries (org_id, agent_id, content_hash)
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS memory_entry_embeddings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    memory_entry_id UUID NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,
    chunk_text TEXT NOT NULL,
    chunk_index INT NOT NULL DEFAULT 0,
    embedding vector(768) NOT NULL,
    model TEXT NOT NULL DEFAULT 'nomic-embed-text',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (memory_entry_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_memory_entry_embeddings_entry
    ON memory_entry_embeddings (memory_entry_id);
CREATE INDEX IF NOT EXISTS idx_memory_entry_embeddings_vector
    ON memory_entry_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

CREATE TABLE IF NOT EXISTS shared_knowledge (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    source_agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    source_memory_id UUID REFERENCES memory_entries(id) ON DELETE SET NULL,
    kind TEXT NOT NULL CHECK (kind IN (
        'decision',
        'lesson',
        'preference',
        'fact',
        'pattern',
        'correction'
    )),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    scope TEXT NOT NULL DEFAULT 'org' CHECK (scope IN ('team', 'org')),
    scope_teams TEXT[] NOT NULL DEFAULT '{}'::text[],
    quality_score DOUBLE PRECISION NOT NULL DEFAULT 0.5 CHECK (quality_score >= 0 AND quality_score <= 1),
    confirmations INT NOT NULL DEFAULT 0 CHECK (confirmations >= 0),
    contradictions INT NOT NULL DEFAULT 0 CHECK (contradictions >= 0),
    last_accessed_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'stale', 'superseded', 'archived')),
    superseded_by UUID REFERENCES shared_knowledge(id) ON DELETE SET NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shared_knowledge_org_quality
    ON shared_knowledge (org_id, quality_score DESC);
CREATE INDEX IF NOT EXISTS idx_shared_knowledge_org_kind
    ON shared_knowledge (org_id, kind);
CREATE INDEX IF NOT EXISTS idx_shared_knowledge_org_status
    ON shared_knowledge (org_id, status);
CREATE INDEX IF NOT EXISTS idx_shared_knowledge_org_scope
    ON shared_knowledge (org_id, scope);
CREATE INDEX IF NOT EXISTS idx_shared_knowledge_scope_teams
    ON shared_knowledge USING gin (scope_teams);

CREATE TABLE IF NOT EXISTS shared_knowledge_embeddings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    shared_knowledge_id UUID NOT NULL REFERENCES shared_knowledge(id) ON DELETE CASCADE,
    chunk_text TEXT NOT NULL,
    chunk_index INT NOT NULL DEFAULT 0,
    embedding vector(768) NOT NULL,
    model TEXT NOT NULL DEFAULT 'nomic-embed-text',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (shared_knowledge_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_shared_knowledge_embeddings_entry
    ON shared_knowledge_embeddings (shared_knowledge_id);
CREATE INDEX IF NOT EXISTS idx_shared_knowledge_embeddings_vector
    ON shared_knowledge_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

CREATE TABLE IF NOT EXISTS agent_memory_config (
    agent_id UUID PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    auto_extract BOOLEAN NOT NULL DEFAULT true,
    auto_recall BOOLEAN NOT NULL DEFAULT true,
    recall_max_tokens INT NOT NULL DEFAULT 2000 CHECK (recall_max_tokens > 0),
    recall_min_relevance DOUBLE PRECISION NOT NULL DEFAULT 0.7 CHECK (recall_min_relevance >= 0 AND recall_min_relevance <= 1),
    recall_max_results INT NOT NULL DEFAULT 5 CHECK (recall_max_results > 0),
    extract_interval_minutes INT NOT NULL DEFAULT 5 CHECK (extract_interval_minutes > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_memory_config_org
    ON agent_memory_config (org_id);

CREATE TABLE IF NOT EXISTS compaction_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    session_key TEXT NOT NULL,
    pre_compaction_tokens INT,
    post_compaction_tokens INT,
    summary_text TEXT,
    recovery_injected BOOLEAN NOT NULL DEFAULT false,
    recovery_injected_at TIMESTAMPTZ,
    recovery_token_count INT,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_compaction_events_org_agent_detected
    ON compaction_events (org_id, agent_id, detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_compaction_events_org_session
    ON compaction_events (org_id, session_key, detected_at DESC);

CREATE TABLE IF NOT EXISTS agent_teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    team_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, agent_id, team_name)
);

CREATE INDEX IF NOT EXISTS idx_agent_teams_agent
    ON agent_teams (agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_teams_org_team
    ON agent_teams (org_id, team_name);

CREATE TABLE IF NOT EXISTS working_memory (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    session_key TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_working_memory_org_session
    ON working_memory (org_id, session_key);
CREATE INDEX IF NOT EXISTS idx_working_memory_expires
    ON working_memory (expires_at);

CREATE TABLE IF NOT EXISTS memory_events (
    id BIGSERIAL PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'memory.created',
        'memory.promoted',
        'memory.archived',
        'knowledge.shared',
        'knowledge.confirmed',
        'knowledge.contradicted',
        'compaction.detected',
        'compaction.recovered'
    )),
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_memory_events_type
    ON memory_events (event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_events_org_created
    ON memory_events (org_id, created_at DESC);

ALTER TABLE memory_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_entries FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS memory_entries_org_isolation ON memory_entries;
CREATE POLICY memory_entries_org_isolation ON memory_entries
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE memory_entry_embeddings ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_entry_embeddings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS memory_entry_embeddings_org_isolation ON memory_entry_embeddings;
CREATE POLICY memory_entry_embeddings_org_isolation ON memory_entry_embeddings
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE shared_knowledge ENABLE ROW LEVEL SECURITY;
ALTER TABLE shared_knowledge FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS shared_knowledge_org_isolation ON shared_knowledge;
CREATE POLICY shared_knowledge_org_isolation ON shared_knowledge
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE shared_knowledge_embeddings ENABLE ROW LEVEL SECURITY;
ALTER TABLE shared_knowledge_embeddings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS shared_knowledge_embeddings_org_isolation ON shared_knowledge_embeddings;
CREATE POLICY shared_knowledge_embeddings_org_isolation ON shared_knowledge_embeddings
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE agent_memory_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_memory_config FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_memory_config_org_isolation ON agent_memory_config;
CREATE POLICY agent_memory_config_org_isolation ON agent_memory_config
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE compaction_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE compaction_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS compaction_events_org_isolation ON compaction_events;
CREATE POLICY compaction_events_org_isolation ON compaction_events
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE agent_teams ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_teams FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_teams_org_isolation ON agent_teams;
CREATE POLICY agent_teams_org_isolation ON agent_teams
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE working_memory ENABLE ROW LEVEL SECURITY;
ALTER TABLE working_memory FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS working_memory_org_isolation ON working_memory;
CREATE POLICY working_memory_org_isolation ON working_memory
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE memory_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE memory_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS memory_events_org_isolation ON memory_events;
CREATE POLICY memory_events_org_isolation ON memory_events
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

DROP TRIGGER IF EXISTS update_memory_entries_updated_at ON memory_entries;
CREATE TRIGGER update_memory_entries_updated_at
    BEFORE UPDATE ON memory_entries
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_shared_knowledge_updated_at ON shared_knowledge;
CREATE TRIGGER update_shared_knowledge_updated_at
    BEFORE UPDATE ON shared_knowledge
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_agent_memory_config_updated_at ON agent_memory_config;
CREATE TRIGGER update_agent_memory_config_updated_at
    BEFORE UPDATE ON agent_memory_config
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
