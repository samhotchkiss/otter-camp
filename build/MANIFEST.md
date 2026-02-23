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
| 035 | 035-model-gateway-routing.md | Model Gateway — Routing, Fallback, and Concurrency | L3 | M | 010, 024, 033 | written |
| 036 | 036-model-gateway-streaming-tracking.md | Model Gateway — Streaming, Token Tracking, and Rollups | L3 | M | 035, 004, 003 | written |
| 037 | 037-model-api.md | Model API Endpoints | L3 | S | 036, 007 | written |
| 038 | 038-memory-schema.md | Memory Schema | L3 | M | 003, 005, 016, 013 | written |
| 039 | 039-memory-extraction.md | Memory Extraction Pipeline | L3 | M | 038, 035, 024 | written |
| 040 | 040-memory-retrieval.md | Memory Retrieval Pipeline | L3 | M | 038, 035, 013 | written |
| 041 | 041-memory-compaction-import.md | Memory Compaction and Import | L3 | M | 038, 039, 040, 004, 024 | written |
| 042 | 042-memory-api.md | Memory API Endpoints | L3 | S | 041, 040, 007 | written |
| 043 | 043-chat-schema.md | Chat Session Schema | L4 | M | 003, 005, 013, 016, 027, 038 | written |
| 044 | 044-chat-service.md | Chat Service | L4 | M | 043, 006, 014, 024, 033 | written |
| 045 | 045-chat-summarization-retention.md | Chat Progressive Summarization and Retention | L4 | M | 043, 044, 035, 036, 024 | written |
| 046 | 046-chat-api.md | Chat API Endpoints | L4 | S | 044, 045, 007, 067 | written |
| 047 | 047-sse-websocket.md | SSE Realtime and WebSocket | L4 | M | 024, 043, 044, 007 | written |
| 048 | 048-turn-engine.md | Turn Execution Engine | L4 | M | 043, 044, 045, 047, 049, 050, 035, 036, 033, 024 | written |
| 049 | 049-tool-resolution.md | Tool Resolution Pipeline | L4 | M | 043, 013, 017, 020, 033, 055 | written |
| 050 | 050-prompt-assembly.md | Prompt Assembly Engine | L4 | M | 043, 044, 045, 013, 011, 020, 038, 040, 049, 033 | written |
| 051 | 051-chat-cli.md | Chat CLI Commands | L4 | S | 046, 047, 068 | written |
| 052 | 052-control-plane-schema.md | Control Plane Schema — Run, Step, Attempt, Tool Execution, Artifact, Event | L4 | M | 003, 004, 013, 016, 027, 033, 043 | written |
| 053 | 053-control-plane-service.md | Control Plane Service — Run Lifecycle, State Machine, Supervisor | L4 | M | 052, 033, 023, 024, 027, 028 | written |
| 054 | 054-control-plane-api.md | Control Plane API Endpoints | L4 | S | 053, 052, 007, 047, 067 | written |
| 055 | 055-tool-execution-service.md | Tool Execution Service and Dispatch Pipeline | L4 | M | 052, 053, 033, 020, 021, 024 | written |
| 056 | 056-native-tools-tier1.md | Native Tools — Tier 1 (Read-Only) | L4 | M | 055, 003, 013, 027, 038, 040 | written |
| 057 | 057-native-tools-tier2.md | Native Tools — Tier 2 (Mutation) | L4 | M | 056, 055, 033, 024, 027, 028, 030 | written |
| 058 | 058-cli-execution.md | CLI Sandbox and Execution | L4 | M | 052, 053, 055, 056, 004, 027 | written |
| 059 | 059-browser-execution.md | Browser Tool Execution | L4 | M | 052, 053, 055, 004, 027, 043 | written |
| 060 | 060-tool-execution-audit-retry.md | Tool Execution Audit, Retry Logic, and Run Event Fan-Out | L4 | S | 052, 053, 055, 024, 047 | written |
| 061 | 061-flow-session-integration.md | Flow-Session Integration and Task Participant Cross-Domain Wiring | L4 | S | 029, 030, 043, 044, 052, 053 | written |
| 062 | 062-model-attribution.md | Model Attribution — Run FK Linkage, Usage Rollup, and Cost Pipeline | L4 | S | 035, 036, 052, 053, 043, 044 | written |
| 063 | 063-observability-security.md | Observability, Security Hardening, and Retention Enforcement | L4 | M | 001, 002, 003, 007, 024, 052, 036, 062 | written |
| 064 | 064-delivery-execution.md | Delivery Execution — Background Workers and Deploy State Machine | L4 | M | 031, 028, 030, 052, 053, 024, 061 | written |
| 065 | 065-scheduling-engine.md | Scheduling Engine — Cron Execution, Overlap Policy, and Schedule API | L4 | S | 016, 018, 028, 024, 019 | written |
| 066 | 066-push-notification-preferences.md | Push Notification Preferences — Schema, Delivery Consumer, and API | L4 | S | 005, 027, 043, 044, 007 | written |
| 067 | 067-api-middleware.md | API Middleware, Envelope Standardization, and Pagination | L4 | S | 001, 007, 024 | written |
| 068 | 068-cli-binary.md | CLI Binary — Build, Packaging, and Command Suite | L4 | M | 001, 002, 005, 009, 006, 007, 063 | written |
| 069 | 069-mobile-api.md | Mobile API — Dashboard Aggregation, Push Token Registration, and WebSocket Preference | L4 | S | 007, 043, 044, 047, 027, 028, 066, 067 | written |
| 070 | 070-web-static-serving.md | Web UI Static File Serving and SPA Infrastructure | L4 | S | 001, 063, 067 | written |
| 071 | 071-auth-integration-tests.md | Auth and Tenancy Integration Tests | L5 | S | 005, 006, 007, 008, 009, 012 | written |
| 072 | 072-agent-integration-tests.md | Agent Integration Tests | L5 | S | 013, 014, 015, 025, 026 | written |
| 073 | 073-project-task-flow-integration-tests.md | Project and Task Flow Integration Tests | L5 | M | 016, 017, 018, 019, 027, 028, 029, 030, 031, 032 | written |
| 074 | 074-memory-integration-tests.md | Memory Integration Tests | L5 | M | 038, 039, 040, 041, 042 | written |
| 075 | 075-model-gateway-integration-tests.md | Model Gateway Integration Tests | L5 | M | 010, 035, 036, 037, 062 | written |
| 076 | 076-control-plane-integration-tests.md | Control Plane Integration Tests | L5 | M | 033, 034, 052, 053, 054, 055 | written |
| 077 | 077-chat-integration-tests.md | Chat Integration Tests | L5 | M | 043, 044, 045, 046, 047, 048 | written |
| 078 | 078-mcp-integration-tests.md | MCP Integration Tests | L5 | S | 020, 021, 022, 009 | written |
| 079 | 079-event-bus-job-queue-integration-tests.md | Event Bus and Job Queue Integration Tests | L5 | S | 024 | written |
| 080 | 080-security-observability-integration-tests.md | Security and Observability Integration Tests | L5 | S | 008, 009, 024, 063 | written |
| 081 | 081-org-bootstrap-e2e.md | Org Bootstrap E2E | L5 | S | 001–080 | written |
| 082 | 082-auth-flow-e2e.md | Auth Flow E2E | L5 | S | 001–080 | written |
| 083 | 083-chat-lifecycle-e2e.md | Chat Lifecycle E2E | L5 | M | 001–080 | written |
| 084 | 084-project-task-flow-e2e.md | Project and Task Flow E2E | L5 | M | 001–080 | written |
| 085 | 085-agent-management-e2e.md | Agent Management E2E | L5 | M | 001–080 | written |
| 086 | 086-memory-pipeline-e2e.md | Memory Pipeline E2E | L5 | M | 001–080 | written |
| 087 | 087-control-plane-e2e.md | Control Plane E2E | L5 | M | 001–080 | written |
| 088 | 088-full-workflow-e2e.md | Full Workflow E2E | L5 | M | 001–080 | written |
| 089 | 089-ci-pipeline.md | CI Pipeline | L5 | S | 001–088 | written |
| 090 | 090-deployment-packaging.md | Deployment Packaging and Docker Compose | L5 | S | 001–080 | written |
