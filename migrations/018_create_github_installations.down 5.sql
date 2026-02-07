DROP TRIGGER IF EXISTS github_installations_updated_at_trg ON github_installations;
DROP INDEX IF EXISTS github_installations_connected_at_idx;
DROP INDEX IF EXISTS github_installations_org_id_idx;
DROP TABLE IF EXISTS github_installations;
