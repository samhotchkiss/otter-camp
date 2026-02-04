-- Rollback schema fixes (Issue #19)

-- Remove per-org task number constraint and trigger
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_org_number_unique;
DROP TRIGGER IF EXISTS set_task_number ON tasks;
DROP FUNCTION IF EXISTS next_task_number();

-- Restore SERIAL behavior
CREATE SEQUENCE IF NOT EXISTS tasks_number_seq;
ALTER TABLE tasks ALTER COLUMN number SET DEFAULT nextval('tasks_number_seq');

-- Remove session_pattern from agents
ALTER TABLE agents DROP COLUMN IF EXISTS session_pattern;

-- Restore original status enum
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check 
  CHECK (status IN ('open', 'in_progress', 'blocked', 'done'));
UPDATE tasks SET status = 'open' WHERE status = 'queued';
