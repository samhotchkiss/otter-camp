DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS org_settings;
DROP TABLE IF EXISTS user_notification_settings;

ALTER TABLE users
    DROP COLUMN IF EXISTS avatar_url,
    DROP COLUMN IF EXISTS role;
