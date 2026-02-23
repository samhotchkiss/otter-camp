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
| 013 | 013-agent-schema.md | Agent Schema | L2 | M | 003, 012 | written |
| 014 | 014-agent-service.md | Agent Service | L2 | M | 013, 006, 024 | written |
| 015 | 015-agent-api.md | Agent API Endpoints | L2 | S | 014, 007 | written |
| 016 | 016-project-schema.md | Project Schema | L2 | S | 003, 005, 012 | written |
| 017 | 017-flow-node-schema.md | Flow Node Schema | L2 | S | 016, 011 | written |
| 018 | 018-project-service.md | Project Service | L2 | S | 016, 017, 013 | written |
| 019 | 019-project-api.md | Project API Endpoints | L2 | S | 018, 007 | written |
| 020 | 020-mcp-schema-service.md | MCP Connection Schema and Service | L2 | M | 003, 009, 016, 013 | written |
| 021 | 021-mcp-circuit-breaker.md | MCP Circuit Breaker and Health | L2 | M | 020, 024 | written |
| 022 | 022-mcp-api.md | MCP API Endpoints | L2 | S | 021, 007 | written |
| 023 | 023-token-budget.md | Token Budget Schema and Service | L2 | S | 003, 016, 024 | written |
| 024 | 024-event-bus-job-queue.md | Event Bus and Job Queue | L2 | M | 002, 003 | written |
| 025 | 025-agent-project-assignment.md | Agent Project Assignment and Skill Attachment | L3 | S | 013, 011, 016 | written |
| 026 | 026-agent-assignment-api.md | Agent API Endpoints — Assignments and Skills | L3 | S | 025, 015, 007 | written |
| 027 | 027-project-task-schema.md | Project Task Schema | L3 | M | 016, 017, 005, 025 | written |
| 028 | 028-project-task-service.md | Project Task Service | L3 | M | 027, 018, 024, 025 | written |
| 029 | 029-flow-execution-schema.md | Flow Execution Schema | L3 | S | 027, 017 | written |
| 030 | 030-flow-execution-service.md | Flow Execution Service | L3 | M | 029, 028, 024 | written |
| 031 | 031-delivery-schema-service.md | Delivery Schema and Service | L3 | M | 027, 016, 024 | written |
| 032 | 032-task-project-api.md | Task and Project API Endpoints | L3 | M | 028, 030, 031, 019, 007 | written |
| 033 | 033-capability-policy.md | Capability Policy Schema and Service | L3 | M | 003, 016, 013, 023 | written |
| 034 | 034-capability-policy-api.md | Capability Policy API Endpoints | L3 | S | 033, 007 | written |
