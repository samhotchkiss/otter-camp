CREATE TABLE chat_session (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    scope_type text NOT NULL CHECK (scope_type IN ('organization', 'project', 'project_task')),
    scope_id uuid NOT NULL,
    mode text NOT NULL CHECK (mode IN ('sync', 'async')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed', 'archived')),
    title text,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid NOT NULL,
    current_turn_id uuid,
    last_message_at timestamptz,
    turn_count integer NOT NULL DEFAULT 0,
    message_count integer NOT NULL DEFAULT 0,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    closed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN chat_session.scope_id IS 'soft ref polymorphic: organization.id | project.id | project_task.id';
COMMENT ON COLUMN chat_session.created_by_id IS 'system actor uses sentinel UUID 00000000-0000-0000-0000-000000000000';
COMMENT ON COLUMN chat_session.current_turn_id IS 'soft ref to chat_turn.id; no FK to avoid circular dependency';

CREATE INDEX chat_session_org_status_last_message_idx
    ON chat_session (organization_id, status, last_message_at DESC);

CREATE INDEX chat_session_scope_status_idx
    ON chat_session (scope_type, scope_id, status);

CREATE INDEX chat_session_created_by_idx
    ON chat_session (created_by_type, created_by_id)
    WHERE created_by_type IS NOT NULL
      AND created_by_id IS NOT NULL;

CREATE TRIGGER chat_session_set_updated_at
BEFORE UPDATE ON chat_session
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
