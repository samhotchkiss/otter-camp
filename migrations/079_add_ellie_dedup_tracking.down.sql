DROP POLICY IF EXISTS ellie_dedup_cursors_org_isolation ON ellie_dedup_cursors;
DROP TRIGGER IF EXISTS ellie_dedup_cursors_updated_at_trg ON ellie_dedup_cursors;
DROP TABLE IF EXISTS ellie_dedup_cursors;

DROP POLICY IF EXISTS ellie_dedup_reviewed_org_isolation ON ellie_dedup_reviewed;
DROP INDEX IF EXISTS ellie_dedup_reviewed_org_reviewed_idx;
DROP TABLE IF EXISTS ellie_dedup_reviewed;
