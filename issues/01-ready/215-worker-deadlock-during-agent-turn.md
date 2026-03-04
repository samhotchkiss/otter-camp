# Issue 215: Worker deadlocks during agent_turn processing

## Summary

The worker process deadlocks/hangs during or shortly after processing `agent_turn` jobs. When this happens, the job queue polling loop stops logging entirely — no more "claiming pending jobs" debug messages appear. The process remains alive (0% CPU) but produces no output.

## Reproduction

1. Start worker: `./bin/ottercamp worker`
2. Send user messages to multiple task sessions via TUI
3. Worker claims and processes several `agent_turn` jobs
4. After processing 3-8 agent_turn jobs, worker stops producing log output
5. Job queue polling stops (no "claiming pending jobs" every 5 seconds)
6. Process is alive but hung (0% CPU, no I/O)

## Pattern

The last log entry before the hang is always:
```
level=INFO msg="consumer started" consumer_name=turn-engine.cancel.{turn_id}.{session_id} last_seq=0
```

This suggests the deadlock occurs in the cancel consumer setup or the subsequent LLM call.

## Impact

- All job processing stops (agent_turn, chat_summarize, schedule_tick, etc.)
- Must manually kill and restart the worker
- Blocks all agent work across all sessions

## Observed instances

- 06:04:17 MST — Worker PID 36390 hung after job 7a4203e9 (agent_turn attempts=2)
- 06:26:04 MST — Worker PID 29585 hung after job d9584117 (agent_turn attempts=3)
- 06:33:14 MST — Worker PID 42956 hung after job 680de878 (agent_turn attempts=1)

## Workaround

Manually kill and restart the worker when it hangs. The agent_turn jobs will be retried on the next worker instance.

## Possible cause

The cancel consumer uses NATS JetStream subscriptions. The deadlock may be related to:
- NATS consumer setup blocking on a channel
- Goroutine leak accumulating across multiple agent_turn jobs
- Database connection pool exhaustion from concurrent consumer queries
- Mutex contention in the turn engine between the main turn goroutine and the cancel consumer
