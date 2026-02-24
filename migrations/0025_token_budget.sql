CREATE TABLE token_budget (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid,
    period text NOT NULL CHECK (period IN ('daily', 'weekly', 'monthly')),
    soft_limit_tokens bigint,
    hard_limit_tokens bigint,
    alert_channel text,
    is_enabled boolean NOT NULL DEFAULT true,
    last_warned_period text,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'system')),
    created_by_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT token_budget_soft_limit_nonnegative_chk
        CHECK (soft_limit_tokens IS NULL OR soft_limit_tokens >= 0),
    CONSTRAINT token_budget_hard_limit_nonnegative_chk
        CHECK (hard_limit_tokens IS NULL OR hard_limit_tokens >= 0),
    CONSTRAINT token_budget_limits_order_chk
        CHECK (
            soft_limit_tokens IS NULL
            OR hard_limit_tokens IS NULL
            OR soft_limit_tokens <= hard_limit_tokens
        )
);

CREATE UNIQUE INDEX token_budget_org_period_uidx
    ON token_budget (organization_id, period)
    WHERE project_id IS NULL;

CREATE UNIQUE INDEX token_budget_org_project_period_uidx
    ON token_budget (organization_id, project_id, period)
    WHERE project_id IS NOT NULL;

CREATE INDEX token_budget_org_enabled_idx
    ON token_budget (organization_id, is_enabled)
    WHERE is_enabled = true;

CREATE INDEX token_budget_project_idx
    ON token_budget (project_id)
    WHERE project_id IS NOT NULL;

DO $$
BEGIN
    -- Project table migration (0031) was merged before this migration number.
    -- Add the FK immediately when project already exists (upgrade path).
    IF to_regclass('public.project') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1
           FROM pg_constraint
           WHERE conname = 'token_budget_project_fk'
       )
    THEN
        ALTER TABLE token_budget
            ADD CONSTRAINT token_budget_project_fk
            FOREIGN KEY (project_id)
            REFERENCES project(id)
            ON DELETE CASCADE;
    END IF;
END;
$$;

CREATE TRIGGER token_budget_set_updated_at
BEFORE UPDATE ON token_budget
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
