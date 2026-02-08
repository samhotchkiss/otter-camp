# Issue #104: Agent Performance Reviews

> ⚠️ **NOT READY FOR WORK** — This issue is still being specced. Do not begin implementation until this banner is removed.

## Summary

A structured pathway for the human to conduct performance review conversations with agents — reviewing their work, giving feedback, identifying growth areas, and leveling them up over time.

## Vision

Agents improve through feedback, not just through code updates. A performance review is a conversation where the human:

1. **Reviews recent work** — what the agent shipped, quality, velocity, autonomy
2. **Gives feedback** — what went well, what didn't, patterns to fix
3. **Sets expectations** — what "leveling up" looks like for this agent's role
4. **Updates the agent's identity** — review conclusions get written to SOUL.md, IDENTITY.md, or memory files so the agent actually internalizes them

The output isn't a score — it's a better agent.

## Key Design Questions

- **Where does the review happen?** Dedicated chat thread per review? A review page? A structured form + conversation hybrid?
- **What data feeds into it?** Issue completion rate, commit history, response quality, peer feedback from other agents?
- **What's the review cadence?** Weekly? Monthly? On-demand? Triggered by milestones?
- **How does feedback persist?** Written to agent files (SOUL.md, memory/)? Stored in Otter Camp? Both?
- **Can agents self-review?** Pre-fill a self-assessment before the human reviews?
- **Multi-agent comparison?** Dashboard showing all agents' growth trajectories?

## Possible Features

### Review Session
- Initiate a review from the agent detail page
- Pre-populated with agent's recent activity (issues closed, commits, chat volume)
- Structured sections: Wins, Growth Areas, Action Items
- Conversation thread where human and agent discuss
- Agent can respond, reflect, ask questions

### Growth Tracking
- History of past reviews visible on agent profile
- Track improvement over time on specific dimensions
- Agent "level" or maturity indicator (optional — could be too gamified)

### Feedback Integration
- Review conclusions automatically written to agent's SOUL.md or memory
- Agent reads these at next session start and adjusts behavior
- "Lessons learned" section that persists across sessions

### Peer Review (Future)
- Agents review each other's work (e.g., Jeremy H reviews Derek's code)
- Cross-agent feedback aggregated for the human

## Dependencies

- Issue #103 (Agent Management) — agent detail page needed as the home for reviews
- Agent identity files accessible from Otter Camp (part of #103 vision)

## Files to Create/Modify

- TBD — depends on design decisions above
