DROP TRIGGER IF EXISTS update_agent_memory_config_updated_at ON agent_memory_config;
DROP TRIGGER IF EXISTS update_shared_knowledge_updated_at ON shared_knowledge;
DROP TRIGGER IF EXISTS update_memory_entries_updated_at ON memory_entries;

DROP POLICY IF EXISTS memory_events_org_isolation ON memory_events;
DROP POLICY IF EXISTS working_memory_org_isolation ON working_memory;
DROP POLICY IF EXISTS agent_teams_org_isolation ON agent_teams;
DROP POLICY IF EXISTS compaction_events_org_isolation ON compaction_events;
DROP POLICY IF EXISTS agent_memory_config_org_isolation ON agent_memory_config;
DROP POLICY IF EXISTS shared_knowledge_embeddings_org_isolation ON shared_knowledge_embeddings;
DROP POLICY IF EXISTS shared_knowledge_org_isolation ON shared_knowledge;
DROP POLICY IF EXISTS memory_entry_embeddings_org_isolation ON memory_entry_embeddings;
DROP POLICY IF EXISTS memory_entries_org_isolation ON memory_entries;

DROP TABLE IF EXISTS memory_events;
DROP TABLE IF EXISTS working_memory;
DROP TABLE IF EXISTS agent_teams;
DROP TABLE IF EXISTS compaction_events;
DROP TABLE IF EXISTS agent_memory_config;
DROP TABLE IF EXISTS shared_knowledge_embeddings;
DROP TABLE IF EXISTS shared_knowledge;
DROP TABLE IF EXISTS memory_entry_embeddings;
DROP TABLE IF EXISTS memory_entries;

DROP EXTENSION IF EXISTS vector;
