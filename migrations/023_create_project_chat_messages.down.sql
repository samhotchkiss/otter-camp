DROP TRIGGER IF EXISTS project_chat_messages_updated_at_trg ON project_chat_messages;
DROP INDEX IF EXISTS project_chat_messages_search_idx;
DROP INDEX IF EXISTS project_chat_messages_org_project_created_idx;
DROP TABLE IF EXISTS project_chat_messages;
