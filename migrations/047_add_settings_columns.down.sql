ALTER TABLE users
DROP COLUMN IF EXISTS notification_preferences;

ALTER TABLE organizations
DROP COLUMN IF EXISTS openclaw_webhook_url;
