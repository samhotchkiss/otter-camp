ALTER TABLE run
    ADD COLUMN input_tokens integer NOT NULL DEFAULT 0,
    ADD COLUMN output_tokens integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT run_input_tokens_nonnegative_ck CHECK (input_tokens >= 0),
    ADD CONSTRAINT run_output_tokens_nonnegative_ck CHECK (output_tokens >= 0);

ALTER TABLE run_step
    ADD COLUMN input_tokens integer NOT NULL DEFAULT 0,
    ADD COLUMN output_tokens integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT run_step_input_tokens_nonnegative_ck CHECK (input_tokens >= 0),
    ADD CONSTRAINT run_step_output_tokens_nonnegative_ck CHECK (output_tokens >= 0);
