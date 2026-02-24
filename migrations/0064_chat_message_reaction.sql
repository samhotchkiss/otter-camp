CREATE TABLE chat_message_reaction (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id uuid NOT NULL REFERENCES chat_message(id) ON DELETE CASCADE,
    session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    reactor_type text NOT NULL CHECK (reactor_type IN ('human_user', 'agent')),
    reactor_id uuid NOT NULL,
    emoji text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON COLUMN chat_message_reaction.reactor_id IS 'soft ref polymorphic: human_user.id | agent.id';

CREATE UNIQUE INDEX chat_message_reaction_unique_actor_emoji_uidx
    ON chat_message_reaction (message_id, reactor_type, reactor_id, emoji);

CREATE INDEX chat_message_reaction_message_idx
    ON chat_message_reaction (message_id, created_at ASC);

CREATE INDEX chat_message_reaction_session_idx
    ON chat_message_reaction (session_id, created_at DESC);
