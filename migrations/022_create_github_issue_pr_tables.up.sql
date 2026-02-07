CREATE TABLE project_github_issues (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    repository_full_name TEXT NOT NULL,
    github_number BIGINT NOT NULL,
    github_node_id TEXT,
    title TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('open', 'closed')),
    body TEXT,
    author_login TEXT,
    is_pull_request BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, repository_full_name, github_number)
);

CREATE INDEX project_github_issues_org_project_idx ON project_github_issues (org_id, project_id, state, updated_at DESC);
CREATE INDEX project_github_issues_repo_number_idx ON project_github_issues (repository_full_name, github_number);

CREATE TABLE project_github_pull_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    issue_id UUID REFERENCES project_github_issues(id) ON DELETE SET NULL,
    repository_full_name TEXT NOT NULL,
    github_number BIGINT NOT NULL,
    github_node_id TEXT,
    title TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('open', 'closed')),
    draft BOOLEAN NOT NULL DEFAULT FALSE,
    mergeable BOOLEAN,
    mergeable_state TEXT,
    head_ref TEXT NOT NULL,
    head_sha TEXT NOT NULL,
    base_ref TEXT NOT NULL,
    base_sha TEXT,
    merged BOOLEAN NOT NULL DEFAULT FALSE,
    merged_at TIMESTAMPTZ,
    merged_commit_sha TEXT,
    author_login TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ,
    last_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, repository_full_name, github_number)
);

CREATE INDEX project_github_pull_requests_org_project_idx ON project_github_pull_requests (org_id, project_id, state, updated_at DESC);
CREATE INDEX project_github_pull_requests_repo_number_idx ON project_github_pull_requests (repository_full_name, github_number);
CREATE INDEX project_github_pull_requests_issue_id_idx ON project_github_pull_requests (issue_id);
CREATE INDEX project_github_pull_requests_merged_idx ON project_github_pull_requests (merged, merged_at DESC);
