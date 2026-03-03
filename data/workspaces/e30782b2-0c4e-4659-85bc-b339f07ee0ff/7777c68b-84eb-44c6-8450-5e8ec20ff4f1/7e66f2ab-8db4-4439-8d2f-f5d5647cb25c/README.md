# sam.blog

Personal blog and photography site built with [Astro](https://astro.build) and deployed to [Cloudflare Pages](https://pages.cloudflare.com).

## Stack

- **Framework:** Astro (static output)
- **Content:** Markdown/MDX via Astro content collections
- **Hosting:** Cloudflare Pages
- **Domain:** sam.blog

## Development

```bash
npm install
npm run dev        # Start dev server
npm run build      # Build for production
npm run preview    # Preview production build
npm run check      # TypeScript type checking
```

## Project Structure

```
src/
├── components/    # Reusable Astro components
├── content/
│   ├── blog/          # Blog posts (markdown/MDX)
│   ├── photography/   # Photography entries (markdown)
│   └── config.ts      # Content collection schemas
├── layouts/       # Page layout templates
└── pages/         # File-based routing
public/            # Static assets (images, fonts, etc.)
```

## Content

### Blog Posts

Add markdown or MDX files to `src/content/blog/`:

```md
---
title: "Post Title"
description: "A short description"
date: 2025-01-01
tags: ["tag1", "tag2"]
draft: false
---

Post content here.
```

### Photography

Add entries to `src/content/photography/`:

```md
---
title: "Photo Title"
description: "Optional description"
date: 2025-01-01
image: "/images/photo.jpg"
imageAlt: "Description of the photo"
location: "Place, Country"
---

Optional body text.
```

## Deployment

Deployed automatically via Cloudflare Pages:
- **Production:** Push to `main` → auto-deploy to sam.blog
- **Preview:** Open a PR → preview deploy with unique URL