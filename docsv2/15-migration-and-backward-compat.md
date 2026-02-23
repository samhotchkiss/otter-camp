---
## Summary

This spec defines the migration and backward compatibility policy for OtterCamp V2: a clean-room rebuild that shares no code, schema, or data with V1. V1 was architecturally bound to OpenClaw for agent orchestration, and since V2 replaces OpenClaw with direct in-app orchestration, there is no incremental migration path. V2 starts from an empty database, runs its own DDL, and populates itself through a bootstrap flow that seeds a minimum dataset: the starter trio of agents (Frank, Lori, Ellie), four default flow templates, two org-default skills (Safety and Communication Policies, General Work Standards), three Anthropic model profiles (high-capability, standard, Haiku), a default org policy, and the "General" org-level chat session. Bootstrap is idempotent -- it runs only when no organization row exists.

The only data bridge between V1 and V2 is an optional JSONL memory import path. V1 conversation transcripts can be exported as JSONL, imported via the CLI (`ottercamp memory import`), and processed through V2's full extraction pipeline (garbage rejection, LLM extraction, scoring, normalization, embedding, and dedup). Imported memories receive `source_type = 'import'` and a trust tier of 0.6 (medium-low), meaning they start at lower confidence than V2-native memories and require corroboration to be promoted. No extraction stage is skipped -- V1 data must meet V2's quality standards. No other V1 data (projects, tasks, chat history, agent profiles) is importable; only memory is considered worth bridging, since structural data can be quickly recreated through conversation.

V2's API is entirely new with no backward compatibility layer, no migration shims, and no V1 endpoint emulation. External integrations must be rewritten. V1 archives (database backups, JSONL exports, source code, documentation) are retained indefinitely in cold storage but are never queried at runtime. The rollback strategy is forward-only: if V2 has issues, fix them in V2. V1 restoration is a documented last resort but depends on OpenClaw availability and loses all V2-era data. Cutover is human-initiated -- the user installs V2, bootstraps it, optionally imports V1 memories, and starts working. There is no automated migration phase, no parallel-run reconciliation, and no cutover script. The document includes a comprehensive pre-launch validation checklist covering fresh install, core workflows, auth, observability, memory import, and deployment.

---

# 15. Migration and Backward Compatibility

## Objective

Define the cutover policy for a clean V2 launch, the relationship between V1 and V2 systems, and the limited data bridge between them. This document answers the question: "what happens to V1 when V2 ships?"

The short answer: V2 is a clean-room rebuild. V1 is archived. The only bridge is an optional memory import path through Ellie's standard JSONL importer.

## Why a Clean Break

### V1 Architecture Constraints

V1 was built on OpenClaw as its agent orchestration layer. Every architectural decision in V1 was shaped by OpenClaw's abstractions: its memory model, its context management, its tool calling conventions, its session lifecycle. These abstractions leaked into every layer of the V1 codebase — from the database schema (which mirrored OpenClaw's entity model) to the API surface (which proxied OpenClaw's interfaces) to the agent runtime (which delegated all orchestration to OpenClaw).

V2 replaces OpenClaw with direct in-app orchestration (doc 01). This is not a refactor — it is a different architecture. The domain model, the agent runtime, the memory system, the session lifecycle, and the control plane are all designed from first principles. Attempting to migrate V1 code or schema into this new architecture would mean either (a) forcing V2 into V1's shape or (b) rewriting every migrated component anyway. Neither is productive.

### Schema Debt

V1's database schema accumulated design decisions that V2 explicitly rejects:

- Two separate memory kind taxonomies (agent-memory vs Ellie-memory) — V2 unifies these (doc 06).
- No scope isolation on memories — V2 enforces org/project/task scoping with foreign keys (doc 06).
- No entity synthesis table — V2 has `memory_entity` and `memory_entity_mention` as core schema (doc 06).
- No dedup tracking — V2 has `memory_dedup_reviewed` for cursor-based progress (doc 06).
- No flow template or flow node tables — V2 introduces a full task flow system (doc 03).
- No control plane or capability model — V2 has a policy engine with layered evaluation (doc 16).

Migrating data from V1's schema to V2's would require a bespoke transformation layer that translates between fundamentally different data models. The effort would exceed the value — V2's schema is designed to be populated fresh through its own extraction and bootstrap pipelines.

### OpenClaw Dependency

V1 cannot run without OpenClaw. V2 cannot run with it. There is no incremental path from one to the other. A clean break is the only honest option.

## What "Clean Room" Means Practically

- **No V1 runtime code is reused.** V2 is a new codebase. V1 source may be read for reference (understanding past behavior, extracting domain knowledge) but no V1 function, module, or package is imported into V2.
- **No V1 database schema is reused.** V2 defines its own DDL across all specs (docs 02, 03, 03a, 04, 06, 07, etc.). Schema migrations run forward from an empty database. There is no ALTER TABLE path from V1 to V2.
- **No V1 data is migrated.** V2 starts with a fresh database populated only by the bootstrap flow and (optionally) the JSONL memory importer. There is no ETL pipeline, no data transformation, no V1-to-V2 data mapping.
- **No V1 API compatibility.** V2's API is entirely new. V1 API endpoints, request/response shapes, and authentication mechanisms are not preserved.
- **V1 informs requirements, not implementation.** V1's product knowledge — what features worked, what users expected, what domain concepts are important — is input to V2's spec process. But V1's code does not constrain V2's architecture, module boundaries, or design decisions.

## What Carries Over from V1

Nothing carries over in code, schema, or data. What carries over is knowledge:

### Product Knowledge

V1 validated core concepts that V2 builds on:

- Chat-primary interface works for agent interaction.
- Memory extraction from conversations is valuable but quality-dependent.
- Entity synthesis is the single biggest retrieval improvement (+15pp hit rate, doc 06).
- 1536d OpenAI embeddings dramatically outperform alternatives (+20pp, doc 06).
- Hybrid BM25+vector search hurts rather than helps (-5pp, doc 06).
- Importance-weighted retrieval hurts rather than helps (-4pp, doc 06).
- File-backed memories augment conversation-extracted memories but do not replace them (doc 06).

These findings are design constraints in V2 — they are documented in doc 06's "V1 Lessons Learned" section and directly shaped the V2 memory system design.

### User Expectations

V1 established expectations about how OtterCamp works: conversational interaction with agents, memory continuity across sessions, project-based work organization. V2 preserves these expectations at the product level while completely reimplementing the system underneath.

### Domain Model Concepts

The core domain concepts (organization, user, agent, chat session, project, task, memory) persist from V1 to V2 — but their definitions, relationships, schemas, and behaviors are all new. The vocabulary is similar; the implementation is not.

## Data Handling

### No Data Migration

V1 data is not migrated to V2. This is a firm policy, not a deferral. The reasons:

1. **Schema incompatibility.** V1 and V2 schemas are fundamentally different. Any migration would be a lossy, best-effort transformation — not a reliable data transfer.
2. **Quality concerns.** V1 data accumulated without the quality gates, scope isolation, and dedup infrastructure that V2 enforces. Migrating V1 data would import V1's quality problems into V2.
3. **Fresh-start value.** V2's extraction pipeline, taxonomy system, and consolidation jobs are designed to build high-quality knowledge from scratch. Starting clean lets these systems operate without legacy noise.
4. **Complexity budget.** Building, testing, and supporting a V1-to-V2 migration pipeline is engineering effort that does not advance V2's product goals.

### V1 Data Retention

V1 data is not deleted. It is archived:

- V1 database backups are retained offline indefinitely for reference. No expiry.
- V1 memory exports (JSONL) are retained offline indefinitely. These are the source material for optional V2 import.
- V1 source code is archived in version control for reference.
- V1 documentation is archived for reference.

There is no runtime read-through to V1 data stores. V2 never queries V1 databases. V1 archives are cold storage — accessible to humans for research, not accessible to V2 systems at runtime.

## V1 Memory Import Path

### The One Bridge

The JSONL memory importer (doc 06, Importer section) is the only data bridge between V1 and V2. It is not a "migration tool" — it is V2's standard bulk import facility, which happens to accept the format that V1 exports produce.

This distinction matters: the importer is a general-purpose feature designed for loading historical data from any source. V1 happens to be one such source. The importer does not know or care that the data came from V1.

### How It Works

1. A human exports V1 memory data as JSONL files (using V1's export tooling, which runs against the V1 archive).
2. The human uploads JSONL files to V2 via the CLI (`ottercamp memory import <path>`).
3. V2's importer processes every JSONL file through the full extraction pipeline — the same pipeline that processes ongoing conversations:
   - Stage 0: Garbage pattern rejection (deterministic pre-filter)
   - Stage 1: LLM extraction (Haiku-class model)
   - Stage 2: Scoring and filtering (threshold 40)
   - Stage 3: Normalization (entity names, taxonomy classification)
   - Stage 4: Embedding, dedup, and storage
4. Extracted memories enter V2 with `source_type = 'import'` and trust tier 0.6 (medium-low, per doc 06's source trust tiers).
5. The import job tracks progress: files processed, memories extracted, duplicates skipped, errors encountered.
6. Ellie reports results to the human when the import completes.

### No Shortcuts

Imported data goes through the full extraction pipeline. No stages are skipped. This means:

- Garbage patterns from V1 are filtered out at Stage 0.
- Low-quality content is rejected at Stage 2 scoring.
- Entity names are normalized to V2's entity model at Stage 3.
- Taxonomy nodes are created or mapped at Stage 3.
- Near-duplicates are caught at Stage 4 dedup.
- V2's quality standards apply uniformly — V1 data is not grandfathered in.

### Quality Limitations

The quality of imported memories is limited by the quality of V1's JSONL exports:

- V1 exports are conversation transcripts, not pre-extracted memories. V2's extraction pipeline must do the work of identifying valuable information.
- V1 conversation quality varies. Some conversations are rich with decisions and context; others are routine and produce few extractable memories.
- The import trust tier (0.6) means imported memories start with a lower confidence cap than memories from direct V2 conversations (0.8) or human statements (1.0). This is deliberate — imported data has not been verified in the V2 context.
- Imported memories can be promoted to higher confidence through corroboration: if a V2 conversation confirms information from an import, the memory's confidence cap rises based on the new, higher-trust source.

### Import Is Optional and Ongoing

The JSONL import is entirely optional. V2 is fully functional without any V1 data. Organizations that are new to OtterCamp will never use this feature. Organizations migrating from V1 can choose to import historical memory data or start completely fresh — either path is supported.

The importer is a permanent CLI capability, not a one-time migration tool. The operator can run `ottercamp memory import` at any point — during initial setup, weeks later when they realize they want historical context, or whenever they have new JSONL data from any source. There is no UI for this; it is CLI-only.

### No Other Data Bridges

The JSONL memory importer is the only data bridge between V1 and V2. There is no CSV import, no JSON import for projects or tasks, no API-to-API sync, and no schema-level migration. Other V1 data (projects, tasks, chat history, agent profiles) is not importable. This is deliberate:

- **Projects and tasks** are structural data, not knowledge. Recreating project structure in V2 takes minutes through conversation with Frank and Lori. Migrating it requires mapping between incompatible schemas for marginal benefit.
- **Chat history** is valuable as a memory source (which is why JSONL import exists) but not as a direct import target. V2's chat schema (doc 02) differs fundamentally from V1's.
- **Agent profiles** are rebuilt from scratch in V2 with new prompt packs, tool policies, model policies, and memory policies (doc 05). V1 agent configurations are not compatible.

## Bootstrap Flow

### What Happens on First V2 Install

When V2 starts for the first time with an empty database, the bootstrap sequence runs (fully specified in doc 14, Bootstrap Dataset section; doc 04 covers the auth-relevant subset):

1. **Run schema migrations.** Create all database tables from the V2 DDL defined across specs. The migration system (doc 08) runs automatically on startup.

2. **Create organization.** A default organization is created. In self-hosted mode, this is the only org. Name defaults to "My Organization" (configurable via CLI or environment variables).

3. **Create first user.** The operator provides an email and password. This user is created as the `owner` of the default org.

4. **Create org-level skills repo and populate with default skills.** The org skills repo is created and seeded with identity skills for the starter trio and PM, plus two org-default skills: Safety and Communication Policies (sensitive info handling, escalation, communication boundaries) and General Work Standards (commit conventions, handling ambiguity, blocker vs workaround). See doc 10.

5. **Seed model profiles.** Three system-provided model profiles (doc 07):
   - **High-capability**: Anthropic Claude Opus (or current best). Default for Frank, Lori, PMs. Assigned as the org default.
   - **Standard**: Anthropic Claude Sonnet (or current mid-tier). Default for workers and reviewers. Falls back to high-capability.
   - **Haiku**: Anthropic Claude Haiku (or current fast/cheap tier). Used for listening evals, summarization, memory extraction, and memory synthesis.

6. **Seed default flow templates.** System-provided flow templates (null `project_id`, doc 03):
   - **"Single Step"**: one work node, no review. For simple, self-contained tasks.
   - **"Work + Review"**: work node → review node (reject loops back). The standard pattern for most tasks.
   - **"Work + Code Review + Human Review"**: work → agent code review → human review gate. For sensitive changes.
   - **"Research"**: single work node, no review. For exploration tasks where the deliverable is knowledge, not code.

   PMs design additional templates through conversation as projects require them.

7. **Seed starter trio.** The three foundational agents are created:
   - **Frank** (Chief of Staff): org-level, the human's primary touchpoint, default responder in the org session.
   - **Lori** (Agent Relations Expert): handles staffing and hiring agents for projects.
   - **Ellie** (Memory): dual role — background memory infrastructure AND conversational agent for memory queries.

   Each agent is created with its full profile: identity metadata, prompt pack (including identity skill from step 4), default tool policy, and model policy assignment (doc 05).

8. **Create org session.** The persistent org-level chat session ("General") is created with Frank as the default responder. Participants: the human (owner), Frank (default responder), Lori (participant), Ellie (listener).

9. **Seed default org policy.** The default organization policy: communication tools create drafts, CLI and browser require capability grants, MCP tools require per-connection grants, max tool calls per turn (50), max turn duration limits (doc 16).

10. **Record bootstrap event.** An audit event records that bootstrap completed, including the org ID, user ID, and agent IDs created.

### Minimum Bootstrap Dataset

The minimum bootstrap dataset is the set of data that ships with every V2 installation. It is not configurable — every V2 instance starts with exactly this data. See doc 14 (Bootstrap Dataset section) for the complete specification including profiles, tool policies, and configuration details.

| Entity | Contents | Source Doc |
|--------|----------|------------|
| Organization | One org (self-hosted) or org-per-tenant (managed) | Doc 04 |
| Human User | One owner user | Doc 04 |
| Skills Repo | Org-level skills repo with identity skills (Frank, Lori, Ellie, PM) and two org defaults | Doc 10 |
| Model Profiles | High-capability, standard, and Haiku (all Anthropic) | Doc 07 |
| Flow Templates | "Single Step", "Work + Review", "Work + Code Review + Human Review", "Research" | Doc 03 |
| Agents | Frank, Lori, Ellie (starter trio) with profiles, skills, and tool policies | Docs 04, 05 |
| Chat Session | "General" org session (Frank as default responder, Ellie as listener) | Doc 02 |
| Org Policy | Default org policy (communication drafts, capability grants, turn limits) | Doc 16 |
| Audit Event | Bootstrap completed event | Doc 04 |

This dataset is sufficient for a human to start using V2 immediately — open the TUI, talk to Frank, and begin working. Everything else (additional agents, projects, tasks, flow templates, skills, memory) is created through normal product usage.

### Bootstrap Is Idempotent

If OtterCamp starts and the database already has an org and user, the bootstrap sequence is skipped. This is detected by checking for the existence of any `organization` row (doc 04). This means restarting the application never re-seeds data.

## API Transition

### V2 API Is Entirely New

V2's API is a new design, informed by V2's domain model and architecture. It does not preserve V1 API endpoints, request/response shapes, or authentication mechanisms.

- V1 API endpoints are not available in V2.
- V1 API keys do not work in V2. New API keys must be created through V2's key management (doc 04).
- V1 authentication sessions do not carry over. Users must create new accounts in V2.
- V1 webhook integrations do not work in V2. Integrations must be rebuilt against V2's event system (doc 12).

### External Integration Impact

Any system that integrated with V1's API must be rewritten for V2. This includes:

- Scripts or automation that call V1 endpoints.
- MCP connections configured for V1 (must be reconfigured for V2, doc 09).
- CLI tools built against V1's interface (V2 ships its own CLI, doc 11).
- Browser extensions or external UIs built against V1's API.

There is no backward-compatibility layer, no API versioning that bridges V1 and V2, and no translation proxy. V2's API is the canonical interface from day one.

### No Migration Shims

V2 does not include migration shims, compatibility adapters, or V1 API emulation layers. If a temporary shim were to be created (which is not planned), it would be isolated, time-boxed, and explicitly prevented from constraining V2's architecture. The bar for creating any shim is high: it must not add complexity to V2's codebase and must have a hard removal date.

## Rollback Plan

### If V2 Launch Fails

V1 is archived and can be restored to operational status if V2 encounters critical issues during launch:

1. V1 database backups are retained and can be loaded into a fresh PostgreSQL instance.
2. V1 application code is archived in version control and can be deployed.
3. V1's OpenClaw dependency would need to be available (this is the main risk — if OpenClaw is no longer accessible, V1 cannot run).

### Rollback Limitations

- **V2 data does not flow back to V1.** Any work done in V2 — conversations, projects, tasks, memories — cannot be migrated to V1. Rolling back means losing V2-era data.
- **The rollback window is limited.** As time passes after V2 launch, V1 restoration becomes less viable: OpenClaw availability may degrade, V1 infrastructure may be decommissioned, and the gap between V1's archived state and current reality grows.
- **Rollback is a last resort, not a safety net.** The build phases (doc 14) include validation gates at each stage specifically to avoid needing a rollback. The goal is to validate V2 thoroughly before any user-facing cutover.

### Forward-Only Recovery

The preferred recovery strategy is forward: if V2 has issues, fix them in V2. The architecture (modular monolith, doc 01) and deployment model (Docker Compose for self-host, doc 08) are designed for rapid iteration. A bug in V2 should be fixed and deployed, not recovered by reverting to V1.

## Timeline and Phases

### Alignment with Build Phases (Doc 14)

Migration and cutover align with doc 14's build phases:

**Phase 0 (Foundation):** No migration activity. V2's domain model, API contracts, and schema are frozen. This is where the V2 foundation is built — migrations, auth, event bus, agent runtime.

**Phase 1 (Synchronous Chat via TUI):** No migration activity. V2 is usable as a chat product. The starter trio is operational. Ellie's memory system begins accumulating V2-native memories. The human can optionally run the JSONL importer at this point to load V1 historical memories — but this is user-initiated, not a system migration step.

**Phase 2 (Projects and Tasks):** No migration activity. V2's project and task systems come online. Work begins happening natively in V2. If V1 was still operational in parallel, this is when the human would begin directing new work to V2 instead.

**Phase 3 (OtterCamp Builds Itself):** V2 is the primary system. V1 is no longer used for active work. V1 archives are retained for reference. If the human has not yet imported V1 memories via JSONL, they may choose to do so now — by this phase, V2's memory system is mature enough to benefit from historical context.

**Phase 4 (Hardening and Distribution):** V2 is production-ready. This is when formal cutover happens for any remaining V1 users:

- V1 systems are formally decommissioned (infrastructure shut down, not data deleted).
- V1 archives are moved to cold storage.
- V2 is the sole operational system.
- Documentation, runbooks, and support processes reference V2 exclusively.

### There Is No "Migration Phase"

Migration is not a phase — it is the absence of one. V2 starts fresh. The "cutover" from V1 to V2 is the human deciding to use V2 instead of V1. There is no data migration step, no parallel-run reconciliation, no cutover weekend. The human installs V2, bootstraps it, optionally imports V1 memories, and starts working.

## Validation Checklist

### Pre-Launch Validation

Before V2 is considered launch-ready, the following must be confirmed:

**Fresh Install**
- [ ] Bootstrap completes successfully on an empty database.
- [ ] All schema migrations run without error.
- [ ] Starter trio (Frank, Lori, Ellie) is created and operational.
- [ ] Default flow templates are created and usable.
- [ ] Default org skills (Safety and Communication Policies, General Work Standards) are created and loaded into agent prompts.
- [ ] Default model profiles (high-capability, standard, Haiku) are created and functional (inference succeeds).
- [ ] Org session ("General") is created with correct participants.
- [ ] Bootstrap is idempotent — restarting the application does not re-seed.

**Core Workflows**
- [ ] Human can chat with Frank in the org session.
- [ ] Human can @mention Lori and Ellie in conversation.
- [ ] Human can create a project through conversation with Frank.
- [ ] Human can create and manage tasks through conversation.
- [ ] Tasks can execute through flow templates (Single Step, Work + Review).
- [ ] Ellie extracts memories from conversations (implicit capture).
- [ ] Ellie responds to direct memory queries (@mention).
- [ ] Memory retrieval returns relevant results in new conversations (passive injection).

**Auth and Permissions**
- [ ] Email/password login works.
- [ ] API key creation, authentication, and revocation work.
- [ ] Organization-level data isolation is enforced (for managed multi-tenant).
- [ ] Rate limiting is functional on login and API endpoints.
- [ ] Session management (creation, sliding expiry, revocation) works.

**Observability and Audit**
- [ ] Audit events are recorded for security-sensitive actions.
- [ ] Bootstrap completion is recorded as an audit event.
- [ ] Agent actions are attributable to the correct principal.
- [ ] Error logging captures actionable information.

**Memory Import (Optional Path)**
- [ ] JSONL import via CLI (`ottercamp memory import`) works end-to-end.
- [ ] Imported data goes through the full extraction pipeline (no stage is skipped).
- [ ] Import job tracks progress and reports results.
- [ ] Imported memories have `source_type = 'import'` and trust tier 0.6.
- [ ] Malformed JSONL lines are skipped and logged, not fatal.
- [ ] Duplicate detection works across imported and native memories.

**Deployment**
- [ ] Docker Compose deployment works for self-hosted mode (doc 08).
- [ ] Schema migrations run automatically on startup (doc 08).
- [ ] Application starts cleanly with only required environment variables.
- [ ] Backup and restore procedures are documented and tested.

### Post-Launch Monitoring

After V2 launches, monitor for:

- Memory extraction quality — are Ellie's extractions from V2 conversations meeting quality expectations?
- Retrieval relevance — are agents receiving useful context through passive injection?
- Import success rates — for users who import V1 memories, what percentage of content produces useful memories?
- Bootstrap reliability — do new installations complete without intervention?
- API stability — are external integrations (rebuilt for V2) functioning correctly?

## Resolved Decisions

1. **V2 is a clean-room rebuild.** No V1 code, schema, or data is reused. V1 informs requirements, not implementation. This is the foundational decision that shapes everything else in this document.

2. **Optional JSONL import for V1 memories is the ONLY data bridge.** No CSV/JSON import for other V1 data (projects, tasks, chat history, agent profiles). Memory is the only V1 artifact worth importing, and it goes through the standard extraction pipeline — not a special migration path.

3. **V1 archives retained indefinitely.** No expiry on V1 database backups, memory exports, source code, or documentation. Cold storage, not active infrastructure.

4. **Minimum bootstrap dataset defined.** Every V2 installation ships with: starter trio agent profiles (Frank, Lori, Ellie), four default flow templates, two org-default skills (Safety and Communication Policies, General Work Standards), three model profiles (high-capability, standard, Haiku), default org policy, and the "General" org session. Full specification in doc 14's Bootstrap Dataset section. This is not configurable — it is the product's starting state.

5. **No V1 API backward compatibility.** V2's API is entirely new. No migration shims, no compatibility adapters, no V1 endpoint emulation. External integrations must be rewritten.

6. **Rollback is forward, not backward.** If V2 has issues, fix them in V2. V1 restoration is a last resort with significant limitations (OpenClaw dependency, data loss of V2-era work). The build phases include validation gates to avoid needing rollback.

7. **Cutover is human-initiated, not system-orchestrated.** There is no automated migration phase, no parallel-run reconciliation, no cutover script. The human installs V2 and starts using it. They import V1 memories if they want to.

8. **Imported data gets no special treatment.** V1 JSONL imports go through every stage of the extraction pipeline. Quality gates, scoring thresholds, dedup, and normalization all apply. V1 data is not grandfathered past V2's quality standards.

9. **Import trust tier is 0.6 (medium-low).** Imported memories cannot exceed 0.6 initial confidence without subsequent corroboration from a higher-trust source. This reflects the inherent uncertainty of historical data processed out of its original context.

10. **JSONL import is CLI-only and permanently available.** `ottercamp memory import` is a permanent CLI capability, not exposed in the web UI. The operator can import at any time — during initial setup, weeks later, or whenever they have new JSONL data from any source.

## Open Questions

- **Starter trio profile updates on upgrade**: when a new OtterCamp version ships with updated prompt packs, policies, or tool configurations for the starter trio (Frank, Lori, Ellie), how are those applied to existing installs? Bootstrap is idempotent and skips if an org exists, so upgrades need a different mechanism. Options: forward-only migration that patches agent rows, a "system profile version" check on startup, or a manual `ottercamp upgrade-agents` CLI command. Flagged from doc 04.

All other questions from the original skeletal spec have been resolved:

- **"Do we offer optional CSV/JSON import tools later, or keep V2 strictly greenfield?"** — Resolved: JSONL memory import only. No CSV/JSON import for other data types. The JSONL importer is the standard bulk import facility (doc 06), not a V1-specific migration tool.

- **"How long do we keep V1 archives available internally?"** — Resolved: indefinitely. No expiry on V1 archives. They move to cold storage but are never deleted.

- **"What minimum bootstrap dataset (if any) should ship with V2?"** — Resolved: starter trio agent profiles, four default flow templates, two org-default skills, three model profiles, default org policy, org session ("General"). Full specification in doc 14's Bootstrap Dataset section. This dataset ships with every V2 installation.
