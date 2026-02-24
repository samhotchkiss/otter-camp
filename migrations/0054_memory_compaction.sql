CREATE TABLE memory_compaction_run (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    run_type text NOT NULL CHECK (run_type IN ('sleep_reflection','task_consolidation','manual')),
    scope_context jsonb NOT NULL DEFAULT '{}'::jsonb,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed')),
    memories_examined integer NOT NULL DEFAULT 0,
    memories_updated integer NOT NULL DEFAULT 0,
    memories_archived integer NOT NULL DEFAULT 0,
    memories_created integer NOT NULL DEFAULT 0,
    error_message text,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX memory_compaction_run_org_type_created_idx
    ON memory_compaction_run (organization_id, run_type, created_at DESC);

