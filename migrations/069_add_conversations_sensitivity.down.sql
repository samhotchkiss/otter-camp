ALTER TABLE conversations
DROP CONSTRAINT IF EXISTS conversations_sensitivity_chk;

ALTER TABLE conversations
DROP COLUMN IF EXISTS sensitivity;
