ALTER TABLE agents
    DROP COLUMN IF EXISTS instructions_md,
    DROP COLUMN IF EXISTS identity_md,
    DROP COLUMN IF EXISTS soul_md,
    DROP COLUMN IF EXISTS emoji,
    DROP COLUMN IF EXISTS role;
