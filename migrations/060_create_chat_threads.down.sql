DROP TRIGGER IF EXISTS chat_threads_updated_at_trg ON chat_threads;
DROP INDEX IF EXISTS chat_threads_project_idx;
DROP INDEX IF EXISTS chat_threads_issue_idx;
DROP INDEX IF EXISTS chat_threads_user_archived_idx;
DROP INDEX IF EXISTS chat_threads_user_active_idx;
DROP TABLE IF EXISTS chat_threads;
