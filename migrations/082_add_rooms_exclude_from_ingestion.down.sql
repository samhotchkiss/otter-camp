DROP INDEX IF EXISTS rooms_org_exclude_from_ingestion_idx;

ALTER TABLE rooms
    DROP COLUMN IF EXISTS exclude_from_ingestion;
