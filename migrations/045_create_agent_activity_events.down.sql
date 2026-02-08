DROP POLICY IF EXISTS agent_activity_events_org_isolation ON agent_activity_events;

DROP INDEX IF EXISTS idx_agent_activity_events_status;
DROP INDEX IF EXISTS idx_agent_activity_events_trigger_started;
DROP INDEX IF EXISTS idx_agent_activity_events_project_started;
DROP INDEX IF EXISTS idx_agent_activity_events_org_started;
DROP INDEX IF EXISTS idx_agent_activity_events_agent_started;

DROP TABLE IF EXISTS agent_activity_events;
