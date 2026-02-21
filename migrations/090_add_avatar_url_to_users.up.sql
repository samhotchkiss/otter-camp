-- Add avatar_url to users table (referenced by settings profile)
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_url TEXT;

-- Settings: notification preferences per user
CREATE TABLE IF NOT EXISTS user_notification_settings (
    user_id TEXT PRIMARY KEY REFERENCES users(id),
    preferences JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Settings: org-level settings (integrations, webhooks, etc.)
CREATE TABLE IF NOT EXISTS org_settings (
    org_id TEXT PRIMARY KEY,
    openclaw_webhook_url TEXT,
    settings JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Settings: API keys for integrations
CREATE TABLE IF NOT EXISTS api_keys (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    org_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    key_hash TEXT NOT NULL,
    prefix TEXT NOT NULL DEFAULT '',
    scopes TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ
);
