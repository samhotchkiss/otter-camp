# Task Manifest

| # | File | Title | Layer | Size | Depends on | Status |
|---|------|-------|-------|------|------------|--------|
| 001 | 001-project-scaffold.md | Project Scaffold and Runtime Harness | L0 | S | — | written |
| 002 | 002-database-infrastructure.md | Database Infrastructure | L0 | S | 001 | written |
| 003 | 003-l0-schema.md | L0 Schema — Foundation Tables | L0 | S | 002 | written |
| 004 | 004-object-storage.md | Object Storage Abstraction | L0 | S | 001 | written |
| 005 | 005-auth-schema.md | Auth Schema — Users, Sessions, API Keys | L1 | S | 002, 003 | written |
| 006 | 006-auth-service.md | Auth Service | L1 | M | 005 | written |
| 007 | 007-auth-api.md | Auth API Endpoints | L1 | S | 006 | written |
| 008 | 008-audit-event.md | Audit Event Schema and Service | L1 | S | 005 | written |
| 009 | 009-secret-store.md | Secret Store | L1 | M | 003, 004 | written |
| 010 | 010-model-provider-registry.md | Model Provider Registry | L1 | M | 003 | written |
| 011 | 011-skill-registry.md | Skill Registry Schema and Service | L1 | S | 003 | written |
| 012 | 012-bootstrap.md | Bootstrap Sequence | L1 | M | 003, 005, 008, 009, 010, 011 | written |
