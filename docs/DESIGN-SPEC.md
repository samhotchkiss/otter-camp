# OtterCamp Design Specification

> **For Codex agents and developers:** This is the single source of truth for all visual patterns.
> Every frontend issue MUST reference this file. Match these patterns exactly.

## Design Philosophy

**Draplin/Field Notes aesthetic:** Clean, bold, utilitarian. No decoration for decoration's sake.
- Dark mode by default
- Warm earth tones (browns, golds, creams)
- 12px border-radius on cards
- Consistent spacing scale
- Clear visual hierarchy

---

## Theme Tokens

### Dark Theme (default)

```css
/* Backgrounds */
--bg: #1A1918;                /* Main background */
--surface: #252422;           /* Card backgrounds */
--surface-alt: #2D2B28;       /* Elevated/header surfaces */
--border: #3D3A36;            /* Borders and dividers */

/* Text */
--text: #FAF8F5;              /* Primary text */
--text-muted: #A69582;        /* Secondary/meta text */

/* Accent */
--accent: #C9A86C;            /* Primary accent (warm gold) */
--accent-hover: #D4B87A;      /* Accent hover state */

/* Status Colors */
--orange: #C87941;            /* Warnings, action items */
--green: #5A7A5C;             /* Success, working */
--red: #B85C38;               /* Error, blocked */
--blue: #4A6D7C;              /* Info, links */
```

### Light Theme

```css
--bg: #FAF8F5;
--surface: #FFFFFF;
--surface-alt: #F5F2ED;
--border: #E8E2D9;
--text: #2D2A26;
--text-muted: #8B7355;
--accent: #5C4A3D;
--accent-hover: #4A3C31;
```

---

## Typography

```css
/* Font Stack */
--font: 'Inter', -apple-system, sans-serif;

/* Sizes */
font-size: 18px;    /* Large titles */
font-size: 15px;    /* Card titles, section headers */
font-size: 14px;    /* Body text, buttons */
font-size: 13px;    /* Section labels */
font-size: 12px;    /* Meta text, timestamps */
font-size: 11px;    /* Badges, tiny labels */

/* Weights */
font-weight: 400;   /* Body */
font-weight: 500;   /* Emphasis */
font-weight: 600;   /* Strong, links */
font-weight: 700;   /* Headers, titles */
font-weight: 800;   /* Logo, hero text */

/* Line Height */
line-height: 1.5;   /* Default for all body text */
```

### Section Labels (uppercase)

```css
.section-title {
  font-size: 13px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 1px;
  color: var(--text-muted);
}
```

---

## Spacing Scale

Use these values consistently. Don't invent new numbers.

```
4px   — Tiny gaps (badge padding, small internal spacing)
8px   — Small gaps (between badge and text, icon gaps)
10px  — Input padding (vertical)
12px  — Standard small padding, gaps between similar items
14px  — List item padding
16px  — Standard padding (card header/body vertical)
20px  — Card padding (horizontal), large gaps
24px  — Section gaps, major padding (topbar, main content)
```

---

## Border Radius

```css
border-radius: 4px;     /* Small elements (badges, kbd) */
border-radius: 8px;     /* Inputs, buttons, search trigger */
border-radius: 10px;    /* Count badges, pills */
border-radius: 12px;    /* Cards (THE standard) */
border-radius: 16px;    /* Large cards, command palette */
border-radius: 50%;     /* Avatars, status dots */
```

**The golden rule:** Cards are always `12px`.

---

## Layout

### App Shell

```html
<div class="app">
  <div class="topbar">...</div>
  <main class="main">
    <div class="primary">...</div>
    <aside class="secondary">...</aside>
  </main>
  <footer class="footer">...</footer>
</div>
```

### Main Content Grid

```css
.main {
  flex: 1;
  display: grid;
  grid-template-columns: 1fr 360px;
  gap: 24px;
  padding: 24px;
  max-width: 1200px;
  margin: 0 auto;
  width: 100%;
}

/* Responsive: single column on mobile */
@media (max-width: 900px) {
  .main {
    grid-template-columns: 1fr;
  }
  .secondary { display: none; }
}
```

---

## Components

### Topbar

The topbar uses the accent color as background. This is distinctive.

```html
<div class="topbar">
  <div class="logo">
    <span class="logo-icon">🦦</span>
    <span>otter.camp</span>
  </div>
  <div class="search-trigger">
    <span>🔍</span>
    <span class="search-trigger-text">Search or type a command...</span>
    <kbd>⌘K</kbd>
  </div>
  <div class="topbar-right">
    <button class="theme-toggle">🌙</button>
    <img class="avatar" src="..." alt="User" />
  </div>
</div>
```

```css
.topbar {
  background: var(--accent);  /* Gold background! */
  color: var(--bg);           /* Dark text on gold */
  padding: 12px 24px;
  display: flex;
  align-items: center;
  gap: 24px;
  position: sticky;
  top: 0;
  z-index: 100;
}

.logo {
  display: flex;
  align-items: center;
  gap: 10px;
  font-weight: 700;
  font-size: 18px;
}

.search-trigger {
  flex: 1;
  max-width: 400px;
  background: rgba(255,255,255,0.15);
  border: 1px solid rgba(255,255,255,0.2);
  border-radius: 8px;
  padding: 10px 16px;
  display: flex;
  align-items: center;
  gap: 12px;
  cursor: pointer;
}

.search-trigger:hover {
  background: rgba(255,255,255,0.2);
}

.search-trigger kbd {
  background: rgba(255,255,255,0.2);
  padding: 2px 8px;
  border-radius: 4px;
  font-size: 12px;
}
```

### Cards

All cards follow this structure:

```html
<article class="card">
  <header class="card-header">
    <div class="card-title-row">
      <span class="card-icon">📋</span>
      <h3 class="card-title">Card Title</h3>
      <span class="badge">Status</span>
    </div>
    <p class="card-meta">Meta information</p>
  </header>
  <div class="card-body">
    <!-- Content -->
  </div>
  <footer class="card-footer">
    <!-- Actions -->
  </footer>
</article>
```

```css
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
}

.card-header {
  background: var(--surface-alt);
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}

.card-body {
  padding: 20px;
}

.card-footer {
  padding: 12px 20px;
  background: var(--surface-alt);
  border-top: 1px solid var(--border);
}
```

#### Action Card (high priority)

```css
.action-card {
  background: var(--surface);
  border: 2px solid var(--orange);  /* Orange border = needs action */
  border-radius: 12px;
  padding: 24px;
  margin-bottom: 12px;
  box-shadow: 0 2px 8px rgba(200, 121, 65, 0.1);
}

.action-card:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 24px rgba(200, 121, 65, 0.2);
}
```

### Buttons

```html
<button class="btn btn-primary">Approve</button>
<button class="btn btn-secondary">View Details</button>
```

```css
.btn {
  padding: 10px 20px;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  border: none;
  font-family: var(--font);
  transition: all 0.15s;
}

.btn-primary {
  background: var(--accent);
  color: var(--bg);
}

.btn-primary:hover {
  background: var(--accent-hover);
}

.btn-secondary {
  background: var(--surface-alt);
  color: var(--text);
  border: 1px solid var(--border);
}
```

### Badges

```html
<span class="badge">Default</span>
<span class="badge badge-success">Working</span>
<span class="badge badge-warning">Waiting</span>
<span class="badge badge-error">Blocked</span>
```

```css
.badge {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  padding: 2px 8px;
  border-radius: 4px;
  background: var(--surface-alt);
  color: var(--text-muted);
}

.badge-success { background: rgba(90, 122, 92, 0.15); color: var(--green); }
.badge-warning { background: rgba(200, 121, 65, 0.15); color: var(--orange); }
.badge-error { background: rgba(184, 92, 56, 0.15); color: var(--red); }
.badge-info { background: rgba(74, 109, 124, 0.15); color: var(--blue); }
```

### Feed Items / List Items

```html
<div class="feed-item">
  <div class="feed-avatar">SH</div>
  <div class="feed-content">
    <p class="feed-text"><strong>Agent Name</strong> did something interesting</p>
    <p class="feed-meta">2 minutes ago · Project Name</p>
  </div>
  <span class="feed-type">progress</span>
</div>
```

```css
.feed-item {
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
  display: flex;
  gap: 14px;
  cursor: pointer;
}

.feed-item:last-child { border-bottom: none; }
.feed-item:hover { background: var(--surface-alt); }

.feed-avatar {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--blue);
  color: white;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
  font-size: 14px;
  flex-shrink: 0;
}
```

### Status Dots

```html
<span class="status-dot status-working"></span>
<span class="status-dot status-blocked"></span>
<span class="status-dot status-idle"></span>
```

```css
.status-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
}

.status-working { background: var(--green); }
.status-blocked { background: var(--red); }
.status-idle { background: var(--text-muted); }
```

### Footer

```html
<footer class="footer">
  <p class="otter-fact">🦦 Otters hold hands while sleeping so they don't drift apart.</p>
  <p class="footer-tagline">otter.camp — Keep your projects afloat</p>
</footer>
```

```css
.footer {
  background: var(--surface-alt);
  border-top: 1px solid var(--border);
  padding: 16px 24px;
  text-align: center;
  font-size: 13px;
  color: var(--text-muted);
}
```

---

## Transitions

Standard transition for interactive elements:

```css
transition: all 0.15s;      /* Quick, snappy */
transition: all 0.2s;       /* Card hovers, larger movements */
transition: all 0.3s;       /* Page transitions, overlays */
```

---

## File Reference

All mockups are in `designs/dashboard-v5/`:

| File | Screen |
|------|--------|
| `index.html` | Main dashboard |
| `inbox.html` | Inbox / approvals list |
| `feed.html` | Activity feed |
| `project.html` | Single project view |
| `task.html` | Task detail |
| `agents.html` | Agent list |
| `chat.html` | Chat interface |
| `review-code.html` | Code diff review |
| `review-content.html` | Content review |
| `login.html` | Authentication |
| `settings.html` | Profile & preferences |
| `settings-github.html` | GitHub connection |
| `settings-openclaw.html` | OpenClaw settings |
| `workflows.html` | Ongoing workflows |

---

## For Codex Agents

When implementing a component:

1. **Find the mockup file** — Check the table above
2. **Copy the exact HTML structure** — Don't improvise
3. **Copy the CSS** — Use the tokens, don't hardcode colors
4. **Test in dark mode** — That's the default
5. **Match the spacing** — Use the spacing scale

If something isn't specified here, check the mockup HTML. The mockups are the authoritative source.
