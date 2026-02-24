CREATE TABLE memory_taxonomy_tag (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    memory_id uuid NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
    taxonomy_node_id uuid NOT NULL REFERENCES memory_taxonomy_node(id) ON DELETE CASCADE,
    assigned_by text NOT NULL CHECK (assigned_by IN ('extraction','manual','inference')),
    confidence numeric(4,3) NOT NULL DEFAULT 1.000 CHECK (confidence >= 0 AND confidence <= 1),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX memory_taxonomy_tag_unique_idx
    ON memory_taxonomy_tag (memory_id, taxonomy_node_id);

CREATE TABLE memory_entity_mention (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    memory_id uuid NOT NULL REFERENCES memory(id) ON DELETE CASCADE,
    entity_id uuid NOT NULL REFERENCES memory_entity(id) ON DELETE CASCADE,
    mention_text text NOT NULL,
    confidence numeric(4,3) NOT NULL DEFAULT 1.000 CHECK (confidence >= 0 AND confidence <= 1),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX memory_entity_mention_unique_idx
    ON memory_entity_mention (memory_id, entity_id);

CREATE INDEX memory_entity_mention_entity_idx
    ON memory_entity_mention (entity_id, created_at DESC);

