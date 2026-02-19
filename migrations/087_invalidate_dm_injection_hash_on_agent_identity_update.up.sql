CREATE OR REPLACE FUNCTION invalidate_dm_injection_hash_on_agent_identity_update()
RETURNS TRIGGER AS $$
BEGIN
    IF COALESCE(OLD.soul_md, '') IS DISTINCT FROM COALESCE(NEW.soul_md, '')
        OR COALESCE(OLD.identity_md, '') IS DISTINCT FROM COALESCE(NEW.identity_md, '')
        OR COALESCE(OLD.instructions_md, '') IS DISTINCT FROM COALESCE(NEW.instructions_md, '') THEN
        UPDATE dm_injection_state
        SET injection_hash = NULL,
            updated_at = NOW()
        WHERE org_id = NEW.org_id
          AND agent_id = NEW.id;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_dm_injection_hash_invalidate_on_agent_identity_update ON agents;
CREATE TRIGGER trg_dm_injection_hash_invalidate_on_agent_identity_update
AFTER UPDATE OF soul_md, identity_md, instructions_md ON agents
FOR EACH ROW
EXECUTE FUNCTION invalidate_dm_injection_hash_on_agent_identity_update();
