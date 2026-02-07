ALTER TABLE users
ADD COLUMN role TEXT NOT NULL DEFAULT 'owner'
CHECK (role IN ('owner', 'maintainer', 'member', 'viewer'));

CREATE INDEX users_org_role_idx ON users (org_id, role);
