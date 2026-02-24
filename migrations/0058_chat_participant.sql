CREATE TABLE chat_participant (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
    participant_type text NOT NULL CHECK (participant_type IN ('human_user', 'agent')),
    participant_id uuid NOT NULL,
    role text NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'member', 'observer')),
    notification_preference text NOT NULL DEFAULT 'all' CHECK (notification_preference IN ('all', 'mentions', 'none')),
    joined_at timestamptz NOT NULL DEFAULT now(),
    removed_at timestamptz
);

COMMENT ON COLUMN chat_participant.participant_id IS 'soft ref polymorphic: human_user.id | agent.id';

CREATE UNIQUE INDEX chat_participant_session_actor_active_uidx
    ON chat_participant (session_id, participant_type, participant_id)
    WHERE removed_at IS NULL;

CREATE INDEX chat_participant_actor_active_idx
    ON chat_participant (participant_type, participant_id)
    WHERE removed_at IS NULL;
