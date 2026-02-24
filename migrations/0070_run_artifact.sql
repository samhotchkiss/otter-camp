CREATE TABLE run_artifact (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES run(id) ON DELETE CASCADE,
    run_step_id uuid REFERENCES run_step(id) ON DELETE SET NULL,
    run_attempt_id uuid REFERENCES run_attempt(id) ON DELETE SET NULL,
    artifact_type text NOT NULL CHECK (artifact_type IN ('stdout', 'stderr', 'screenshot', 'file_snapshot', 'structured_output', 'error_detail')),
    storage_key text NOT NULL,
    content_type text NOT NULL,
    byte_size integer NOT NULL,
    inline_content text,
    filename text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT run_artifact_byte_size_nonnegative_ck CHECK (byte_size >= 0),
    CONSTRAINT run_artifact_storage_key_unique UNIQUE (storage_key)
);

CREATE INDEX run_artifact_run_idx
    ON run_artifact (run_id);

CREATE INDEX run_artifact_step_idx
    ON run_artifact (run_step_id);

CREATE INDEX run_artifact_type_run_idx
    ON run_artifact (artifact_type, run_id);
