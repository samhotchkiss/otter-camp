# 15. Migration and Backward Compatibility

## Objective

Define the cutover policy for a clean V2 launch.

## Clean-Room Constraint

- V2 does not reuse V1 runtime code.
- V2 does not reuse V1 database schema.
- V2 does not migrate V1 data.
- Any temporary compatibility shim must not constrain V2 architecture.

## Data and Schema Policy

- Data migration: none.
- Schema migration: none.
- V2 starts with a fresh database and fresh seed/bootstrap flow.

## V1 Handling

- V1 systems can be archived for reference only.
- V1 exports may be retained offline for audit/history needs.
- No runtime read-through into V1 data stores.

## API Transition Strategy

- Publish a V2 API contract as the canonical interface.
- If migration shims are needed, keep them isolated and time-boxed.
- Do not preserve V1 API behavior when it conflicts with V2 architecture.

## Migration Phases

1. Freeze V2 contracts and bootstrap flow.
2. Stand up fresh V2 environment and initialize empty schema.
3. Validate V2 end-to-end in staging with synthetic/test fixtures.
4. Launch V2 as a fresh-start environment.
5. Keep V1 archived separately for reference.

## Validation Checklist

- Fresh install path is reproducible.
- Permission boundaries are enforced from first boot.
- Core chat/project/task workflows run correctly on clean data.
- Observability and audit logs are functional at launch.

## Open Questions

- Do we offer optional CSV/JSON import tools later, or keep V2 strictly greenfield?
- How long do we keep V1 archives available internally?
- What minimum bootstrap dataset (if any) should ship with V2?
