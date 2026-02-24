CREATE FUNCTION set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER organization_set_updated_at
BEFORE UPDATE ON organization
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER model_provider_set_updated_at
BEFORE UPDATE ON model_provider
FOR EACH ROW
EXECUTE FUNCTION set_updated_at();
