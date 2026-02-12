INSERT INTO memories (
    org_id,
    kind,
    title,
    content,
    metadata,
    importance,
    confidence,
    status,
    source_project_id,
    occurred_at,
    created_at,
    updated_at
)
SELECT
    me.org_id,
    CASE me.kind
        WHEN 'decision' THEN 'technical_decision'
        WHEN 'action_item' THEN 'process_outcome'
        WHEN 'summary' THEN 'context'
        WHEN 'feedback' THEN 'correction'
        ELSE me.kind
    END,
    me.title,
    me.content,
    COALESCE(me.metadata, '{}'::jsonb) || jsonb_build_object(
        'source_table', 'memory_entries',
        'source_id', me.id::text,
        'legacy_agent_id', me.agent_id::text,
        'legacy_source_session', me.source_session,
        'legacy_source_issue', me.source_issue
    ),
    me.importance,
    me.confidence,
    CASE me.status
        WHEN 'active' THEN 'active'
        WHEN 'warm' THEN 'archived'
        WHEN 'archived' THEN 'archived'
        ELSE 'archived'
    END,
    me.source_project,
    me.occurred_at,
    me.created_at,
    me.updated_at
FROM memory_entries me
WHERE NOT EXISTS (
    SELECT 1
    FROM memories m
    WHERE m.org_id = me.org_id
      AND m.metadata->>'source_table' = 'memory_entries'
      AND m.metadata->>'source_id' = me.id::text
)
ON CONFLICT (org_id, content_hash) WHERE status = 'active' DO NOTHING;

INSERT INTO memories (
    org_id,
    kind,
    title,
    content,
    metadata,
    importance,
    confidence,
    status,
    occurred_at,
    created_at,
    updated_at
)
SELECT
    sk.org_id,
    CASE sk.kind
        WHEN 'decision' THEN 'technical_decision'
        WHEN 'lesson' THEN 'lesson'
        WHEN 'preference' THEN 'preference'
        WHEN 'fact' THEN 'fact'
        WHEN 'pattern' THEN 'pattern'
        WHEN 'correction' THEN 'correction'
        ELSE 'context'
    END,
    sk.title,
    sk.content,
    COALESCE(sk.metadata, '{}'::jsonb) || jsonb_build_object(
        'source_table', 'shared_knowledge',
        'source_id', sk.id::text,
        'legacy_source_agent_id', sk.source_agent_id::text,
        'legacy_source_memory_id', sk.source_memory_id,
        'legacy_scope', sk.scope,
        'legacy_scope_teams', sk.scope_teams,
        'legacy_confirmations', sk.confirmations,
        'legacy_contradictions', sk.contradictions,
        'legacy_quality_score', sk.quality_score
    ),
    GREATEST(1, LEAST(5, ROUND(sk.quality_score * 5)::int))::smallint,
    sk.quality_score,
    CASE sk.status
        WHEN 'active' THEN 'active'
        WHEN 'stale' THEN 'archived'
        WHEN 'superseded' THEN 'deprecated'
        WHEN 'archived' THEN 'archived'
        ELSE 'archived'
    END,
    sk.occurred_at,
    sk.created_at,
    sk.updated_at
FROM shared_knowledge sk
WHERE NOT EXISTS (
    SELECT 1
    FROM memories m
    WHERE m.org_id = sk.org_id
      AND m.metadata->>'source_table' = 'shared_knowledge'
      AND m.metadata->>'source_id' = sk.id::text
)
ON CONFLICT (org_id, content_hash) WHERE status = 'active' DO NOTHING;

INSERT INTO memories (
    org_id,
    kind,
    title,
    content,
    metadata,
    importance,
    confidence,
    status,
    occurred_at,
    created_at,
    updated_at
)
SELECT
    am.org_id,
    'context',
    CASE am.kind
        WHEN 'daily' THEN 'Daily memory'
        WHEN 'long_term' THEN 'Long-term memory'
        WHEN 'note' THEN 'Agent note'
        ELSE 'Agent memory'
    END,
    am.content,
    jsonb_build_object(
        'source_table', 'agent_memories',
        'source_id', am.id::text,
        'legacy_agent_id', am.agent_id::text,
        'legacy_kind', am.kind,
        'legacy_date', am.date
    ),
    2,
    0.4,
    'active',
    COALESCE(am.date::timestamptz, am.created_at),
    am.created_at,
    am.updated_at
FROM agent_memories am
WHERE NOT EXISTS (
    SELECT 1
    FROM memories m
    WHERE m.org_id = am.org_id
      AND m.metadata->>'source_table' = 'agent_memories'
      AND m.metadata->>'source_id' = am.id::text
)
ON CONFLICT (org_id, content_hash) WHERE status = 'active' DO NOTHING;

UPDATE memories m
SET
    superseded_by = replacement.id,
    updated_at = GREATEST(m.updated_at, NOW())
FROM shared_knowledge sk
JOIN memories replacement
  ON replacement.org_id = sk.org_id
 AND replacement.metadata->>'source_table' = 'shared_knowledge'
 AND replacement.metadata->>'source_id' = sk.superseded_by::text
WHERE m.org_id = sk.org_id
  AND m.metadata->>'source_table' = 'shared_knowledge'
  AND m.metadata->>'source_id' = sk.id::text
  AND sk.superseded_by IS NOT NULL
  AND (m.superseded_by IS NULL OR m.superseded_by <> replacement.id);
