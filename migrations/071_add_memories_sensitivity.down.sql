DROP INDEX IF EXISTS memories_org_sensitivity_idx;

ALTER TABLE memories
DROP CONSTRAINT IF EXISTS memories_sensitivity_chk;

ALTER TABLE memories
DROP COLUMN IF EXISTS sensitivity;
