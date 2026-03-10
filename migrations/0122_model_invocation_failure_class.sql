ALTER TABLE model_invocation
    ADD COLUMN IF NOT EXISTS failure_class text
        CHECK (failure_class IN ('provider_auth', 'provider_rate_limit', 'provider_transient', 'product_runtime'));

CREATE INDEX IF NOT EXISTS model_invocation_failure_class_created_idx
    ON model_invocation (organization_id, failure_class, created_at DESC)
    WHERE failure_class IS NOT NULL;
