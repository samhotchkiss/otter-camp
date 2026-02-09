DROP POLICY IF EXISTS issue_labels_org_isolation ON issue_labels;
DROP POLICY IF EXISTS project_labels_org_isolation ON project_labels;
DROP POLICY IF EXISTS labels_org_isolation ON labels;

DROP INDEX IF EXISTS idx_issue_labels_issue;
DROP INDEX IF EXISTS idx_issue_labels_label;
DROP INDEX IF EXISTS idx_project_labels_label;
DROP INDEX IF EXISTS idx_labels_org_name;
DROP INDEX IF EXISTS idx_labels_org;

DROP TABLE IF EXISTS issue_labels;
DROP TABLE IF EXISTS project_labels;
DROP TABLE IF EXISTS labels;
