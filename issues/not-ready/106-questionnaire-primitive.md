# Issue #106: Questionnaire Primitive

> ⚠️ **NOT READY FOR WORK** — This issue is still being specced. Do not begin implementation until this banner is removed.

## Problem

When agents need information from humans, the current pattern is broken:

1. Agent writes a list of 5-7 questions in prose
2. Human responds in a wall of text
3. Agent tries to parse which answer maps to which question
4. Things get lost, misattributed, or half-answered
5. Follow-up questions create more chaos

This happens constantly — Planners kicking back questions, issue creation needing details, review decisions with conditions, deploy checklists. It's one of the most common friction points in human-agent communication.

## Solution

A **questionnaire** is a structured form that agents can create and humans can fill out. It works in both issue threads and chat conversations.

### What a Questionnaire Looks Like

Agent creates:
```json
{
  "type": "questionnaire",
  "title": "Design decisions for onboarding flow",
  "questions": [
    {
      "id": "q1",
      "text": "Should we use WebSockets or polling for real-time updates?",
      "type": "select",
      "options": ["WebSocket", "Long polling", "Either — your call"],
      "required": true
    },
    {
      "id": "q2",
      "text": "Target response time for initial page load?",
      "type": "text",
      "placeholder": "e.g., under 2 seconds",
      "required": true
    },
    {
      "id": "q3",
      "text": "Should this work offline?",
      "type": "boolean",
      "default": false
    },
    {
      "id": "q4",
      "text": "Any specific mobile considerations?",
      "type": "text",
      "required": false
    },
    {
      "id": "q5",
      "text": "Priority for this work?",
      "type": "select",
      "options": ["P0 — Drop everything", "P1 — This week", "P2 — Soon", "P3 — When we get to it"],
      "default": "P2 — Soon"
    },
    {
      "id": "q6",
      "text": "Which platforms need support?",
      "type": "multiselect",
      "options": ["Desktop web", "Mobile web", "iOS app", "Android app"],
      "required": true
    }
  ]
}
```

Human sees a rendered form and fills it out. Agent gets structured responses:
```json
{
  "questionnaire_id": "abc123",
  "responses": {
    "q1": "WebSocket",
    "q2": "Under 1.5 seconds",
    "q3": true,
    "q4": "",
    "q5": "P1 — This week",
    "q6": ["Desktop web", "Mobile web"]
  }
}
```

### Field Types

| Type | Renders As | Value |
|------|-----------|-------|
| `text` | Text input (single line) | String |
| `textarea` | Multi-line text area | String |
| `boolean` | Yes/No toggle | Boolean |
| `select` | Dropdown / radio buttons | String (single selection) |
| `multiselect` | Checkboxes | Array of strings |
| `number` | Number input | Number |
| `date` | Date picker | ISO date string |

### Where Questionnaires Appear

1. **Issue comments** — Planner creates a questionnaire in the issue thread. Human fills it out. Planner reads the structured response and continues planning.

2. **Chat messages** — Agent sends a questionnaire in the global chat. Human fills it out inline. Response is stored as a chat message with structured data.

3. **Issue creation templates** — Projects can define a questionnaire template that's shown when creating new issues. Ensures the human provides the right information upfront.

4. **Review checklists** — Reviewer creates a checklist questionnaire: "Does this meet X? Does Y work? Any concerns about Z?" Human responds with structured yes/no + notes.

5. **Deploy confirmation** — Before manual deploy: "Confirm: staging tested? ☐ Rollback plan in place? ☐ Notify users? ☐"

### How Agents Create Questionnaires

**In chat:** Agent includes a questionnaire JSON block in its response. The frontend detects and renders it as a form.

**Via API:**
```
POST /api/issues/{id}/questionnaire
POST /api/chat/{conversation_id}/questionnaire
```

**Via CLI:**
```bash
otter issue ask --project X 1 --question "WebSockets or polling?" --type select --options "WebSocket,Polling,Either"
```

### How Humans Respond

**Web UI:** Click/type into the rendered form, hit submit.

**Chat (Slack/Discord/etc.):** The bridge renders the questionnaire as a formatted message. Human can either:
- Reply with numbered answers: "1. WebSocket 2. Under 1.5s 3. Yes"
- Or use a thread with inline responses

The bridge parses the response and structures it. Best effort — structured UI is always better, but text fallback works.

**CLI:**
```bash
otter issue respond --project X 1 --questionnaire abc123 --q1 "WebSocket" --q3 true
```

### Data Model

```sql
CREATE TABLE questionnaires (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  org_id UUID NOT NULL,
  context_type TEXT NOT NULL,  -- 'issue', 'chat', 'template'
  context_id UUID NOT NULL,     -- issue_id or conversation_id
  author TEXT NOT NULL,
  title TEXT,
  questions JSONB NOT NULL,
  responses JSONB,              -- NULL until answered
  responded_by TEXT,
  responded_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
```

### Rendering

In the web UI, questionnaires render as styled form cards within the message/comment stream. Before response: interactive form. After response: read-only display showing Q&A pairs.

In Slack/Discord: rendered as formatted text blocks with the best approximation of form elements available on that platform.

## Use Cases

| Scenario | Who Creates | Who Responds | Where |
|----------|-------------|-------------|-------|
| Planner needs design decisions | Planner agent | Human | Issue thread |
| Issue creation template | Project config | Human | Issue create form |
| Review checklist | Reviewer agent | Human | Issue thread |
| Deploy confirmation | System | Human | Issue or chat |
| Agent needs clarification | Any agent | Human | Chat |
| Performance review questions | System | Agent (self-review) | Agent detail page |
| Onboarding setup | System | Human | Settings |

## Relationship to #105

Questionnaires are how the Planner "kicks back questions to the human" (from the diagram). Instead of a messy prose exchange, the Planner creates a structured questionnaire, the human fills it out, and the Planner has clean data to continue planning.

## Files to Create/Modify

- `migrations/` — questionnaires table
- `internal/api/questionnaires.go` — CRUD + response handling
- `internal/api/router.go` — new routes
- `web/src/components/Questionnaire.tsx` — form renderer
- `web/src/components/QuestionnaireResponse.tsx` — read-only display
- `web/src/components/chat/GlobalChatSurface.tsx` — detect and render questionnaires in chat
- `bridge/openclaw-bridge.ts` — render questionnaires for Slack/chat surfaces
- `cmd/otter/main.go` — `otter issue ask` and `otter issue respond` commands
