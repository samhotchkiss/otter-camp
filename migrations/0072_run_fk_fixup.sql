ALTER TABLE model_invocation
    ADD CONSTRAINT model_invocation_run_id_fk
    FOREIGN KEY (run_id)
    REFERENCES run(id)
    ON DELETE SET NULL;

ALTER TABLE model_invocation
    ADD CONSTRAINT model_invocation_run_step_id_fk
    FOREIGN KEY (run_step_id)
    REFERENCES run_step(id)
    ON DELETE SET NULL;

ALTER TABLE model_invocation
    ADD CONSTRAINT model_invocation_run_attempt_id_fk
    FOREIGN KEY (run_attempt_id)
    REFERENCES run_attempt(id)
    ON DELETE SET NULL;
