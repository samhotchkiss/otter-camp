# 13. Security, Observability, and Cost Controls

## Security Baseline

- Org-level data isolation enforced in DB and API.
- Secret storage in encrypted-at-rest provider.
- Fine-grained authorization for sensitive tool and connector actions.
- Audit log for privileged operations.

## Privacy and Retention

- Configurable retention by org.
- Redaction controls for logs and model transcripts.
- Export/delete tooling for compliance workflows.

## Observability

- Structured logs with trace IDs.
- Metrics for latency, errors, queue depth, tool success, token usage.
- Tracing across API -> worker -> model/tool calls.
- Operator dashboards and alerting thresholds.

## Reliability Objectives

- SLOs for API availability and core task execution.
- Clear failure domains and circuit breakers.
- Incident playbooks for provider outages and queue backlog.

## Cost Controls

- Token and cost accounting per org/project/user/agent.
- Per-org budgets and hard limits.
- Model routing policies by cost tier.
- Usage forecasting and anomaly alerts.

## Open Questions

- Which compliance targets matter for first commercial launch?
- What default retention policy balances usability and privacy?
- Should cost limits fail closed or degrade to cheaper models?

