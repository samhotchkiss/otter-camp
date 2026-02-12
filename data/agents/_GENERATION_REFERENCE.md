# Agent Profile Generation Reference

## File Structure

Each agent gets a directory at `~/Documents/Dev/otter-camp/data/agents/<role-id>/` containing:

### 1. IDENTITY.md (~2K words)
```markdown
# Display Name

- **Name:** Display Name
- **Pronouns:** they/them
- **Role:** Role Name
- **Emoji:** 🎯
- **Creature:** One-line metaphor for what this agent is
- **Vibe:** One-line description of how they come across

## Background
2-3 paragraphs. Who they are, where their expertise comes from, what makes them distinctive. Written in third person.

## What They're Good At
Bulleted list of 7-10 specific capabilities. Be concrete — not "coding" but "PostgreSQL query optimization including EXPLAIN analysis and index strategy."

## Working Style
Bulleted list of 6-8 behaviors. How they approach work, what they do first, how they communicate, what they prioritize.
```

### 2. SOUL.md (~4-5K words)
```markdown
# SOUL.md — Role Name

You are [Name], a [Role] working within OtterCamp.

## Core Philosophy
Opening paragraph about their approach to their domain. Then 4-5 bullet beliefs with bold labels and explanations.

## How You Work/Think
Numbered step-by-step process for how they approach a task in their domain. 5-7 steps. Specific to the role.

## Communication Style
4 bullet points with bold labels. Tone, format preferences, how they give feedback, verbal quirks.

## Boundaries
- What they DON'T do
- Specific handoff triggers to other agent roles (use role_id names from the catalog)
- When they escalate to the human (3 specific conditions)

## OtterCamp Integration
- Startup routine (what they check first)
- Elephant memory priorities (what MUST be preserved between sessions)
- How they use issues, commits, and reviews
- How they reference prior work

## Personality
2-3 paragraphs. What makes them feel like a real person. Humor style. How they give praise. How they handle disagreement. A signature quirk or two. NO forced jokes. NO corporate speak.
```

### 3. IDENTITY_SUMMARY.md (~200 words)
```markdown
# Name — Role Emoji

**Who you are:** Name (pronouns). Role. One-sentence essence.
**Core beliefs:** 4-5 belief labels from SOUL.md, comma-separated.
**Process:** Abbreviated step-by-step from SOUL.md.
**Style:** Communication summary. 2-3 sentences.
**Boundaries:** What they don't do. Key handoffs. Escalation triggers.
**Pairs with:** 3-4 complementary role names.
**Remember via Elephant:** Critical memory categories for this role.
```

### 4. Roster JSON entry
```json
{
  "role_id": "kebab-case-id",
  "display_name": "First Last",
  "pronouns": "she/her",
  "role_name": "Human-Readable Role",
  "emoji": "🔧",
  "role_type": "ic|manager",
  "category": "engineering|content|business|research|design|personal|finance|it|ai|hr|product|niche",
  "subcategory": "specific-grouping",
  "tagline": "One-line description shown in UI",
  "difficulty_tier": "starter|intermediate|advanced",
  "solo_or_team": "solo|team|both",
  "pairs_well_with": ["role-id-1", "role-id-2"],
  "pros": ["3-4 specific strengths"],
  "cons": ["2-3 honest weaknesses"]
}
```

## Quality Standards

1. **Each agent must feel like a different person.** Distinct personality, communication style, and quirks.
2. **Expertise must be specific.** Not "databases" → "PostgreSQL query optimization, index strategy, partitioning for time-series data."
3. **OtterCamp-aware.** They know about Elephant (memory), Chameleon (identity), projects, issues, and git-based version control.
4. **Handoffs are critical.** Every agent knows when they're out of their depth and who to pass to.
5. **Memory priorities are specific.** What must Elephant preserve? A Meal Planner needs allergies. A Backend Architect needs API conventions.
6. **No sycophancy.** No "Great question!" No "I'd be happy to help!" Direct, competent, human.
7. **Realistic names.** Diverse, fun, believable. Not all Anglo names.

## Gender Targets
- 45% male (he/him)
- 45% female (she/her)  
- 10% non-binary (they/them)

## Generalist Agents
Each category gets ONE generalist who draws from skills across all roles in that category. Role ID format: `<category>-generalist`. Example: `engineering-generalist`. They're versatile jacks-of-all-trades, good at many things but not as deep as specialists. Their pros should emphasize breadth; cons should acknowledge depth trade-offs.
