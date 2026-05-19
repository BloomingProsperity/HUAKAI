# 2026-04-29 new-api cache billing reasoning R3
| Field | Value |
| --- | --- |
| Owner directive | "拆 7 项目深度必须够 + 保证真实，不造假" |
| Scope | Produce one source-verified clean-room decomposition for `new-api` cache-aware billing buckets and reasoning-effort handling at `docs/decompositions/new-api/cache-billing-reasoning-source-verified.md`. Read the critic first, then source regions, then write. Do not read the Claude deep draft. |
| Out of scope | Implementation code, database schema changes, billing/auth/quota runtime changes, LICENSE changes, and copying upstream source identifiers or code shapes into HUAKAI docs. |
| Success criteria | At least 12 distinct source regions are read and listed in §10; every §2 behavior claim cites a source region; critic findings are disposed as CONFIRM/REFUTE/OPEN; lifecycle traces and failure modes only use observed source behavior; final document ends with the required Chinese Owner summary. |
| Time estimate | 60-90 minutes wall clock; one Codex work unit. |
| Blast radius | Low: documentation-only replacement of a superseded decomposition. Main risk is overstating behavior or leaking AGPL implementation details. |
| Failure modes | Unsupported behavior claims, accidental upstream naming/path leakage, too few source regions, failure to address critic findings, or writing speculative failure modes. Mitigation: keep a separate region ledger, cite each behavior assertion, and use OPEN QUESTION when source observation is incomplete. |
| Decision points | Stop if the work would require modifying high-risk files; otherwise proceed because this is a low-risk documentation artifact under Owner start. |
| Pre-execution checklist | 1. Read critic. 2. Read project rules and glossary. 3. Locate source mirror. 4. Identify source regions without using the Claude draft. 5. Read 12+ regions around billing, usage, cache, reasoning, streaming, and settlement. 6. Draft with observed/inferred/open categories. 7. Check for forbidden upstream identifiers before finalizing. |
