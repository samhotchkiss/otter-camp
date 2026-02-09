ALTER TABLE projects
    ADD COLUMN require_human_review BOOLEAN NOT NULL DEFAULT false;
