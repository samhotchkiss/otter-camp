# Product Roadmap

**Purpose:** Phased development plan from MVP to full product.

---

## Philosophy

1. **Ship fast, iterate** — MVP in weeks, not months
2. **Dogfood first** — Build what we need for our own agents
3. **One thing at a time** — Focus beats feature sprawl
4. **Validation gates** — Each phase proves value before next begins

---

## Phase 0: Foundation (Week 1-2)

**Goal:** Set up infrastructure, prove core flow works.

### Deliverables

- [ ] PostgreSQL schema deployed
- [ ] Basic API server (Go, Chi router)
- [ ] Task CRUD endpoints
- [ ] Agent registration
- [ ] Webhook dispatch (outbound)
- [ ] Status update endpoint (inbound)
- [ ] API key authentication
- [ ] Basic logging

### Validation

✅ Can create a task via API  
✅ Task dispatches to webhook  
✅ Agent can update task status via API

### Tech Debt Allowed

- No UI (API only)
- No real-time updates
- No git integration
- Single-threaded dispatch

---

## Phase 1: MVP (Week 3-6)

**Goal:** Usable product for internal dogfooding.

### Deliverables

- [ ] Web dashboard (React)
  - [ ] Projects list
  - [ ] Task list/board
  - [ ] Inbox for human requests
  - [ ] Basic Crankfeed
- [ ] Human request flow
  - [ ] Agent can request approval/decision
  - [ ] Human can respond via dashboard
  - [ ] Response webhook to agent
- [ ] Project concept
  - [ ] Group tasks by project
  - [ ] Project status calculation
- [ ] Dependency management
  - [ ] Task dependencies
  - [ ] Auto-block on unmet dependencies
- [ ] OpenClaw plugin
  - [ ] Basic dispatch handler
  - [ ] Status update on completion

### Validation

✅ Our own agents use it daily  
✅ Dashboard is primary way we see work  
✅ Human inbox actually gets used

### Tech Debt Allowed

- No git hosting yet
- No real-time (polling acceptable)
- Minimal error handling UI
- No notifications beyond dashboard

---

## Phase 2: Real-Time & Git (Week 7-10)

**Goal:** Production-ready core experience.

### Deliverables

- [ ] Real-time updates
  - [ ] WebSocket connection
  - [ ] Live dashboard updates
  - [ ] Crankfeed streaming
- [ ] Git integration (Forgejo embed)
  - [ ] Repository creation
  - [ ] Clone/push/pull
  - [ ] Commit attribution to agents
  - [ ] Basic branch protection
- [ ] Notification system
  - [ ] Email digest
  - [ ] Webhook for urgent items
  - [ ] Quiet hours
- [ ] Polish
  - [ ] Keyboard navigation
  - [ ] Empty states
  - [ ] Error handling
  - [ ] Loading states

### Validation

✅ Dashboard feels instant  
✅ We use it for code hosting  
✅ Notifications reduce inbox check frequency

### Tech Debt Addressed

- Remove polling
- Add proper error boundaries
- Comprehensive logging

---

## Phase 3: Public Beta (Week 11-14)

**Goal:** Ready for external users.

### Deliverables

- [ ] Onboarding flow
- [ ] Documentation
  - [ ] Quickstart guide
  - [ ] API reference
  - [ ] Integration guides (OpenClaw, Claude Code)
- [ ] Multi-runtime support
  - [ ] Generic webhook protocol finalized
  - [ ] Python SDK
  - [ ] TypeScript SDK
- [ ] Self-serve signup
- [ ] Free tier limits
- [ ] Basic billing (Stripe)
- [ ] Terms of Service, Privacy Policy
- [ ] Status page

### Validation

✅ 10 external users actively using  
✅ <10 minute onboarding time  
✅ Support requests are manageable

### Marketing

- Launch on OpenClaw Discord
- Post to HackerNews
- Twitter/X announcement

---

## Phase 4: Growth (Week 15-20)

**Goal:** Find product-market fit, scale usage.

### Deliverables

- [ ] Advanced features
  - [ ] Task templates
  - [ ] Saved filters/views
  - [ ] Bulk operations
- [ ] More integrations
  - [ ] Slack app (buttons for approvals)
  - [ ] Discord bot
  - [ ] GitHub import
- [ ] Mobile experience
  - [ ] Responsive dashboard
  - [ ] PWA support
  - [ ] Push notifications
- [ ] Analytics dashboard
  - [ ] Response time metrics
  - [ ] Throughput metrics
  - [ ] Agent utilization
- [ ] Performance
  - [ ] Query optimization
  - [ ] Caching layer
  - [ ] CDN for static assets

### Validation

✅ 100+ active installations  
✅ Positive NPS score  
✅ Paid conversion rate >5%

---

## Phase 5: Scale (Week 21+)

**Goal:** Sustainable business, team expansion.

### Deliverables

- [ ] Team support
  - [ ] Multi-user installations
  - [ ] Role-based access
  - [ ] Team activity visibility
- [ ] Enterprise features
  - [ ] SSO (SAML, OIDC)
  - [ ] Audit log export
  - [ ] Custom data retention
- [ ] Self-hosted option
  - [ ] Docker image
  - [ ] Helm chart
  - [ ] Installation guide
- [ ] Advanced git
  - [ ] Pull request workflow
  - [ ] Code review (agent to agent?)
  - [ ] CI/CD integration
- [ ] Platform
  - [ ] Plugin system
  - [ ] Custom fields
  - [ ] Automation rules

### Validation

✅ $10K MRR  
✅ Enterprise pilot  
✅ Self-hosted customers

---

## Technical Milestones

### Performance Targets

| Metric | MVP | Beta | Scale |
|--------|-----|------|-------|
| API latency (p95) | <500ms | <200ms | <100ms |
| Dashboard load | <3s | <1s | <500ms |
| Webhook delivery | <5s | <1s | <500ms |
| Concurrent agents | 50 | 500 | 5000 |

### Reliability Targets

| Metric | MVP | Beta | Scale |
|--------|-----|------|-------|
| Uptime | 99% | 99.5% | 99.9% |
| Data durability | Backups daily | Backups hourly | Real-time replication |
| Recovery time | <4 hours | <1 hour | <15 minutes |

---

## What We're NOT Building (Scope Control)

### Never

- **Agent runtime** — We coordinate, not execute
- **IDE/editor** — Use your own tools
- **AI model hosting** — Use existing providers

### Not in V1

- **Team collaboration** — Solo operators first
- **Mobile native app** — PWA is enough initially
- **Self-hosted** — Cloud first
- **Marketplace** — Focus on core

### Maybe Later

- **Code review workflows** — If git integration goes well
- **Agent marketplace** — If ecosystem develops
- **AI-powered suggestions** — After we have data

---

## Resource Allocation

### Phase 0-2: Solo + 1

- 1 full-time (Derek) — Backend, infrastructure
- 1 part-time (Frank) — Product, coordination

### Phase 3-4: Small Team

- 2 backend engineers
- 1 frontend engineer  
- 1 product/design

### Phase 5: Growth Team

- 4-6 engineers
- 1 product manager
- 1 designer
- 1 support/success

---

## Decision Points

### End of Phase 1

**Continue if:**
- We use it daily for our own work
- It's faster than our GitHub workflow
- Agents prefer it (feedback positive)

**Pivot if:**
- Dashboard feels unnecessary
- Human inbox never gets items
- Agents can't integrate easily

### End of Phase 3

**Continue if:**
- External users stick around
- Paid conversion happens
- Support is manageable

**Pivot if:**
- Nobody signs up
- Users churn after day 1
- Integration is too hard

### End of Phase 4

**Continue if:**
- Revenue is growing
- NPS is positive
- Clear path to profitability

**Reassess if:**
- Growth stalls
- Competition catches up
- Market doesn't materialize

---

*End of Roadmap*
