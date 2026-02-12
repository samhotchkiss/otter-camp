ALTER TABLE memories
ADD COLUMN IF NOT EXISTS sensitivity TEXT NOT NULL DEFAULT 'normal';

ALTER TABLE memories
DROP CONSTRAINT IF EXISTS memories_sensitivity_chk;

ALTER TABLE memories
ADD CONSTRAINT memories_sensitivity_chk
CHECK (sensitivity IN ('normal', 'sensitive'));

CREATE INDEX IF NOT EXISTS memories_org_sensitivity_idx
    ON memories (org_id, sensitivity)
    WHERE sensitivity = 'sensitive';
