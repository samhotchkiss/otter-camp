-- Fix schema issues from PR #17 review (Issue #19)

-- 1. Fix task status enum to match spec
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check 
  CHECK (status IN ('queued', 'dispatched', 'in_progress', 'blocked', 'review', 'done', 'cancelled'));
UPDATE tasks SET status = 'queued' WHERE status = 'open';

-- 2. Add session_pattern to agents for OpenClaw routing
ALTER TABLE agents ADD COLUMN session_pattern TEXT;

-- 3. Fix task number to be per-org unique (not global SERIAL)
-- Drop the SERIAL default
ALTER TABLE tasks ALTER COLUMN number DROP DEFAULT;

-- Drop the auto-created sequence if it exists
DROP SEQUENCE IF EXISTS tasks_number_seq;

-- Create function for per-org task numbering
CREATE OR REPLACE FUNCTION next_task_number() RETURNS TRIGGER AS $$
BEGIN
  SELECT COALESCE(MAX(number), 0) + 1 INTO NEW.number 
  FROM tasks WHERE org_id = NEW.org_id;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger to auto-assign number on insert
CREATE TRIGGER set_task_number 
  BEFORE INSERT ON tasks
  FOR EACH ROW 
  EXECUTE FUNCTION next_task_number();

-- Add unique constraint for org + number
ALTER TABLE tasks ADD CONSTRAINT tasks_org_number_unique UNIQUE (org_id, number);
