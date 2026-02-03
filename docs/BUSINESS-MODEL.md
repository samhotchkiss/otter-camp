# Business Model & Unit Economics

**Purpose:** Financial model for AI Hub as a sustainable business.

---

## Pricing Philosophy

### Why Not Per-Seat?

Traditional SaaS charges per-seat because each user:
- Consumes resources linearly
- Represents a marginal cost
- Has a clear willingness to pay

AI agents break this model:
- They're not employees
- They don't have budgets
- Adding agents doesn't 10x infrastructure costs
- An operator might run 1 agent or 100 — value is similar

### Per-Installation Pricing

We charge per **installation** (human operator), not per agent.

**Rationale:**
- The human is the customer
- Value scales with human's productivity, not agent count
- Simpler to understand
- Encourages agent adoption (no penalty for more agents)

---

## Pricing Tiers

### Free Tier

**Price:** $0/month

**Limits:**
- 1 installation (1 human)
- 5 agents
- 5 projects
- 500 tasks/month
- 1 GB storage
- Community support
- 7-day activity retention

**Goal:** Let solo operators try before buying. Convert on growth.

### Pro Tier

**Price:** $25/month (annual: $240/year, save 20%)

**Limits:**
- 1 installation
- Unlimited agents
- Unlimited projects
- Unlimited tasks
- 50 GB storage
- Email support
- 90-day activity retention
- Custom domain

**Goal:** Core revenue from serious solo operators.

### Team Tier

**Price:** $15/user/month (min 2 users)

**Limits:**
- Multiple users per installation
- Everything in Pro
- 200 GB storage
- Role-based access
- SSO (Google, GitHub)
- Priority support
- 1-year activity retention

**Goal:** Expand into small teams, agencies.

### Enterprise Tier

**Price:** Custom (starting ~$500/month)

**Limits:**
- Unlimited users
- Self-hosted option
- SLA (99.9% uptime)
- Dedicated support
- Custom integrations
- Compliance (SOC 2, HIPAA)
- Unlimited retention

**Goal:** Large organizations, compliance requirements.

---

## Revenue Projections

### Assumptions

| Metric | Year 1 | Year 2 | Year 3 |
|--------|--------|--------|--------|
| Free signups | 5,000 | 20,000 | 50,000 |
| Free → Pro conversion | 5% | 6% | 7% |
| Pro subscribers | 250 | 1,200 | 3,500 |
| Team accounts | 10 | 100 | 500 |
| Avg team size | 3 | 4 | 5 |
| Enterprise accounts | 0 | 5 | 20 |

### Revenue Calculation

**Year 1:**
- Pro: 250 × $25 × 12 = $75,000
- Team: 10 × 3 × $15 × 12 = $5,400
- Enterprise: $0
- **Total: $80,400**

**Year 2:**
- Pro: 1,200 × $25 × 12 = $360,000
- Team: 100 × 4 × $15 × 12 = $72,000
- Enterprise: 5 × $500 × 12 = $30,000
- **Total: $462,000**

**Year 3:**
- Pro: 3,500 × $25 × 12 = $1,050,000
- Team: 500 × 5 × $15 × 12 = $450,000
- Enterprise: 20 × $750 × 12 = $180,000
- **Total: $1,680,000**

---

## Cost Structure

### Infrastructure

| Component | Free | Pro | Notes |
|-----------|------|-----|-------|
| Compute (per user/month) | $0.50 | $1.00 | Shared infrastructure |
| Database | $0.10 | $0.30 | PostgreSQL hosted |
| Git storage | $0.05/GB | $0.05/GB | Object storage |
| Bandwidth | $0.02 | $0.10 | Outbound webhooks |
| **Total/user/month** | ~$0.70 | ~$1.50 | |

### Gross Margin

**Pro tier ($25/month):**
- Revenue: $25
- Cost: ~$2 (compute, storage, support allocation)
- **Gross margin: ~92%**

**Team tier ($15/user/month):**
- Revenue: $15
- Cost: ~$2
- **Gross margin: ~87%**

### Operating Costs (Year 1)

| Category | Monthly | Annual |
|----------|---------|--------|
| Infrastructure | $1,000 | $12,000 |
| Engineering (2 FTE) | $20,000 | $240,000 |
| Support (0.5 FTE) | $5,000 | $60,000 |
| Tools & services | $500 | $6,000 |
| Marketing | $1,000 | $12,000 |
| **Total** | $27,500 | $330,000 |

### Break-Even Analysis

**Monthly burn:** ~$27,500  
**Revenue needed:** ~$27,500  
**Pro subscribers needed:** ~1,100  

With Year 2 projections (1,200 Pro + Team + Enterprise), we hit profitability.

---

## Unit Economics

### Customer Acquisition Cost (CAC)

**Year 1 (Community-driven):**
- Marketing spend: $12,000
- Pro conversions: 250
- **CAC: $48**

**Year 2 (Paid channels):**
- Marketing spend: $100,000
- Pro conversions: 1,200
- **CAC: $83**

### Lifetime Value (LTV)

**Pro customer:**
- Monthly revenue: $25
- Gross margin: 92%
- Churn rate: 5%/month (assumed)
- **LTV: $25 × 0.92 × (1/0.05) = $460**

**LTV:CAC Ratio:**
- Year 1: $460 / $48 = **9.6x** (excellent)
- Year 2: $460 / $83 = **5.5x** (healthy)

Target: >3x is good, >5x is excellent.

### Payback Period

**Pro customer:**
- CAC: $83
- Monthly contribution: $25 × 0.92 = $23
- **Payback: 3.6 months**

Target: <12 months is healthy.

---

## Monetization Opportunities

### Add-Ons (Future)

| Feature | Price | Target |
|---------|-------|--------|
| Extra storage (per 50GB) | $5/month | Heavy users |
| Priority webhooks | $10/month | Critical workloads |
| Extended retention | $10/month | Compliance |
| Custom integrations | One-time | Enterprise |

### Marketplace (Future)

If we build a template/plugin ecosystem:
- 15-30% cut on marketplace transactions
- Featured placement fees
- Certification programs

### Professional Services

- Integration consulting: $200/hour
- Custom development: Project-based
- Training: $500/session

---

## Competitive Pricing Comparison

| Product | Individual | Team | Enterprise |
|---------|------------|------|------------|
| **AI Hub** | $25/mo | $15/user/mo | Custom |
| GitHub Team | $4/user/mo | $4/user/mo | $21/user/mo |
| Linear | $8/user/mo | $8/user/mo | Custom |
| Notion | $10/user/mo | $10/user/mo | Custom |
| Retool | — | $10/user/mo | $50/user/mo |

**Positioning:** Premium to GitHub, competitive with Linear, cheaper than custom tooling.

---

## Risk Factors

### Risk: Low Conversion

**Mitigation:**
- Generous free tier to get users invested
- In-app upgrade prompts at limit boundaries
- Case studies showing ROI

### Risk: High Churn

**Mitigation:**
- Strong onboarding to show value fast
- Integrations that create lock-in
- Regular feature releases

### Risk: Price Pressure

**Mitigation:**
- Differentiate on agent-native features (can't commoditize)
- Build switching costs (data, integrations)
- Consider usage-based element for high-volume users

---

## Key Metrics to Track

### Revenue Metrics

- MRR (Monthly Recurring Revenue)
- ARR (Annual Recurring Revenue)
- Revenue per customer
- Expansion revenue (upgrades)

### Growth Metrics

- New signups (free)
- Free → Pro conversion rate
- Pro → Team conversion rate
- Referral rate

### Retention Metrics

- Monthly churn rate (target: <5%)
- Net revenue retention (target: >100%)
- DAU/MAU ratio (engagement)

### Efficiency Metrics

- CAC by channel
- LTV:CAC ratio
- Payback period
- Gross margin

---

## Summary

| Metric | Year 1 | Year 2 | Year 3 |
|--------|--------|--------|--------|
| ARR | $80K | $462K | $1.68M |
| Customers (paid) | ~260 | ~1,350 | ~4,050 |
| Gross Margin | 92% | 90% | 88% |
| LTV:CAC | 9.6x | 5.5x | 4.0x |
| Team Size | 3 | 6 | 12 |

**Verdict:** Sustainable SaaS business with strong unit economics. Path to $1M+ ARR by Year 3 with modest assumptions.

---

*End of Business Model*
