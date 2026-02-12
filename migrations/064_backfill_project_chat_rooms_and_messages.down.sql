DELETE FROM chat_messages cm
USING project_chat_messages pcm
WHERE cm.id = pcm.id;

DELETE FROM rooms r
WHERE r.type = 'project'
  AND r.context_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1
      FROM chat_messages cm
      WHERE cm.room_id = r.id
  )
  AND NOT EXISTS (
      SELECT 1
      FROM room_participants rp
      WHERE rp.room_id = r.id
  );
