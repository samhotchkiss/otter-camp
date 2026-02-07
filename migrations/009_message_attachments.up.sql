-- Add attachments support to comments (messages)
-- Attachments stored as JSONB array: [{filename, size, mime_type, url, thumbnail_url}]

ALTER TABLE comments ADD COLUMN attachments JSONB DEFAULT '[]';

-- Index for querying messages with attachments
CREATE INDEX comments_attachments_idx ON comments USING GIN (attachments);

-- Create attachments storage table for metadata tracking
CREATE TABLE attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    comment_id UUID REFERENCES comments(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    mime_type TEXT NOT NULL,
    storage_key TEXT NOT NULL UNIQUE,
    url TEXT NOT NULL,
    thumbnail_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX attachments_org_idx ON attachments(org_id);
CREATE INDEX attachments_comment_idx ON attachments(comment_id);
