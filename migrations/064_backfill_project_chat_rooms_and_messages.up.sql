INSERT INTO rooms (
    org_id,
    name,
    type,
    context_id,
    created_at,
    updated_at
)
SELECT
    p.org_id,
    p.name,
    'project',
    p.id,
    NOW(),
    NOW()
FROM projects p
WHERE EXISTS (
    SELECT 1
    FROM project_chat_messages pcm
    WHERE pcm.org_id = p.org_id
      AND pcm.project_id = p.id
)
AND NOT EXISTS (
    SELECT 1
    FROM rooms r
    WHERE r.org_id = p.org_id
      AND r.type = 'project'
      AND r.context_id = p.id
);

INSERT INTO chat_messages (
    id,
    org_id,
    room_id,
    sender_id,
    sender_type,
    body,
    type,
    attachments,
    created_at
)
SELECT
    pcm.id,
    pcm.org_id,
    r.id,
    (
        substr(md5(pcm.org_id::text || ':' || coalesce(nullif(trim(pcm.author), ''), 'unknown')), 1, 8) || '-' ||
        substr(md5(pcm.org_id::text || ':' || coalesce(nullif(trim(pcm.author), ''), 'unknown')), 9, 4) || '-' ||
        substr(md5(pcm.org_id::text || ':' || coalesce(nullif(trim(pcm.author), ''), 'unknown')), 13, 4) || '-' ||
        substr(md5(pcm.org_id::text || ':' || coalesce(nullif(trim(pcm.author), ''), 'unknown')), 17, 4) || '-' ||
        substr(md5(pcm.org_id::text || ':' || coalesce(nullif(trim(pcm.author), ''), 'unknown')), 21, 12)
    )::uuid,
    CASE
        WHEN pcm.author = '__otter_session__' OR pcm.author LIKE 'project_chat_session_reset:%' THEN 'system'
        ELSE 'user'
    END,
    pcm.body,
    CASE
        WHEN pcm.author = '__otter_session__' OR pcm.author LIKE 'project_chat_session_reset:%' THEN 'system'
        ELSE 'message'
    END,
    pcm.attachments,
    pcm.created_at
FROM project_chat_messages pcm
JOIN rooms r
  ON r.org_id = pcm.org_id
 AND r.type = 'project'
 AND r.context_id = pcm.project_id
ON CONFLICT (id) DO NOTHING;
