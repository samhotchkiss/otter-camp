CREATE TABLE IF NOT EXISTS agent_memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('daily', 'long_term', 'note')),
    date DATE,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS agent_memories_org_agent_idx
    ON agent_memories (org_id, agent_id, created_at DESC);

CREATE INDEX IF NOT EXISTS agent_memories_agent_kind_idx
    ON agent_memories (agent_id, kind, date DESC, created_at DESC);
