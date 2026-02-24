CREATE TABLE consumer_cursor (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    consumer_name text NOT NULL,
    organization_id uuid REFERENCES organization(id) ON DELETE CASCADE,
    last_seq bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX consumer_cursor_name_org_unique
    ON consumer_cursor (consumer_name, organization_id)
    WHERE organization_id IS NOT NULL;

CREATE UNIQUE INDEX consumer_cursor_name_global_unique
    ON consumer_cursor (consumer_name)
    WHERE organization_id IS NULL;

CREATE TRIGGER consumer_cursor_set_updated_at
BEFORE UPDATE ON consumer_cursor
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
