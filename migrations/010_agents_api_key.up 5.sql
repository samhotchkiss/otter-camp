-- Add api_key column to agents table for API authentication
ALTER TABLE agents ADD COLUMN api_key TEXT;

-- Create unique index on api_key (null values are excluded from uniqueness)
CREATE UNIQUE INDEX agents_api_key_unique ON agents (api_key) WHERE api_key IS NOT NULL;

-- Create index for fast lookups by api_key
CREATE INDEX agents_api_key_idx ON agents (api_key) WHERE api_key IS NOT NULL;
