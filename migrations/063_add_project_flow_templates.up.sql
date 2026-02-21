CREATE TABLE project_flow_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX project_flow_templates_project_idx
    ON project_flow_templates (project_id, created_at DESC);

CREATE UNIQUE INDEX project_flow_templates_project_default_idx
    ON project_flow_templates (project_id)
    WHERE is_default;

CREATE TABLE project_flow_template_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    flow_template_id UUID NOT NULL REFERENCES project_flow_templates(id) ON DELETE CASCADE,
    step_order INTEGER NOT NULL,
    step_key TEXT NOT NULL,
    label TEXT NOT NULL,
    role TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX project_flow_template_steps_order_idx
    ON project_flow_template_steps (flow_template_id, step_order);

CREATE UNIQUE INDEX project_flow_template_steps_key_idx
    ON project_flow_template_steps (flow_template_id, step_key);

ALTER TABLE project_issues
    ADD COLUMN flow_template_id UUID REFERENCES project_flow_templates(id) ON DELETE SET NULL,
    ADD COLUMN flow_step_key TEXT,
    ADD COLUMN flow_step_index INTEGER;

CREATE INDEX project_issues_flow_template_idx
    ON project_issues (flow_template_id);
