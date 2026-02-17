# Projects: Hosted Wildcard Routing Runbook

> Summary: Operator checklist for enabling and validating `*.otter.camp` routing in hosted mode without breaking `api.otter.camp` or the bare landing domain.
> Last updated: 2026-02-16
> Audience: Operators and reviewers shipping hosted onboarding.

## Goal

Enable `https://{slug}.otter.camp` for hosted web app tenants while preserving:
- `https://api.otter.camp` -> API service
- `https://otter.camp` -> landing/www service

## Preconditions

- Hosted signup path is merged and can create org slugs (Spec 310 dependency).
- Backend supports slug-scoped workspace resolution for hosted requests.
- Web client sends org slug context (`X-Otter-Org`) from hosted subdomains.
- You know the current Railway service target for:
  - web app
  - API
  - www/landing page

## DNS Records Checklist

Use your DNS provider (Cloudflare/Route53/etc.) and keep these records explicit:

1. `api.otter.camp` -> API service target (existing record; do not replace).
2. `otter.camp` (or `@`) -> www/landing target (existing record; do not replace).
3. `*.otter.camp` -> web app service target (new wildcard record for tenant UI).

Notes:
- Prefer CNAME to provider hostname when supported.
- If apex CNAME is unsupported, keep current apex setup unchanged and only add wildcard for `*`.
- Keep proxy mode consistent with your TLS strategy.

## TLS Setup Options

Choose one path and document which one was used in rollout notes.

### Option A: Railway-managed wildcard cert

1. Attach wildcard custom domain (`*.otter.camp`) to the web service.
2. Verify Railway certificate issuance completes for wildcard.
3. Ensure `api.otter.camp` remains attached only to API service.
4. Ensure bare `otter.camp` remains attached only to www service.

### Option B: Cloudflare-managed wildcard cert/proxy

1. Keep Railway service domains private/internal.
2. Point wildcard/public hostnames via Cloudflare proxy rules.
3. Provision wildcard edge cert for `*.otter.camp`.
4. Verify origin TLS mode and cert trust between Cloudflare and Railway.

## Rollout Sequence

1. Confirm current production health:
   - `curl -I https://api.otter.camp`
   - `curl -I https://otter.camp`
2. Add wildcard DNS record for `*.otter.camp` to web service target.
3. Provision/verify wildcard TLS certificate.
4. Wait for DNS + cert propagation.
5. Validate hosted tenant URL (`https://swh.otter.camp`) loads web app.
6. Confirm API and bare landing routes still resolve to the correct services.

## Validation Commands and Expected Results

Run from a shell with network access:

```bash
curl -I https://swh.otter.camp
curl -I https://api.otter.camp
curl -I https://otter.camp
```

Expected:
- `https://swh.otter.camp`: HTTPS succeeds with a valid cert and non-error app response (`200` or expected redirect).
- `https://api.otter.camp`: Existing API behavior unchanged (`200` on health/doc route or expected API status).
- `https://otter.camp`: Existing landing page behavior unchanged (`200` or expected redirect).

Optional cert verification:

```bash
openssl s_client -connect swh.otter.camp:443 -servername swh.otter.camp </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer
```

## Rollback

If wildcard rollout causes regressions:

1. Remove or disable the wildcard DNS record.
2. Detach wildcard domain/cert from provider config.
3. Re-validate `api.otter.camp` and `otter.camp` health endpoints.
4. Log incident details and config diffs before retrying.

## Evidence Checklist

- [ ] Screenshot or log of wildcard DNS record (`*.otter.camp`) configuration.
- [ ] Certificate status evidence for wildcard domain.
- [ ] `curl -I https://swh.otter.camp` output captured.
- [ ] `curl -I https://api.otter.camp` output captured.
- [ ] `curl -I https://otter.camp` output captured.
- [ ] Confirmation that hosted web requests include org context and return org-scoped API data.

## Change Log

- 2026-02-16: Added canonical wildcard DNS/TLS rollout and validation runbook for hosted subdomain routing (Spec 311).
