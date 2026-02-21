CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS rooms (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT,
    type TEXT NOT NULL CHECK (type IN ('project', 'issue', 'ad_hoc', 'system')),
    context_id UUID,
    last_compacted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS rooms_org_type_context_idx
    ON rooms (org_id, type, context_id);

CREATE TABLE IF NOT EXISTS room_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    participant_id UUID NOT NULL,
    participant_type TEXT NOT NULL CHECK (participant_type IN ('agent', 'user')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (room_id, participant_id)
);

CREATE INDEX IF NOT EXISTS room_participants_room_joined_idx
    ON room_participants (org_id, room_id, joined_at DESC);

CREATE TABLE IF NOT EXISTS conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    topic TEXT,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS conversations_room_started_idx
    ON conversations (org_id, room_id, started_at DESC);

CREATE TABLE IF NOT EXISTS chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    sender_id UUID NOT NULL,
    sender_type TEXT NOT NULL CHECK (sender_type IN ('agent', 'user', 'system')),
    body TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'message' CHECK (type IN (
        'message',
        'whisper',
        'system',
        'context_injection'
    )),
    conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    embedding vector(384),
    search_document tsvector GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(body, ''))
    ) STORED,
    attachments JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS chat_messages_room_created_idx
    ON chat_messages (org_id, room_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS chat_messages_conversation_idx
    ON chat_messages (conversation_id) WHERE conversation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS chat_messages_search_idx
    ON chat_messages USING GIN (search_document);
CREATE INDEX IF NOT EXISTS chat_messages_embedding_idx
    ON chat_messages USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
    WHERE embedding IS NOT NULL;
CREATE INDEX IF NOT EXISTS chat_messages_unembedded_idx
    ON chat_messages (created_at) WHERE embedding IS NULL;

CREATE TABLE IF NOT EXISTS memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN (
        'technical_decision',
        'process_decision',
        'preference',
        'fact',
        'lesson',
        'pattern',
        'anti_pattern',
        'correction',
        'process_outcome',
        'context'
    )),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    importance SMALLINT NOT NULL DEFAULT 3 CHECK (importance BETWEEN 1 AND 5),
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated', 'archived')),
    superseded_by UUID REFERENCES memories(id) ON DELETE SET NULL,
    source_conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    source_project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    embedding vector(384),
    content_hash TEXT GENERATED ALWAYS AS (
        encode(digest((kind || ':' || title || ':' || content)::bytea, 'sha256'), 'hex')
    ) STORED,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS memories_dedup_active
    ON memories (org_id, content_hash) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS memories_embedding_idx
    ON memories USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
    WHERE embedding IS NOT NULL;
CREATE INDEX IF NOT EXISTS memories_org_kind_idx
    ON memories (org_id, kind, occurred_at DESC);
CREATE INDEX IF NOT EXISTS memories_org_status_idx
    ON memories (org_id, status, occurred_at DESC);
CREATE INDEX IF NOT EXISTS memories_conversation_idx
    ON memories (source_conversation_id) WHERE source_conversation_id IS NOT NULL;

DROP TRIGGER IF EXISTS rooms_updated_at_trg ON rooms;
CREATE TRIGGER rooms_updated_at_trg
BEFORE UPDATE ON rooms
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS conversations_updated_at_trg ON conversations;
CREATE TRIGGER conversations_updated_at_trg
BEFORE UPDATE ON conversations
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS memories_updated_at_trg ON memories;
CREATE TRIGGER memories_updated_at_trg
BEFORE UPDATE ON memories
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

ALTER TABLE rooms ENABLE ROW LEVEL SECURITY;
ALTER TABLE rooms FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS rooms_org_isolation ON rooms;
CREATE POLICY rooms_org_isolation ON rooms
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE room_participants ENABLE ROW LEVEL SECURITY;
ALTER TABLE room_participants FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS room_participants_org_isolation ON room_participants;
CREATE POLICY room_participants_org_isolation ON room_participants
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS conversations_org_isolation ON conversations;
CREATE POLICY conversations_org_isolation ON conversations
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE chat_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_messages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS chat_messages_org_isolation ON chat_messages;
CREATE POLICY chat_messages_org_isolation ON chat_messages
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE memories ENABLE ROW LEVEL SECURITY;
ALTER TABLE memories FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS memories_org_isolation ON memories;
CREATE POLICY memories_org_isolation ON memories
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
