ALTER TABLE rooms
    ADD COLUMN IF NOT EXISTS exclude_from_ingestion BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS rooms_org_exclude_from_ingestion_idx
    ON rooms (org_id, exclude_from_ingestion)
    WHERE exclude_from_ingestion = TRUE;
