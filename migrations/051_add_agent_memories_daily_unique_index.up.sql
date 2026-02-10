CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_memories_daily_unique
    ON agent_memories (agent_id, date)
    WHERE kind = 'daily';
