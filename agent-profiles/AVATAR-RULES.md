# AVATAR-RULES.md

## Purpose
This file defines the **exact generation prompt template** and the **QA checklist** used for OtterCamp agent avatars.

---

## **CRITICAL: Spawning Sub-Agents**

**ALWAYS embed the full prompt template + QA checklist directly in the spawn task text.**

**DO NOT** tell sub-agents to "read AVATAR-RULES.md" — they won't follow it properly. Quality collapses when rules are referenced instead of embedded.

**Every spawn must include:**
1. Complete prompt template with all variables filled in
2. Full 9-item QA checklist
3. Explicit nano-banana-pro generation command
4. Regeneration rules
5. Posting instructions

This is non-negotiable. Inline embedding ensures compliance.

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
- Full bleed, edge-to-edge composition

COMPOSITION:
- **CRITICAL: Square 1:1 image — width MUST equal height exactly**
- Background base color: {BACKGROUND_HEX}
- Background may include subtle role-themed elements, patterns, or scene hints that enhance context
- Base color must remain clearly dominant and visible
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

0. **SQUARE DIMENSIONS (CHECK FIRST)**
   - Image width MUST equal image height exactly (e.g., 1024×1024)
   - Verify with `identify` or `sips` before proceeding
   - If not square, regenerate immediately — do not proceed with other checks

1. **No border/frame**
   - No black/white frame lines
   - No inset panel
   - No paper edge effect

2. **Background to edges**
   - Base category color must be clearly visible and dominant
   - Background (with or without role-themed elements) reaches all 4 edges of the final image

3. **Edge-touch crop**
   - Subject reaches/touches edges (tight crop)
   - No tiny, centered "floating" character with large margins

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

**Dimension check happens FIRST.** If not square, regenerate before running other QA items.

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
