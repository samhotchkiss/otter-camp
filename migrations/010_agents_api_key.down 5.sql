-- Remove api_key column and indexes from agents table
DROP INDEX IF EXISTS agents_api_key_idx;
DROP INDEX IF EXISTS agents_api_key_unique;
ALTER TABLE agents DROP COLUMN IF EXISTS api_key;
