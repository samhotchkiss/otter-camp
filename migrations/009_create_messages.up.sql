-- Create messages table for task/thread conversations
CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    task_id UUID REFERENCES tasks(id) ON DELETE CASCADE,
    thread_id UUID,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT messages_task_or_thread CHECK (task_id IS NOT NULL OR thread_id IS NOT NULL)
);

-- Indexes for efficient querying
CREATE INDEX messages_org_id_idx ON messages(org_id);
CREATE INDEX messages_task_id_idx ON messages(task_id);
CREATE INDEX messages_thread_id_idx ON messages(thread_id);
CREATE INDEX messages_created_at_idx ON messages(created_at DESC);

-- updated_at trigger
CREATE TRIGGER messages_updated_at_trg
BEFORE UPDATE ON messages
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();
