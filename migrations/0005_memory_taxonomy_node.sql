CREATE TABLE memory_taxonomy_node (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    parent_id uuid REFERENCES memory_taxonomy_node(id) ON DELETE RESTRICT,
    slug text NOT NULL,
    display_name text NOT NULL,
    description text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, parent_id, slug)
);
