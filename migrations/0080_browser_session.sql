CREATE TABLE browser_session (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'idle', 'closed', 'revoked')),
    browser_type text NOT NULL DEFAULT 'chromium',
    current_url text,
    user_agent text,
    credentials_injected jsonb NOT NULL DEFAULT '[]'::jsonb,
    idle_since timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX browser_session_task_active_unique_idx
    ON browser_session (task_id)
    WHERE status = 'active';

CREATE INDEX browser_session_task_status_idx
    ON browser_session (task_id, status);

CREATE INDEX browser_session_agent_idx
    ON browser_session (agent_id);

CREATE INDEX browser_session_org_created_at_idx
    ON browser_session (organization_id, created_at);

CREATE TRIGGER browser_session_set_updated_at
BEFORE UPDATE ON browser_session
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
