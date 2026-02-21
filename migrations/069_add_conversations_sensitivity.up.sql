ALTER TABLE conversations
ADD COLUMN IF NOT EXISTS sensitivity TEXT NOT NULL DEFAULT 'normal';

ALTER TABLE conversations
DROP CONSTRAINT IF EXISTS conversations_sensitivity_chk;

ALTER TABLE conversations
ADD CONSTRAINT conversations_sensitivity_chk
CHECK (sensitivity IN ('normal', 'sensitive'));
