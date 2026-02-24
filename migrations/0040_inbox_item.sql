CREATE TABLE inbox_item (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    target_user_id uuid REFERENCES human_user(id) ON DELETE CASCADE,
    item_type text NOT NULL CHECK (item_type IN ('task_review', 'human_approval_required', 'blocker_filed', 'comment_mention', 'draft_action_review', 'browser_handoff', 'system_alert')),
    source_project_id uuid REFERENCES project(id) ON DELETE CASCADE,
    source_task_id uuid REFERENCES project_task(id) ON DELETE CASCADE,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid,
    title text NOT NULL,
    body text,
    action_payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    is_read boolean NOT NULL DEFAULT false,
    is_acted boolean NOT NULL DEFAULT false,
    acted_at timestamptz,
    acted_by_id uuid REFERENCES human_user(id) ON DELETE SET NULL,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX inbox_item_target_acted_created_desc_idx
    ON inbox_item (target_user_id, is_acted, created_at DESC);

CREATE INDEX inbox_item_org_acted_broadcast_idx
    ON inbox_item (organization_id, is_acted)
    WHERE target_user_id IS NULL;

CREATE INDEX inbox_item_source_task_idx
    ON inbox_item (source_task_id)
    WHERE source_task_id IS NOT NULL;

CREATE INDEX inbox_item_expires_due_idx
    ON inbox_item (expires_at)
    WHERE expires_at IS NOT NULL AND is_acted = false;
