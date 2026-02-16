# Issue #113 — Voice Interaction

> STATUS: NOT READY — Sam has additional ideas to add

## Problem

Controlling an agent team through text is high-friction. You have to open a chat, type out instructions, wait for responses, then do it again for the next agent. If steering your agents takes more than 30 seconds, you'll stop doing it — and the system drifts.

Voice is the lowest-friction input method. You should be able to talk for 30 seconds while walking to lunch and have your entire agent team reprioritize.

## Concept

Voice notes → transcription → structured extraction → agent action. The full loop from spoken intent to agent execution in seconds.

### Flow

```
Voice Note (phone, desktop, watch, whatever)
    ↓
Transcription (Whisper / WisprFlow / OpenAI API)
    ↓
Structured Extraction (Memory Agent or dedicated processor)
    ├→ Priority changes     → update Living Priority Stack (#112)
    ├→ Direct instructions  → route to specific agent
    ├→ Decisions            → write to shared knowledge (#111)
    ├→ Context/preferences  → write to memory, scoped appropriately
    └→ Questions            → route to relevant agent, surface answer
    ↓
Agents Act (reprioritize, execute, respond)
    ↓
Summary Back (voice or text confirmation of what happened)
```

### Input Methods

- **Mobile** — voice note in Telegram/Signal/iMessage → OpenClaw receives → processes
- **Desktop** — hotkey → record → transcribe → process
- **OtterCamp UI** — record button in dashboard
- **CLI** — `otter voice` command with microphone input
- **Wearable** — Apple Watch voice note → routes through phone

### Key Design Questions

- How does the system distinguish between "tell Frank to do X" vs "I'm thinking out loud"?
- Should voice responses come back as audio (TTS) or text?
- Real-time streaming transcription vs batch (record → send → process)?
- How to handle corrections ("actually, scratch that, I meant...")?

## Integration Points

- **#111 Memory System** — voice context gets extracted as memories, scoped appropriately
- **#112 Living Priority Stack** — voice is the primary input method for priority changes
- **#105 Pipeline** — voice approvals ("approve it", "reject, needs more detail")
- **OpenClaw** — already supports voice notes via Telegram/Signal

## Dependencies

- [ ] #111 — Memory Infrastructure (for structured extraction + distribution)
- [ ] #112 — Living Priority Stack (for priority updates)
- [ ] Transcription service (Whisper API, local Whisper, or WisprFlow)

## Sam's Ideas

*(To be filled in — Sam mentioned having a bunch of ideas for this)*
