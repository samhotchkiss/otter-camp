CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.updated_at IS NOT DISTINCT FROM OLD.updated_at THEN
        NEW.updated_at = now();
    END IF;
    RETURN NEW;
END;
$$;
