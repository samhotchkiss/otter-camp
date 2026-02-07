ALTER TABLE project_repo_bindings
ADD COLUMN force_push_required BOOLEAN NOT NULL DEFAULT FALSE;
