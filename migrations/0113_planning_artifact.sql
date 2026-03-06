CREATE TABLE planning_artifact (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    source_task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    artifact_kind text NOT NULL CHECK (artifact_kind IN (
        'discovery_plan',
        'strategy_artifact',
        'prd_spec',
        'backlog_story_pack',
        'pre_mortem_readiness_artifact',
        'metrics_framework',
        'gtm_launch_plan'
    )),
    artifact_slug text NOT NULL,
    title text NOT NULL,
    repo_path text NOT NULL,
    current_version integer NOT NULL DEFAULT 1 CHECK (current_version > 0),
    latest_content_sha256 text NOT NULL,
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT planning_artifact_project_repo_path_unique UNIQUE (project_id, repo_path)
);

CREATE INDEX planning_artifact_project_updated_desc_idx
    ON planning_artifact (project_id, updated_at DESC);

CREATE INDEX planning_artifact_source_task_updated_desc_idx
    ON planning_artifact (source_task_id, updated_at DESC);

CREATE TRIGGER planning_artifact_set_updated_at
BEFORE UPDATE ON planning_artifact
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TABLE planning_artifact_version (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    artifact_id uuid NOT NULL REFERENCES planning_artifact(id) ON DELETE CASCADE,
    source_task_id uuid NOT NULL REFERENCES project_task(id) ON DELETE CASCADE,
    version_number integer NOT NULL CHECK (version_number > 0),
    title text NOT NULL,
    repo_path text NOT NULL,
    content_sha256 text NOT NULL,
    byte_size integer NOT NULL CHECK (byte_size >= 0),
    created_by_type text NOT NULL CHECK (created_by_type IN ('human_user', 'agent', 'system')),
    created_by_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT planning_artifact_version_artifact_version_unique UNIQUE (artifact_id, version_number)
);

CREATE INDEX planning_artifact_version_artifact_version_desc_idx
    ON planning_artifact_version (artifact_id, version_number DESC);

CREATE INDEX planning_artifact_version_source_task_created_desc_idx
    ON planning_artifact_version (source_task_id, created_at DESC);
