# Sam.blog — Site Architecture & Information Design

> Blueprint for design and development. Defines sitemap, content model, URL structure, navigation, and homepage layout.

---

## 1. Sitemap & Page Hierarchy

```
sam.blog/
├── /                        ← Homepage
├── /blog/                   ← Blog listing (all posts, filterable)
│   ├── /blog/[slug]         ← Individual blog post
│   └── /blog/topic/[topic]  ← Posts filtered by topic
├── /photography/            ← Curated photo gallery
├── /about/                  ← Sam's story & background
└── /contact/                ← Speaking, consulting, work inquiries
```

**Total page types: 5** (Homepage, Blog Index, Blog Post, Photography, About, Contact)

This is intentionally lean. A personal site earns trust through focus, not sprawl.

---

## 2. Content Taxonomy

### Decision: Tags only (no separate categories)

**Rationale:**
- Sam has ~5 topic areas. A two-tier system (categories + tags) adds complexity with no payoff at this scale.
- A flat tag model is simpler to implement in Astro content collections, simpler in URLs, and simpler for readers to understand.
- If the site grows to 200+ posts across 15 topics, we can introduce hierarchy later. Right now, flat tags do the job.

### Defined Topics (Tags)

| Tag slug          | Display name         | Description                                                    |
|-------------------|----------------------|----------------------------------------------------------------|
| `ethics`          | Ethics               | Moral reasoning, tech ethics, responsibility                   |
| `internet`        | Internet             | Web culture, platforms, digital life                           |
| `parenting`       | Parenting            | Raising kids, family, the intersection of tech and childhood   |
| `ai-orchestration`| AI & Orchestration   | Technical content — AI systems, agent orchestration, LLMs      |
| `leadership`      | Thought Leadership   | Industry perspective, strategy, lessons from experience        |

### Content collection schema (Astro)

```typescript
// src/content/config.ts
import { defineCollection, z } from 'astro:content';

const blog = defineCollection({
  type: 'content',
  schema: z.object({
    title: z.string(),
    description: z.string(),               // For meta + listing cards
    publishDate: z.coerce.date(),
    updatedDate: z.coerce.date().optional(),
    topics: z.array(z.enum([
      'ethics',
      'internet',
      'parenting',
      'ai-orchestration',
      'leadership',
    ])).min(1),                              // At least one topic required
    featured: z.boolean().default(false),    // Surface on homepage
    draft: z.boolean().default(false),
    heroImage: z.string().optional(),        // Optional hero/og image
  }),
});

const photography = defineCollection({
  type: 'data',
  schema: z.object({
    title: z.string(),
    src: z.string(),
    alt: z.string(),
    caption: z.string().optional(),
    date: z.coerce.date().optional(),
    featured: z.boolean().default(false),   // Surface on homepage
    order: z.number().optional(),           // Manual sort override
  }),
});

export const collections = { blog, photography };
```

### File structure

```
src/content/
├── blog/
│   ├── my-first-post.mdx
│   ├── on-raising-kids-online.mdx
│   └── ...
└── photography/
    └── gallery.json          ← or individual .json files per image
```

Posts are tagged in frontmatter. A post can have multiple topics:

```yaml
---
title: "Teaching My Kids About AI"
description: "What happens when your 8-year-old asks if the computer is alive."
publishDate: 2025-01-15
topics: ["parenting", "ai-orchestration"]
featured: true
---
```

---

## 3. URL Structure

| Page                 | URL pattern               | Example                                  |
|----------------------|---------------------------|------------------------------------------|
| Homepage             | `/`                       | `sam.blog`                               |
| Blog index           | `/blog/`                  | `sam.blog/blog/`                         |
| Blog post            | `/blog/[slug]/`           | `sam.blog/blog/teaching-kids-about-ai/`  |
| Topic filter         | `/blog/topic/[topic]/`    | `sam.blog/blog/topic/ethics/`            |
| Photography          | `/photography/`           | `sam.blog/photography/`                  |
| About                | `/about/`                 | `sam.blog/about/`                        |
| Contact              | `/contact/`               | `sam.blog/contact/`                      |

### Conventions

- **Trailing slashes: yes.** Consistent, avoids redirect ambiguity on Cloudflare Pages.
- **All lowercase, hyphen-separated.** No underscores, no camelCase.
- **Post slugs derived from filename.** `src/content/blog/teaching-kids-about-ai.mdx` → `/blog/teaching-kids-about-ai/`
- **No date segments in URLs.** `/blog/2025/01/slug/` is unnecessary noise. Flat slugs are cleaner and more shareable. Dates live in frontmatter.
- **Topic pages are filter views, not separate sections.** `/blog/topic/ethics/` renders the same blog listing component, filtered. This keeps one source of truth for the post list.

### Astro file routing

```
src/pages/
├── index.astro                        ← Homepage
├── blog/
│   ├── index.astro                    ← Blog listing
│   ├── [slug].astro                   ← Dynamic post pages (from content collection)
│   └── topic/
│       └── [topic].astro              ← Dynamic topic filter pages
├── photography/
│   └── index.astro                    ← Gallery page
├── about/
│   └── index.astro                    ← About page
└── contact/
    └── index.astro                    ← Contact page
```

---

## 4. Navigation

### Primary Navigation (Desktop)

```
[Sam.blog logo/wordmark]    Blog    Photography    About    Contact
```

Five items max. Clean, scannable. The logo/wordmark links home.

### Primary Navigation (Mobile)

Hamburger menu (or slide-out drawer) containing the same five links:

```
Home
Blog
Photography
About
Contact
```

"Home" is explicit on mobile since there's no persistent logo to tap.

### Footer

```
──────────────────────────────────────────────
  © 2025 Sam [Last Name]

  Blog · Photography · About · Contact

  [RSS icon] RSS    [GitHub icon] GitHub    [LinkedIn icon] LinkedIn    [Twitter/X icon] X
──────────────────────────────────────────────
```

Footer includes:
- Copyright
- Same primary nav links (accessibility/usability — readers who scroll to the bottom shouldn't have to scroll back up)
- Social links + RSS feed link
- Kept minimal. No sitemap dump, no "built with" badges.

### Cross-linking Strategy

| From              | Links to                                                              |
|-------------------|-----------------------------------------------------------------------|
| Homepage          | Featured blog posts (2–3), photography highlights (3–4 images), about teaser, contact CTA |
| Blog post         | Related posts (same topic, shown at bottom), photography callout (if relevant), about page (author bio snippet) |
| Blog listing      | Topic filter pills at top, individual posts                           |
| Photography       | Link to about page for context on Sam's photography                   |
| About             | Links to blog (recent writing), photography, contact CTA              |
| Contact           | Back-links to about (for context on who they're contacting)           |

**Principle:** Every page should have a natural next step. Nobody should hit a dead end.

---

## 5. Homepage — Sections & Order

The homepage answers three questions for its three audiences:

| Audience               | Question                          |
|------------------------|-----------------------------------|
| Hiring manager         | "Who is this person? Are they credible?" |
| Conference organizer   | "Does this person have interesting ideas? Can they communicate?" |
| Consulting prospect    | "Does this person understand my problem space?" |

### Section order (top to bottom):

#### 1. Hero / Introduction
- **Sam's name** (large, confident)
- **One-liner** — who he is in one sentence. Something like: *"I write about ethics, technology, and the internet. I build AI systems. I take photographs. I'm raising kids."*
- **No stock photo. No abstract gradient.** Either a portrait of Sam or clean typography on a considered background color.
- **Primary CTA:** "Read the blog" (this is the heart of the site)
- **Secondary CTA:** "Get in touch" (for the hiring/speaking/consulting crowd)

#### 2. Featured Writing (2–3 posts)
- Cards showing title, description, topic tag(s), and date.
- Hand-curated via `featured: true` in frontmatter. Not just "latest 3" — Sam should control which posts represent him.
- This section does the most work for conference organizers and hiring managers. It proves Sam can think and write.

#### 3. Topic Overview
- A compact section showing the 5 topic areas with brief (one-line) descriptions.
- Each links to the filtered blog view (`/blog/topic/[topic]/`).
- Communicates range and intentionality — "this person thinks across domains."

#### 4. Photography Highlight (3–4 images)
- A small, curated grid or horizontal scroll of featured photos.
- Links to `/photography/` for the full gallery.
- Purpose: signals that Sam is a real, dimensional person — not just a LinkedIn profile with a keyboard.

#### 5. About Teaser
- 2–3 sentences about Sam. Not the full bio — a taste.
- "Read more →" links to `/about/`.
- Helps hiring managers and conference organizers quickly confirm: "Okay, this person has substance."

#### 6. Contact CTA (bottom of page)
- Clear prompt: *"Looking for a speaker, consultant, or collaborator? Let's talk."*
- Links to `/contact/`.
- This is the conversion point. After scrolling the whole homepage, a visitor who's interested needs a clear next step.

---

## 6. Technical Notes (Astro + Cloudflare)

### Content Collections
- Blog posts: Markdown/MDX in `src/content/blog/`
- Photography data: JSON in `src/content/photography/`
- Actual photo files: `public/images/photography/` (or optimized via `astro:assets`)

### Static Output
- Astro configured for `output: 'static'` — full SSG, no server functions needed.
- All topic filter pages (`/blog/topic/[topic]/`) generated at build time via `getStaticPaths()`.
- Blog post pages generated at build time from the content collection.

### RSS Feed
- Generate at `/rss.xml` using `@astrojs/rss`.
- Include full post content in the feed (not just excerpts). Readers who use RSS will appreciate this.

### SEO & Meta
- Each page gets: `<title>`, `<meta name="description">`, Open Graph tags, Twitter card tags.
- Blog posts additionally get: `article:published_time`, `article:tag` (topics).
- Homepage `<title>`: "Sam [Last Name] — Ethics, Technology, Internet, Photography"
- Blog post `<title>`: "[Post Title] — Sam.blog"

### Performance
- No client-side JavaScript unless strictly necessary (light gallery interactions, mobile nav toggle).
- Static HTML + CSS does the heavy lifting. This is a content site, not an app.
- Images optimized at build time via Astro's image integration.

### 404 Page
- Custom `/404.astro` — friendly message, link back to homepage and blog. Not an afterthought.

---

## 7. What This Architecture Does NOT Include (and Why)

| Omitted                  | Reason                                                              |
|--------------------------|---------------------------------------------------------------------|
| Search                   | Not needed at launch. Dozens of posts, not thousands. Topic filters + good writing handle discovery. Add later if post count demands it. |
| Comments                 | Complexity and moderation burden for minimal value. Sam can engage on social platforms. Revisit if there's demand. |
| Newsletter signup        | Not in scope for architecture. Can be added as a component later (Buttondown, ConvertKit, etc.) without changing site structure. |
| Portfolio / Work page    | The blog *is* the portfolio. Sam's writing demonstrates his thinking. A separate "work" page would duplicate signal. |
| Tags page (`/tags/`)     | Topics serve this purpose. A separate tags index page isn't needed with only 5 topics. |
| Pagination               | At launch, blog listing can be a single scrollable page. Add pagination when post count warrants it (50+). |

---

## Summary

This is a five-page personal site with a blog at its center. The architecture is deliberately simple — five pages, one content type (blog posts) with flat topic tags, one photography collection, clean URLs, and a homepage that tells a clear story.

The simplicity is the point. Every architectural decision should make it easier for Sam to publish and easier for visitors to understand who Sam is.

**Next steps:** Design (OC-2) and development (OC-3) build against this document.