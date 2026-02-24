CREATE TABLE memory_import (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    requested_by uuid REFERENCES human_user(id) ON DELETE SET NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','completed','failed')),
    file_key text NOT NULL,
    total_records integer,
    processed_records integer NOT NULL DEFAULT 0,
    imported_records integer NOT NULL DEFAULT 0,
    rejected_records integer NOT NULL DEFAULT 0,
    error_message text,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX memory_import_org_status_idx
    ON memory_import (organization_id, status);

