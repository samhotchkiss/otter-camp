DELETE FROM memories
WHERE metadata->>'source_table' IN ('memory_entries', 'shared_knowledge', 'agent_memories');
