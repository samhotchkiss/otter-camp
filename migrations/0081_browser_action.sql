CREATE TABLE browser_action (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    browser_session_id uuid NOT NULL REFERENCES browser_session(id) ON DELETE CASCADE,
    run_id uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    run_step_id uuid NOT NULL REFERENCES run_step(id) ON DELETE CASCADE,
    action_type text NOT NULL CHECK (action_type IN (
        'navigate',
        'click',
        'type',
        'select',
        'hover',
        'scroll',
        'press_key',
        'screenshot',
        'extract_text',
        'extract_structured',
        'get_page_info',
        'wait_for',
        'wait_for_navigation',
        'back',
        'forward',
        'refresh',
        'evaluate'
    )),
    input jsonb NOT NULL DEFAULT '{}'::jsonb,
    output jsonb,
    screenshot_artifact_id uuid REFERENCES run_artifact(id) ON DELETE SET NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_progress', 'completed', 'failed', 'blocked_by_domain_policy')),
    error_message text,
    url_at_execution text,
    started_at timestamptz,
    completed_at timestamptz,
    duration_ms integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT browser_action_duration_nonnegative_ck CHECK (duration_ms IS NULL OR duration_ms >= 0)
);

CREATE INDEX browser_action_session_created_idx
    ON browser_action (browser_session_id, created_at);

CREATE INDEX browser_action_run_idx
    ON browser_action (run_id);

CREATE INDEX browser_action_run_step_idx
    ON browser_action (run_step_id);
