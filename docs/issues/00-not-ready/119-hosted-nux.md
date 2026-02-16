# Issue #119 — Hosted New User Experience (NUX)

> ⚠️ **NOT READY** — Future work for Tier 2-3 hosted deployments

## Summary

The onboarding flow for users signing up at otter.camp (hosted), as opposed to self-hosted local installs.

## Deferred Items

### Org Subdomains
- New orgs get a subdomain: `acme.otter.camp`
- Requires: wildcard DNS, wildcard TLS certs, routing layer
- For now: orgs just have a display name + slug, no subdomain

### Signup Flow (Hosted Path)
- `otter init` → selects "Hosted" → generates link to `otter.camp/setup`
- Web-based onboarding: name, email, org name, magic link verification
- Auto-provision org, admin user, auth token
- CLI connects to hosted instance after setup

### Additional Hosted Concerns
- Magic link email delivery (need email provider)
- Invite links for adding team members
- Billing / subscription management
- Multi-tenant isolation
- Custom domain support (bring your own domain)

## Dependencies

- Local install flow working first (#239 P0/P1 fixes)
- Tier 2-3 architecture decisions (#000 product vision)
