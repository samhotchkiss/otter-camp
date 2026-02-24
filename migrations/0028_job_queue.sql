CREATE TABLE job_queue (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_type text NOT NULL,
    priority integer NOT NULL DEFAULT 100,
    payload jsonb NOT NULL DEFAULT '{}',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'claimed', 'done', 'failed', 'dead_letter')),
    claimed_by text,
    claimed_at timestamptz,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 3 CHECK (max_attempts >= 1),
    last_error text,
    run_after timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX job_queue_pending_priority_run_after_idx
    ON job_queue (status, priority, run_after)
    WHERE status = 'pending';

CREATE INDEX job_queue_claimed_at_idx
    ON job_queue (claimed_at)
    WHERE status = 'claimed';

CREATE TRIGGER job_queue_set_updated_at
BEFORE UPDATE ON job_queue
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
