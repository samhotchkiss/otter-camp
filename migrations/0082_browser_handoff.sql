CREATE TABLE browser_handoff (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    browser_session_id uuid NOT NULL REFERENCES browser_session(id) ON DELETE CASCADE,
    inbox_item_id uuid NOT NULL REFERENCES inbox_item(id) ON DELETE CASCADE,
    run_id uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    agent_id uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    target_user_id uuid NOT NULL REFERENCES human_user(id) ON DELETE CASCADE,
    reason text NOT NULL,
    pre_handoff_screenshot_artifact_id uuid REFERENCES run_artifact(id) ON DELETE SET NULL,
    post_handoff_screenshot_artifact_id uuid REFERENCES run_artifact(id) ON DELETE SET NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'expired', 'cancelled')),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '24 hours'),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX browser_handoff_run_idx
    ON browser_handoff (run_id);

CREATE INDEX browser_handoff_inbox_item_idx
    ON browser_handoff (inbox_item_id);

CREATE INDEX browser_handoff_session_idx
    ON browser_handoff (browser_session_id);

CREATE INDEX browser_handoff_pending_expiry_idx
    ON browser_handoff (status, expires_at)
    WHERE status = 'pending';
