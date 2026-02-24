CREATE TABLE memory_dedup_reviewed (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    memory_id_a uuid NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
    memory_id_b uuid NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
    cosine_similarity numeric(5,4) NOT NULL,
    decision text NOT NULL CHECK (decision IN ('keep_both','merge','supersede_a','supersede_b','deferred')),
    reviewed_by text NOT NULL CHECK (reviewed_by IN ('llm','human')),
    reviewed_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (memory_id_a <> memory_id_b)
);

CREATE UNIQUE INDEX memory_dedup_reviewed_pair_unique_idx
    ON memory_dedup_reviewed (
        LEAST(memory_id_a::text, memory_id_b::text),
        GREATEST(memory_id_a::text, memory_id_b::text)
    );

CREATE INDEX memory_dedup_reviewed_decision_idx
    ON memory_dedup_reviewed (decision, reviewed_at DESC);

