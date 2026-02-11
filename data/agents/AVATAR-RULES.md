# AVATAR-RULES.md

## Purpose
This file defines the **exact generation prompt template** and the **QA checklist** used for OtterCamp agent avatars.

---

## Canonical Avatar Generation Prompt (Template)

Use this exact template, replacing the placeholders:

```text
Create a square avatar of an anthropomorphic otter in classic woodcut style matching the approved aesthetic.

STYLE:
- Hand-inked woodcut / linocut look
- Bold black linework
- Visible crosshatching and carved-stroke texture
- Natural warm fur tones (tan/brown/cream), never monochrome color wash

COMPOSITION:
- Square 1:1 image
- Solid background color: {BACKGROUND_HEX}
- Background must be full bleed to all edges
- No border, no frame, no inset rectangle, no matte, no vignette
- Tight crop: head/shoulders should reach image edges

CONTENT RULES:
- Exactly one coherent otter face (no duplicate faces/features)
- No text, labels, logos, symbols, badges, or watermarks
- No extra panels or poster-like framing
- Expression must be neutral or friendly
- Must NOT look angry, scary, hostile, or mean
- Avoid stereotyped, harmful, or caricatured features
- Fur and clothing must be clearly distinguishable from background color

CHARACTER:
- Name: {DISPLAY_NAME}
- Role: {ROLE_NAME}
- Pronoun/gender cue handling: {GENDER_CUE}.  Should be easily identified as the assigned gender.
- Distinctive accessories/features: {ACCESSORY_SET}
- Keep high silhouette contrast so avatar is recognizable at very small size
```

---

## Per-Avatar Variables

- `{BACKGROUND_HEX}`: from category color map
- `{DISPLAY_NAME}`: from `roster_entry.json`
- `{ROLE_NAME}`: from `roster_entry.json`
- `{GENDER_CUE}`:
  - `she/her` → feminine-coded cues
  - `he/him` → masculine-coded cues
  - other → androgynous cues
- `{ACCESSORY_SET}`: role-distinct styling (glasses, hat, scarf, headset, jacket, braid, etc.)

---

Category color map:
• Engineering & Development — #E8723A (Burnt Orange)
• Design & Creative — #9B59B6 (Royal Purple)
• Content & Writing — #3498DB (Ocean Blue)
• Data & AI/ML — #2ECC71 (Emerald Green)
• DevOps & Infrastructure — #E74C3C (Signal Red)
• Marketing & Growth — #F39C12 (Amber Gold)
• Business & Operations — #1ABC9C (Teal)
• Health & Wellness — #27AE60 (Forest Green)
• Personal & Home — #8E44AD (Deep Violet)
• Finance & Legal — #2C3E50 (Charcoal Navy)
• Security & Quality — #C0392B (Crimson)
• Research & Education — #16A085 (Deep Teal)
• Leadership & Strategy — #34495E (Slate Grey)
• Generalist / Multi — #7F8C8D (Warm Grey)

---

## QA Checklist (Must Pass Before Posting)

For each generated avatar, validate all items:

1. **No border/frame**
   - No black/white frame lines
   - No inset panel
   - No paper edge effect

2. **Background to edges**
   - Solid category color reaches all 4 edges of the final image

3. **Edge-touch crop**
   - Subject reaches/touches edges (tight crop)
   - No tiny, centered “floating” character with large margins

4. **Single-face integrity**
   - Exactly one face
   - No extra eyes, duplicate muzzle, doubled head, or other anatomical artifacts

5. **Expression safety/style**
   - Neutral or friendly expression only
   - Reject if angry/scary/mean/intimidating

6. **Color separation**
   - Fur tones remain warm natural tan/brown/cream
   - Fur/clothing must not blend into background
   - Reject monochrome/cyan/flat colorized skin look

7. **No text/symbol overlays**
   - No names, titles, badges, logos, icons, or random marks

8. **Small-size readability**
   - Distinct silhouette and role cues visible at thumbnail size

---

## Regeneration Rule

If any single checklist item fails:
- Regenerate immediately
- Re-run QA
- Only post once all checks pass

---

## Posting Rule

When a new avatar passes QA:
1. Save to agent folder as:
   - `data/agents/{role-id}/avatar.png`
2. Post to Slack channel immediately
3. Check Slack for stop/update message before generating next avatar

---

## Quick Runbook (Copy/Paste)

### 1) Generate one avatar (single role)

```bash
# Example vars
ROLE_ID="api-designer"
DISPLAY_NAME="Pedro Santiago"
ROLE_NAME="API Designer"
BACKGROUND_HEX="#E8723A"
GENDER_CUE="feminine-coded"
ACCESSORY_SET="wireframe glasses, collared shirt, vest"
OUT="2026-02-11-${ROLE_ID}-avatar.png"

PROMPT="Create a square avatar of an anthropomorphic otter in classic woodcut style matching the approved aesthetic. Hand-inked linocut look, bold black linework, crosshatching, warm natural fur tones. Solid ${BACKGROUND_HEX} background full bleed to all edges. No border/frame/inset/matte/vignette/text/logo. Tight crop so head/shoulders reach edges. Exactly one coherent face. Expression neutral or friendly, never angry/scary/mean. Avoid stereotypes/caricatures. Name: ${DISPLAY_NAME}. Role: ${ROLE_NAME}. ${GENDER_CUE}. Distinctive features: ${ACCESSORY_SET}."

uv run /Users/sam/.npm-global/lib/node_modules/openclaw/skills/nano-banana-pro/scripts/generate_image.py \
  --prompt "$PROMPT" \
  --filename "$OUT" \
  --resolution 1K
```

### 2) Save to agent folder

```bash
cp "/Users/sam/.openclaw/workspace-avatar-design/avatars/batch20/${OUT}" \
   "/Users/sam/Documents/Dev/otter-camp/data/agents/${ROLE_ID}/avatar.png"
```

### 3) QA check before posting (manual gate)

```text
PASS required on all:
- no border/frame
- background reaches all edges
- subject touches edges (tight crop)
- single coherent face (no duplicate features)
- neutral/friendly expression
- warm fur tones distinct from background
- no text/logo/symbols
- readable at small thumbnail size
```

### 4) Post to Slack after PASS

Use OpenClaw `message` tool with:
- `action=send`
- `channel=slack`
- `target=<DM or channel id>`
- `filePath=/Users/sam/Documents/Dev/otter-camp/data/agents/${ROLE_ID}/avatar.png`

### 5) Stop-check between each generation

Before generating the next avatar, check Slack for any new instruction (especially stop/pause/change requests).
