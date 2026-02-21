DROP TRIGGER IF EXISTS memories_updated_at_trg ON memories;
DROP TRIGGER IF EXISTS conversations_updated_at_trg ON conversations;
DROP TRIGGER IF EXISTS rooms_updated_at_trg ON rooms;

DROP TABLE IF EXISTS room_participants;
DROP TABLE IF EXISTS chat_messages;
DROP TABLE IF EXISTS memories;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS rooms;
