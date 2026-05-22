# 2026-05-21 audit-b-codex

| Owner directive | "HUAKAI 全面自查 —— 一个 specifier lane" / "LANE_ID = B (网关与路由)" |
| Scope | In: §2 model access, §5 gateway core, §7 routing and scheduling; HUAKAI source evidence; sub2api / CLIProxyAPI / new-api behavior evidence; lane report only. Out: HUAKAI implementation changes, commits, deployment, schema/auth/billing/quota changes. |
| Success criteria | `docs/process/research/2026-05-21-audit-B.md` exists; every assigned leaf has HUAKAI status evidence or "未找到"; every reference-project claim has a clean-room citation; extra HUAKAI modules in this lane are listed; report tail includes source files read, lane, agent, UTC timestamp. |
| Time estimate | 2-4 hours wall clock; single Codex session. |
| Blast radius | Low for repository behavior because only plan/report files are written; audit quality risk is high if evidence is incomplete or speculative. |
| Failure modes | Missing a provider path: mitigate with `rg` across handler/router/adapter names. Clean-room leakage: paraphrase behavior only, avoid upstream identifiers in prose except citation paths. Stale new-api SHA: read local HEAD with `git -C ~/refs/new-api rev-parse HEAD`. Overclaiming: mark "未找到" or "Open question" instead of guessing. |
| Decision points | If a required source tree is absent or unreadable, record the gap in the report. If non-report edits appear necessary, stop and ask Owner. |
| Pre-execution checklist | 1. Confirm report path and local source roots. 2. Read existing W1 retry/failover report. 3. Locate HUAKAI gateway/protocol/routing/adapters/tests. 4. Locate each reference project's provider, gateway, and routing surfaces. 5. Record citations while drafting. 6. Run final citation/clean-room self-check. |

Concrete execution order:

1. Read `docs/process/research/2026-05-21-audit-w1-phase1-retry-failover.md` and relevant HUAKAI docs for gateway/routing terminology.
2. Use `rg --files` and targeted `rg` to map HUAKAI gateway handlers, protocol conversion, vendor adapters, routing, failover, cache, token/account pool, and tests.
3. Read the smallest HUAKAI code regions needed to support each leaf status and line-cite them.
4. Read sub2api / CLIProxyAPI / new-api source regions for comparable vendor adaptation, mixed provider selection, routing, retry/failover, timeout/streaming/error behavior, and limit/cooldown semantics.
5. Write the lane report in Chinese with status, parity, gaps, tree-missing modules, total gap table, and required clean-room tail.
6. Self-check that no raw upstream code, distinctive identifiers, comments, schemas, or line-by-line algorithm descriptions were copied.
