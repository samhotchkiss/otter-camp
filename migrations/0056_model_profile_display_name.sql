ALTER TABLE model_profile
    ADD COLUMN display_name text NOT NULL DEFAULT '';

UPDATE model_profile
SET display_name = model_name
WHERE display_name = '';
