# Spec 335: Multi-Tenant Table Partitioning & Row-Level Security

## Problem

All tenant data lives in shared, unpartitioned tables (`memories`, `chat_messages`, `conversations`, `rooms`, etc.). Tenant isolation is enforced purely by application-level `WHERE org_id = $1` filters. This creates two risks:

1. **Data leakage:** A single missed `org_id` filter in any query — especially vector similarity searches — returns results across all tenants. The surface area for this bug grows with every new query, endpoint, and worker.
2. **Scaling:** Without partitioning, vector indexes span all tenants. As tenant count and memory volume grow (currently ~30k memories/month from one user), vector search performance degrades for everyone. Index builds, vacuums, and maintenance hit all data at once.

## Current State

- `memories`, `chat_messages`, `conversations`, `rooms`, `room_participants`: all regular tables (`relkind = 'r'`), no partitioning
- Every query manually includes `WHERE org_id = $1`
- No Row Level Security (RLS) policies
- Vector indexes (IVFFlat on `embedding`, `embedding_1536`) span all rows across all orgs
- A vector similarity query missing the org_id filter returns nearest neighbors from ANY tenant

## Proposed Solution

### Phase 1: Row Level Security (safety net, low effort)

Add RLS policies to all tenant-scoped tables as a defense-in-depth layer:

```sql
ALTER TABLE memories ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON memories
  USING (org_id = current_setting('app.org_id')::uuid);
```

- API server sets `app.org_id` session variable on each request from the authenticated org
- Even if a query forgets `WHERE org_id`, RLS prevents cross-tenant reads
- Works with existing table structure — no migration needed beyond the policies

### Phase 2: Table Partitioning (performance + isolation)

Convert key tables to `PARTITION BY LIST (org_id)`:

- `memories`
- `chat_messages`
- `conversations`
- `rooms`
- `room_participants`

Each tenant gets a physical partition with its own vector indexes. Benefits:
- Postgres prunes partitions at query time — queries physically can't touch other tenants' data
- Vector indexes are per-partition (smaller, faster to build, better recall)
- Per-tenant backup/export becomes trivial (dump one partition)
- Index maintenance (REINDEX, VACUUM) can run per-partition without blocking other tenants

### Phase 3: Automated Partition Management

- Auto-create partition when a new org is provisioned
- Migration tooling to move existing data into partitions
- Monitoring for partition sizes and index health

## Tables to Partition

Priority order (highest data volume / security sensitivity first):

| Table | Reason |
|---|---|
| `memories` | Highest sensitivity — personal memory data, vector search |
| `chat_messages` | High volume, vector search, full conversation history |
| `conversations` | Linked to chat_messages |
| `rooms` | Tenant-scoped chat rooms |
| `room_participants` | Linked to rooms |
| `agent_memories` | Agent-specific memory data |
| `knowledge_entries` | Shared knowledge with embeddings |

## Acceptance Criteria

- [ ] RLS policies on all tenant-scoped tables enforce org_id isolation
- [ ] API server sets `app.org_id` session variable from authenticated context
- [ ] Key tables partitioned by org_id with per-partition vector indexes
- [ ] Existing queries continue to work (partition pruning is transparent)
- [ ] Vector similarity searches are physically scoped to the querying tenant's partition
- [ ] New org provisioning auto-creates partitions
- [ ] Migration path for existing data into partitioned tables
- [ ] No cross-tenant data returned in any query (verified by test)

## Test Plan

1. **RLS test:** Connect as app user without setting `app.org_id` — verify no rows returned
2. **RLS test:** Set `app.org_id` to org A, query memories — verify only org A data returned
3. **Cross-tenant vector test:** Insert similar vectors in two orgs, run similarity search scoped to org A — verify org B vectors never appear
4. **Partition pruning test:** EXPLAIN ANALYZE a query with org_id — verify partition prune in plan
5. **Performance test:** Compare vector search latency before/after partitioning with 100k+ rows

## Dependencies

- Need to audit every query in `internal/store/` and `internal/api/` for org_id scoping before RLS rollout
- Partitioning requires a migration that rewrites tables — needs maintenance window or online migration strategy

## Priority

High — this is a prerequisite for safe multi-tenant operation. Phase 1 (RLS) should ship before onboarding external users.
