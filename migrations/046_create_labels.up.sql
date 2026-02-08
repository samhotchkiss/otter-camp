CREATE TABLE IF NOT EXISTS labels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '#6b7280',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, name)
);

CREATE INDEX IF NOT EXISTS idx_labels_org ON labels(org_id);
CREATE INDEX IF NOT EXISTS idx_labels_org_name ON labels(org_id, name);

ALTER TABLE labels ENABLE ROW LEVEL SECURITY;
ALTER TABLE labels FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS labels_org_isolation ON labels;
CREATE POLICY labels_org_isolation ON labels
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

CREATE TABLE IF NOT EXISTS project_labels (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    label_id UUID NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, label_id)
);

CREATE INDEX IF NOT EXISTS idx_project_labels_label ON project_labels(label_id);

ALTER TABLE project_labels ENABLE ROW LEVEL SECURITY;
ALTER TABLE project_labels FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS project_labels_org_isolation ON project_labels;
CREATE POLICY project_labels_org_isolation ON project_labels
    USING (
        EXISTS (
            SELECT 1 FROM projects p
            WHERE p.id = project_labels.project_id
              AND p.org_id = current_org_id()
        )
        AND EXISTS (
            SELECT 1 FROM labels l
            WHERE l.id = project_labels.label_id
              AND l.org_id = current_org_id()
        )
    )
    WITH CHECK (
        EXISTS (
            SELECT 1 FROM projects p
            WHERE p.id = project_labels.project_id
              AND p.org_id = current_org_id()
        )
        AND EXISTS (
            SELECT 1 FROM labels l
            WHERE l.id = project_labels.label_id
              AND l.org_id = current_org_id()
        )
    );

CREATE TABLE IF NOT EXISTS issue_labels (
    issue_id UUID NOT NULL REFERENCES project_issues(id) ON DELETE CASCADE,
    label_id UUID NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (issue_id, label_id)
);

CREATE INDEX IF NOT EXISTS idx_issue_labels_label ON issue_labels(label_id);
CREATE INDEX IF NOT EXISTS idx_issue_labels_issue ON issue_labels(issue_id);

ALTER TABLE issue_labels ENABLE ROW LEVEL SECURITY;
ALTER TABLE issue_labels FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS issue_labels_org_isolation ON issue_labels;
CREATE POLICY issue_labels_org_isolation ON issue_labels
    USING (
        EXISTS (
            SELECT 1 FROM project_issues i
            WHERE i.id = issue_labels.issue_id
              AND i.org_id = current_org_id()
        )
        AND EXISTS (
            SELECT 1 FROM labels l
            WHERE l.id = issue_labels.label_id
              AND l.org_id = current_org_id()
        )
    )
    WITH CHECK (
        EXISTS (
            SELECT 1 FROM project_issues i
            WHERE i.id = issue_labels.issue_id
              AND i.org_id = current_org_id()
        )
        AND EXISTS (
            SELECT 1 FROM labels l
            WHERE l.id = issue_labels.label_id
              AND l.org_id = current_org_id()
        )
    );
