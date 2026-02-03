# Security Model

**Purpose:** Define how AI Hub protects user data, authenticates requests, and prevents abuse.

---

## Threat Model

### Assets to Protect

1. **Source code** — Repositories contain proprietary code
2. **Task content** — May contain sensitive business logic
3. **API keys** — Compromise enables impersonation
4. **Webhook secrets** — Enables forged events
5. **User credentials** — Account takeover risk

### Threat Actors

1. **External attackers** — Trying to access data or disrupt service
2. **Malicious agents** — Compromised agent runtime sending bad data
3. **Rogue operators** — Insider threat (less relevant for solo operators)
4. **Abusive users** — Spam, resource exhaustion

---

## Authentication

### API Key Authentication

Primary auth for API and agent communication.

**Key format:**
```
aihub_sk_[32 random alphanumeric characters]
aihub_sk_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6
```

**Key storage:**
- Hashed with bcrypt (cost factor 12) in database
- Full key shown once at creation, never again
- Keys can be rotated (old key revoked, new key issued)

**Key scopes (future):**
- `full` — All permissions (default)
- `read` — Read-only access
- `dispatch` — Can update task status, not create tasks
- `repo:name` — Limited to specific repository

**Request format:**
```
Authorization: Bearer aihub_sk_xxx
```

### Web Session Authentication

For dashboard users.

**Session tokens:**
- JWT with 1-hour expiry
- Refresh tokens with 7-day expiry
- Stored in httpOnly cookies

**Password requirements:**
- Minimum 12 characters
- No common passwords (check against HaveIBeenPwned list)
- Breached password detection

**MFA (optional, future):**
- TOTP (authenticator apps)
- WebAuthn/passkeys

### Agent Identification

Agents identify themselves via header:
```
X-AIHub-Agent: derek
```

This is **informational only** — it's not authentication. The API key authenticates; the agent header attributes actions.

**Validation:**
- Agent ID must exist in installation
- Agent ID must match expected pattern (lowercase, alphanumeric, hyphens)
- Unknown agent IDs are rejected

---

## Authorization

### Installation Isolation

All data is scoped to an installation. An API key can only access its own installation's data.

```sql
-- Every query includes installation_id
SELECT * FROM tasks WHERE installation_id = $1 AND id = $2
```

### No Per-Agent Permissions

Within an installation, all agents have equal access. They're all the same operator.

**Rationale:** Agents aren't employees. They're extensions of one person. Complex permission matrices add overhead without value for solo operators.

### Future: Team Permissions

For team plans:
- **Owner** — Full access, billing, delete installation
- **Admin** — Full access except billing/delete
- **Member** — Create/modify tasks, view all
- **Viewer** — Read-only access

---

## Webhook Security

### Outbound Webhooks (Hub → Agent)

**Signature:**
```
X-AIHub-Signature: sha256=abc123...
X-AIHub-Timestamp: 1709234567
```

**Computation:**
```python
import hmac
import hashlib

signature = hmac.new(
    key=webhook_secret.encode(),
    msg=f"{timestamp}.{body}".encode(),
    digestmod=hashlib.sha256
).hexdigest()
```

**Verification steps:**
1. Parse timestamp from header
2. Reject if timestamp is >5 minutes old (replay protection)
3. Compute expected signature
4. Compare signatures (constant-time)

**Webhook secret:**
- 32-byte random value, hex-encoded
- Unique per installation
- Can be rotated

### Inbound Webhooks (Agent → Hub)

API key authentication + agent ID header.

```
POST /api/v1/tasks/eng-042/status
Authorization: Bearer aihub_sk_xxx
X-AIHub-Agent: derek
Content-Type: application/json

{ "action": "complete", ... }
```

---

## Data Protection

### Encryption at Rest

- Database: PostgreSQL with TDE (transparent data encryption)
- Git objects: Encrypted object storage (AES-256)
- Backups: Encrypted before upload to backup storage

### Encryption in Transit

- All connections require TLS 1.2+
- HSTS enabled (1 year, includeSubdomains)
- Certificate pinning for mobile apps (future)

### Data Retention

- Active data: Retained indefinitely while account is active
- Deleted installations: Soft delete, hard delete after 30 days
- Activity logs: 90 days, then summarized
- Audit logs: 1 year minimum

### GDPR Compliance

- Data export: User can export all data (JSON format)
- Data deletion: User can request full deletion
- Data portability: Standard formats for migration

---

## Rate Limiting

### Per-Installation Limits

| Resource | Free Tier | Pro Tier |
|----------|-----------|----------|
| API requests | 1,000/hour | 10,000/hour |
| Git operations | 500/hour | 5,000/hour |
| Webhooks sent | 100/hour | 1,000/hour |
| Webhooks received | 500/hour | 5,000/hour |
| Human requests | 50/hour | 500/hour |

### Rate Limit Headers

```
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 950
X-RateLimit-Reset: 1709238000
```

### Rate Limit Response

```
HTTP/1.1 429 Too Many Requests
Retry-After: 3600

{
  "error": {
    "code": "rate_limited",
    "message": "Rate limit exceeded. Retry after 3600 seconds.",
    "details": {
      "limit": 1000,
      "remaining": 0,
      "reset": 1709238000
    }
  }
}
```

### Burst Handling

- Token bucket algorithm
- Allows short bursts above limit
- Sustained traffic enforced to limit

---

## Abuse Prevention

### Account Creation

- Email verification required for sensitive actions
- CAPTCHA on signup (hCaptcha, privacy-friendly)
- Rate limit on account creation per IP

### API Abuse

- Rate limiting (above)
- Request size limits (1MB body max)
- Timeout limits (30s max request time)

### Git Abuse

- Repository size limits (1GB free, 10GB pro)
- Push size limits (100MB per push)
- Large file detection (warn on >50MB files)

### Webhook Abuse

- Payload size limit (1MB)
- Timeout limit (10s for delivery)
- Retry limit (3 attempts, then fail)
- Dead letter queue for investigation

---

## Audit Logging

### What's Logged

All state-changing operations:
- Authentication events (login, logout, key create/revoke)
- Task lifecycle (create, update, dispatch, complete)
- Agent changes (create, update, delete)
- Repository operations (create, push, branch protect)
- Human responses (approvals, decisions)
- Administrative actions (settings, billing)

### Log Format

```json
{
  "timestamp": "2026-02-03T11:30:00Z",
  "installation_id": "inst_xxx",
  "actor": {
    "type": "agent",
    "id": "derek",
    "ip": "192.168.1.100"
  },
  "action": "task.complete",
  "resource": {
    "type": "task",
    "id": "eng-042"
  },
  "details": {
    "previous_status": "in_progress",
    "new_status": "done"
  },
  "request_id": "req_abc123"
}
```

### Log Retention

- Real-time: 7 days (searchable)
- Archive: 1 year (compressed, retrievable)
- Compliance: As required by customer

### Log Access

- Owner can view audit logs in dashboard
- Export available (JSON, CSV)
- API access for integration

---

## Incident Response

### Severity Levels

| Level | Description | Response Time |
|-------|-------------|---------------|
| **P1** | Service down, data breach | <15 minutes |
| **P2** | Major feature broken | <1 hour |
| **P3** | Minor issue, workaround exists | <4 hours |
| **P4** | Cosmetic, low impact | Next business day |

### Response Procedures

**Data Breach:**
1. Contain (revoke compromised credentials)
2. Assess (scope of breach)
3. Notify (affected users within 72 hours)
4. Remediate (fix vulnerability)
5. Review (post-mortem, update procedures)

**Service Outage:**
1. Acknowledge (status page update)
2. Diagnose (identify root cause)
3. Mitigate (restore service)
4. Communicate (updates every 30 minutes)
5. Post-mortem (within 48 hours)

---

## Security Development

### Secure Development Practices

- Code review required for all changes
- Dependency scanning (Dependabot, Snyk)
- SAST (static analysis) in CI
- Secret scanning (prevent committed secrets)
- Signed commits required

### Penetration Testing

- Annual third-party pentest
- Bug bounty program (future)
- Regular internal security reviews

### Vulnerability Disclosure

- security@aihub.example.com
- 90-day disclosure timeline
- Credit for responsible disclosure

---

## Compliance (Future)

### SOC 2 Type II

- Target: Year 2
- Requires: Audit logging, access controls, monitoring

### GDPR

- Required from day 1 (EU users)
- Data export, deletion, portability

### HIPAA (Future)

- If healthcare customers need it
- Requires: BAA, additional controls

---

## Self-Hosted Security

For self-hosted deployments:

### Minimum Requirements

- TLS certificate (Let's Encrypt supported)
- Database encryption enabled
- Firewall rules (only expose necessary ports)
- Regular updates (security patches)

### Recommended

- Private network deployment
- VPN access only
- Separate database server
- Backup encryption

### Security Responsibility

- AI Hub: Secure software, timely patches
- Customer: Infrastructure, network, access control

---

*End of Security Model*
