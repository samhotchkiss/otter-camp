ALTER TABLE organizations
ADD COLUMN IF NOT EXISTS openclaw_webhook_url TEXT NOT NULL DEFAULT '';

ALTER TABLE users
ADD COLUMN IF NOT EXISTS notification_preferences JSONB NOT NULL DEFAULT '{}'::jsonb;
