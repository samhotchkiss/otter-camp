# Spec Issues

Issues found during spec review that must be resolved before building starts.
Each issue must be resolved by Sam before Codex picks up the affected task.

| # | Severity | Affected Docs | Issue | Status |
|---|----------|--------------|-------|--------|
| 1 | AMBIGUOUS | 05, 14 | Doc 05 defines `budget_cap_cents` on the agent table. Doc 14 resolved question #24 says rename it to `budget_cap_tokens` (tokens as universal budget unit, not cents). The schema in doc 05 was not updated to reflect this resolution. Which column name and type is correct? | Open |
| 2 | AMBIGUOUS | 04, 14 | Doc 04 describes the bootstrap sequence (steps 1-7) and doc 14 has an authoritative 10-step bootstrap sequence. Doc 14 itself says it is authoritative and that doc 04 "defers to doc 14" (resolved question #7). However doc 04's bootstrap section does not say this — it stands as-is. Implementer will need to use doc 14's 10-step sequence. Flag for doc 04 update. | Open |
| 3 | GAP | 03, 16 | inbox_item.item_type includes 'browser_handoff' in the schema check constraint but the spec text for doc 03 does not define what a browser_handoff inbox item is. Doc 11 (not yet read) likely defines it. Need to confirm when reading doc 11. | Open |
| 4 | AMBIGUOUS | 05, 14 | Doc 14 resolved question #8 says Lori has private memory enabled: "doc 05 is authoritative — all staff agents (including Lori) have private memory enabled." Doc 05 states private_memory_enabled defaults to false for "all other staff" except PMs, Frank, Lori, and Ellie. This appears consistent but doc 14's mention of a prior inconsistency suggests there was a version where Lori did not have private memory. Confirm doc 05 is the final word: Lori has private_memory_enabled=true. | Open |
