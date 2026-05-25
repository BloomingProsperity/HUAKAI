# 2026-05-25 AE-D3 Admin UI Detailed Cause Synthesis (Claude x Codex)

- UTC: 2026-05-25T00:57:49Z
- 输入:
  - Claude: [2026-05-25-ae-d3-admin-ui-detailed-cause-claude.md](2026-05-25-ae-d3-admin-ui-detailed-cause-claude.md) (124 lines, recommends C mirror now + 30-day cleanup)
  - Codex: [2026-05-25-ae-d3-admin-ui-detailed-cause-codex.md](2026-05-25-ae-d3-admin-ui-detailed-cause-codex.md) (182 lines, recommends B audit-derived projection)
- 输出性质: synthesis only. No implementation. No `git add`. No commit. No push.

> For implementation workers: do not execute this plan until Owner approves §F. This artifact is the CLAUDE.md #10 parallel-draft step 3 synthesis. Reviewer lane read the two specifier plans and internal rule context only; it did not re-read reference-project source.

## §0 各 lane 揭示的关键事实

| # | 事实 | Claude lane | Codex lane | Synthesis impact |
|---|---|---|---|---|
| F-1 | `provider_accounts.last_refresh_outcome` already exists and is exposed through backend/API, but its CHECK does not include the S1 four detailed causes. | Covered with migration/generated-code/API citations. | Covered with migration/generated-code/API citations. | A/C cannot write detailed mirror until a migration widens the provider-account CHECK. |
| F-2 | `oauth_refresh_audit_events.outcome` already accepts `auth_expired`, `rate_limit_exceeded`, `risk_control_triggered`, and `account_disabled`. | Covered. | Covered. | Audit table is already the canonical detailed evidence candidate. |
| F-3 | Provider Accounts panel currently does not render `last_refresh_outcome`, even though the backend returns it. | Covered. | Covered. | "Mirror" alone does not create operator visibility; UI work is required for Panel 1. |
| F-4 | Renew Status panel currently renders "Last Result" from `account_credentials.last_refresh_outcome`, not `provider_accounts.last_refresh_outcome`. | Covered. | Covered and emphasized. | Changing only provider-account mirror does not satisfy Panel 5. |
| F-5 | `account_credentials.last_refresh_outcome` is loose text in the inspected table, while refresh failure currently stores coarse `refresh_failed` plus failure class. | Partly covered. | Covered more deeply. | If Owner wants Panel 5 detailed cause, B/audit projection is cleaner than adding a second mirror without a separate decision. |
| F-6 | Worker failure path records detailed audit outcomes and health transitions, but inspected path does not currently update the provider-account mirror with detailed cause. | Covered. | Covered more deeply. | A/C requires transaction design proving audit and mirror do not diverge. |
| F-7 | Both lanes treat raw provider/OAuth text as security-sensitive; UI default should show categorical outcome, not raw message. | Covered. | Covered. | Any option must preserve sanitization and token-leakage guardrails. |

Key fact count: **7**.

## §A 共识区

| 主题 | Claude | Codex | Synthesis |
|---|---|---|---|
| UI visibility | Operators need admin-surface visibility into detailed refresh cause. | Same: answer "yes" to direct admin UI display. | **Adopt: UI should show detailed categorical cause.** |
| Audit canonicality | Audit table remains forensic/canonical; mirror is current summary if used. | Audit table should be sole detailed truth for B; mirror only cache if used. | **Adopt: audit is canonical evidence in every option.** |
| Provider-account CHECK | A/C needs schema alignment before writing detailed values. | Same. | **Adopt: no detailed mirror write before PG CHECK migration/test.** |
| Panel separation | Panel 1 and Panel 5 are sourced differently and need explicit scope. | Same, stronger. | **Adopt: Owner must choose Panel 1, Panel 5, or both.** |
| Backfill | Do not synthesize historical detailed causes without observed audit row. | Same: no synthetic backfill. | **Adopt: audit-derived only; no guessed cause.** |
| Security | Outcome class is safe default; raw/redacted error detail needs tighter approval. | Same. | **Adopt: default UI shows class + timestamp, not raw upstream body/message.** |
| Tests | Use discriminating tests; A/C need migration/PG proof, B needs latest-audit projection proof. | Same. | **Adopt: test gate differs by option but must catch wrong source.** |

共识数: **7**.

## §B 冲突区

### B-1 Core Conflict: Claude C vs Codex B

| Dimension | Claude C: mirror now + 30-day cleanup | Codex B: audit projection, no mirror | Synthesis read |
|---|---|---|---|
| Data model | `provider_accounts.last_refresh_outcome` becomes a bounded current-state cache for the four detailed causes; audit stays canonical. | `provider_accounts.last_refresh_outcome` remains coarse/legacy; UI gets detailed cause from latest audit projection. | Both preserve audit canonicality. The disagreement is whether a row-level current cache is worth schema/write complexity. |
| Operator UX | Fastest path for Panel 1 after UI column/detail work; account list can read an existing response field. | Direct display still exists, but via a new `last_refresh_detail_*` DTO. | B is not "hide from UI"; it is "show from audit-derived projection." |
| Panel 5 fit | Does not solve Renew Status unless `account_credentials` also mirrors detail or endpoint joins audit. | Same projection can serve Panel 1 and Panel 5 without two mirrors. | This is the strongest argument for B if "admin UI" means both panels. |
| Schema risk | Requires provider-account CHECK migration now; possible later cleanup/clear/migrate. | Avoids provider-account CHECK churn for detailed cause. | B has lower schema blast radius; C has a smaller first-screen Panel 1 patch if Panel 1 only. |
| Consistency risk | Mirror can diverge from audit if every failure path and transaction boundary is not covered. | Latest-audit query can be expensive or ambiguous without tenant-scope/index/order discipline. | C's risk is stale/contradictory state; B's risk is projection query correctness/performance. |
| Implementation cost | A migration + worker mirror write + UI; if canonical audit link is required, still needs audit lookup. | Latest-audit query/service + DTO + UI; no mirror writer. | For Panel 1 only, C may be faster. For both panels, B may be simpler overall. |
| Cleanup | Explicit 2026-06-24 review gate decides keep/clear/replace mirror. | No cleanup gate needed for mirror; projection can evolve. | Cleanup gate is useful only if Owner values immediate Panel 1 cache enough to accept temporary duplication. |

### B-2 Synthesis Position

Synthesis recommends a **hybrid decision shape, with B as the target architecture and C as an optional narrow acceleration path**:

1. Product answer: **yes**, admin UI should directly show the detailed categorical cause.
2. Canonical source: **audit table remains canonical** for detailed evidence.
3. Target data-source strategy: **B audit-derived projection** when Panel 5 or both panels are in scope.
4. Narrow acceleration: **C temporary provider-account cache** is acceptable only if Owner selects Panel 1-first and wants the shortest account-list visibility path; it must include a 2026-06-24 cleanup/reconciliation gate.
5. Rejected shape: **A permanent mirror as canonical detailed truth**. Neither lane supports making `provider_accounts.last_refresh_outcome` the forensic source of record.

冲突数: **2 major conflict axes**: data source strategy and rollout speed vs duplication risk.

## §C 各方独有维度

| 来源 | 独有维度 | Synthesis 处理 |
|---|---|---|
| Claude | Explicit 30-day cleanup date for C: 2026-06-24 UTC. | 纳入 as required if Owner chooses C/hybrid cache. |
| Claude | Backfill should mark pre-decision values as existing state, not proof of detailed cause. | 纳入; no synthetic historical cause. |
| Claude | Mirror should be documented as "current operator summary", not audit evidence. | 纳入; this becomes required language for C. |
| Claude | If C selected, implementation commit/body or follow-up review file should record cleanup gate. | 纳入; required in execution sequence. |
| Codex | B can serve both Panel 1 and Panel 5 without two mirrors. | 纳入; primary reason synthesis prefers B target. |
| Codex | Worker account-refresh list does not preload mirror value; inspected failure path does not update provider-account mirror. | 纳入; A/C must add transaction coverage and tests. |
| Codex | OpenAPI currently uses loose strings; separate typed detail field is cleaner than tightening legacy field early. | 纳入; B/C should prefer `last_refresh_detail_*` if response shape changes. |
| Codex | Existing frontend health-state enum mismatch risk may surface during AE-D3 UI work. | 降级为 implementation watch item; not an Owner blocker for AE-D3 choice. |

各方独有维度数: **8**.

## §D 执行序 (synthesis 推荐)

```
[Decision Gate: Owner approves §F] (required)
  ├── D1: confirm direct detailed-cause UI display
  ├── D2: choose panel scope: Panel 1, Panel 5, or both
  ├── D3: choose data-source strategy: B target, C temporary cache, or A permanent mirror
  └── D4-D7: lock detail level, backfill, tests, and OpenAPI shape

[Slice AE-D3-0: source/scope confirmation] (0.5 day)
  ├── Re-read current migration head and confirm provider-account CHECK status
  ├── Reconfirm Panel 1 and Panel 5 API sources
  └── Write failing tests against the Owner-selected source; fixture must fail if the wrong field is used

[Slice AE-D3-B: audit-derived projection target] (0.5-1.5 days)
  ├── Add tenant-scoped latest-audit projection outside frozen-package new-file paths
  ├── Expose distinct detail fields, e.g. outcome/timestamp/source/request-id if approved
  ├── Render chosen panel(s) from projection
  └── Prove newest audit row wins, missing audit is neutral, and raw secret/error body is absent

[Optional Slice AE-D3-C: Panel 1 temporary mirror acceleration] (0.5-1 day, only if Owner chooses C)
  ├── Add PG migration widening provider-account CHECK
  ├── Update mirror only as current-state cache in the same transactional boundary as audit evidence
  ├── Render Panel 1 direct cache with audit link/provenance where available
  └── Record cleanup/reconciliation gate for 2026-06-24 UTC

[Review Gate]
  ├── Run focused backend tests; PG migration tests if A/C touches schema
  ├── Run frontend typecheck/build or targeted UI tests if UI changes
  ├── Stage intended diff only
  └── Run mandatory `codex exec review --uncommitted --full-auto --sandbox read-only`
```

Synthesis execution recommendation: **B target first if both panels or Panel 5 are in scope; C only as Panel 1-first acceleration with dated cleanup.**

## §E 借鉴对照 (from specifier-cited evidence; no reference source reread)

| Reference | Specifier-cited behavior | Synthesis read |
|---|---|---|
| Sub2API | Monitor flow records per-check history with status/latency/message/time, while latest-list queries expose concise current status/latency. `Wei-Shaw/sub2api@91da81599373:backend/internal/service/channel_monitor_service.go:269`, `Wei-Shaw/sub2api@91da81599373:backend/internal/repository/channel_monitor_repo.go:258` | Supports separating current operator summary from detailed history/audit. This can justify C as cache, but not as canonical evidence. |
| Sub2API | Ops path sanitizes upstream error text before storage/display. `Wei-Shaw/sub2api@91da81599373:backend/internal/service/ops_service.go:210`, `Wei-Shaw/sub2api@91da81599373:backend/internal/service/ops_service.go:283` | Supports categorical UI display and rejects raw provider error mirroring. |
| New-API | Channel error policy can update current channel state/reason/time and notify operators. `QuantumNous/new-api@20d3e7373452:service/channel.go:18`, `QuantumNous/new-api@20d3e7373452:model/channel.go:734` | Supports a current-state materialization pattern for operator recovery; HUAKAI must keep it independently designed and non-canonical if audit exists. |
| LiteLLM | Health-check storage/history exposes status/detail/time, and endpoints distinguish current and historical views. `BerriAI/litellm@79b457867197:litellm/proxy/schema.prisma:1045`, `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:455`, `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:1179` | Supports B/C split: current UI summary can exist, but historical evidence remains separately queryable. |
| LiteLLM | Health checks return healthy/unhealthy endpoint lists while retaining exception/status detail separately from the health-state map. `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:269`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:307` | Supports Codex B: UI can show direct detail through a projection without making the account row field the detailed source of truth. |
| CLIProxyAPI | Unauthorized refresh failure is persisted into current account/auth state and disables future auto-refresh scheduling for that auth entry. `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor.go:4164`, `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor_scheduler_refresh_test.go:81` | Supports C/A-style current-state cache for operator triage, but does not require HUAKAI to treat that cache as forensic evidence. |
| CLIProxyAPI docs | Local dashboards/managers expose account/channel/status/account-pool operational state. `zhanglunet/CLIProxyAPI@21fad9dbb447:README_CN.md:81`, `zhanglunet/CLIProxyAPI@21fad9dbb447:README_CN.md:85` | Supports the product answer that admin UI should show actionable detailed cause. |
| Portkey Gateway | Local gateway UI streams request logs with status and provides a details view. `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/log/index.ts:75`, `Portkey-AI/gateway@d2ea41f4e17c:src/public/index.html:1980` | Supports quick list status plus drilldown; for HUAKAI, mirror/projection should be summary-only with audit drilldown. |

Reference comparison count: **5 projects** (Sub2API, New-API, LiteLLM, CLIProxyAPI, Portkey Gateway).

## §F Owner 决策清单 (synthesis surface)

| ID | Owner decision | Options | Synthesis recommendation | 参考项目对照 (≥2 per decision) | Required now? |
|---|---|---|---|---|---|
| AE-D3-SD1 | Should admin UI directly display detailed refresh cause? | A: yes / B: no | **A: yes**. Operators should see categorical cause without DB queries. | CLIProxyAPI docs expose operational dashboard/account-pool state `zhanglunet/CLIProxyAPI@21fad9dbb447:README_CN.md:81`, `zhanglunet/CLIProxyAPI@21fad9dbb447:README_CN.md:85`; Portkey local UI exposes list status and details `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/log/index.ts:75`, `Portkey-AI/gateway@d2ea41f4e17c:src/public/index.html:1980`. | **Yes** |
| AE-D3-SD2 | Which admin surface is in this slice? | A: Panel 1 Provider Accounts / B: Panel 5 Renew Status / C: both | **C eventually; Owner should pick first slice.** If uncertain, choose **B target architecture serving both**, with UI rollout ordered by operational priority. | LiteLLM separates current/health and detail/history endpoints `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:455`, `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:1179`; Sub2API latest-list vs history pattern supports list + detail split `Wei-Shaw/sub2api@91da81599373:backend/internal/service/channel_monitor_service.go:269`, `Wei-Shaw/sub2api@91da81599373:backend/internal/repository/channel_monitor_repo.go:258`. | **Yes** |
| AE-D3-SD3 | What backs the visible detailed cause? | A: permanent mirror in `provider_accounts.last_refresh_outcome` / B: audit-derived projection / C: temporary mirror cache + audit canonical / D: mixed B target with optional C Panel 1 acceleration | **D**: B is target architecture; C allowed only for Panel 1-first acceleration with cleanup. Reject A as canonical. | LiteLLM detail separate from health-state map supports B/projection `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:269`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:307`; New-API current state/reason supports C/cache when operators need recovery state `QuantumNous/new-api@20d3e7373452:service/channel.go:18`, `QuantumNous/new-api@20d3e7373452:model/channel.go:734`; Sub2API current summary + history supports hybrid `Wei-Shaw/sub2api@91da81599373:backend/internal/service/channel_monitor_service.go:280`, `Wei-Shaw/sub2api@91da81599373:backend/internal/repository/channel_monitor_repo.go:263`. | **Yes** |
| AE-D3-SD4 | If C is chosen, what is the cache rule and cleanup gate? | A: no cache / B: cache indefinitely / C: cache as current summary until 2026-06-24 review | **C**. Cache is current operator summary only; audit is canonical; cleanup/reconcile on 2026-06-24 UTC. | CLIProxyAPI current-state failure persistence supports cache utility `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor.go:4164`, `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor_scheduler_refresh_test.go:81`; LiteLLM current/history separation supports not making cache the only truth `BerriAI/litellm@79b457867197:litellm/proxy/schema.prisma:1045`, `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:1179`. | Required only if C/D-with-C |
| AE-D3-SD5 | What may the UI show by default? | A: outcome class + timestamp / B: class + redacted error class / C: redacted message / D: raw provider/OAuth message | **A now; B only in detail drawer if already sanitized. Reject D.** | Sub2API sanitizes upstream error text before ops display/storage `Wei-Shaw/sub2api@91da81599373:backend/internal/service/ops_service.go:210`, `Wei-Shaw/sub2api@91da81599373:backend/internal/service/ops_service.go:283`; Portkey list status + detail view pattern supports progressive disclosure `Portkey-AI/gateway@d2ea41f4e17c:src/public/index.html:1980`, `Portkey-AI/gateway@d2ea41f4e17c:src/public/index.html:2033`. | **Yes** |
| AE-D3-SD6 | Backfill policy for detailed cause | A: no backfill / B: audit-derived only / C: synthetic inference from coarse fields | **B for projection; A acceptable for mirror. Reject C.** | Sub2API history rows provide observed per-check evidence before latest summary `Wei-Shaw/sub2api@91da81599373:backend/internal/service/channel_monitor_service.go:269`, `Wei-Shaw/sub2api@91da81599373:backend/internal/repository/channel_monitor_repo.go:258`; LiteLLM historical health records support deriving from stored observations, not guesswork `BerriAI/litellm@79b457867197:litellm/proxy/schema.prisma:1045`, `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:1179`. | **Yes** |
| AE-D3-SD7 | API/OpenAPI shape | A: reuse legacy `last_refresh_outcome` only / B: add separate `last_refresh_detail_*` projection fields / C: tighten legacy enum now | **B for B/C target; A only for narrow Panel 1 temporary mirror; avoid C until semantics settle.** | LiteLLM separates health-state/list detail from historical/detail surfaces `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:269`, `BerriAI/litellm@79b457867197:litellm/proxy/health_check.py:307`; Portkey separates list logs and details view `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/log/index.ts:75`, `Portkey-AI/gateway@d2ea41f4e17c:src/public/index.html:2033`. | **Yes if API changes** |
| AE-D3-SD8 | Test gate | A: UI fixture only / B: handler + UI / C: PG + handler + UI when schema changes | **C when A/C mirror touches schema; B when audit projection only.** Tests must fail if wrong source field wins. | LiteLLM and Sub2API both keep latest/current and history/detail as distinguishable surfaces, so HUAKAI tests must distinguish source paths `BerriAI/litellm@79b457867197:litellm/proxy/health_endpoints/_health_endpoints.py:455`, `Wei-Shaw/sub2api@91da81599373:backend/internal/repository/channel_monitor_repo.go:258`; CLIProxyAPI current-state tests show auth failure state can be asserted directly `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor_scheduler_refresh_test.go:81`, `zhanglunet/CLIProxyAPI@21fad9dbb447:sdk/cliproxy/auth/conductor_scheduler_refresh_test.go:89`. | **Yes** |

§F Owner 决策清单 D 数: **8**.

Synthesis recommended option: **Mixed D** — product decision "yes, direct UI display"; data target **B audit-derived projection**; optional **C temporary Panel 1 cache** only if Owner wants fastest Provider Accounts visibility and accepts a dated cleanup gate.

## Owner 中文摘要

这份 synthesis 只读取 Claude/Codex 两条已带 source citation 的 specifier plan 和内部规则，没有重读 reference 源码。真实共识是：详细 cause 应该进入 admin UI、audit table 必须保持 canonical、`provider_accounts` mirror 写入前必须先改 CHECK、Panel 1/Panel 5 数据源不同、不能做 synthetic backfill、默认 UI 只能显示分类原因不能泄漏 raw error。核心冲突是 Claude C 的 "先镜像当 current cache + 30 天清理" 和 Codex B 的 "不镜像、从 audit projection 读"; synthesis 推荐混合 D：目标架构选 B，若 Owner 要 Panel 1 最快上线可临时采用 C，并强制 2026-06-24 清理/对账 gate。没有功能缩水；clean-room 风险低；安全风险集中在 UI 不得显示 raw provider/OAuth 错误或 secret；需要 Owner 现在确认 §F 的 8 个决策。

## §G Lane + UTC

Clean-room provenance:
- Source files read: `CLAUDE.md`; `docs/process/plans/2026-05-24-boringssl-phase-4-5-synthesis.md`; `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-claude.md`; `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-codex.md`.
- Specifier-cited reference evidence reviewed through those plan artifacts, not re-read in this reviewer lane: Sub2API monitor/ops cited regions; New-API channel status cited regions; LiteLLM health/history cited regions; CLIProxyAPI auth/dashboard cited regions; Portkey gateway log/UI cited regions.
- Lane: reviewer
- Agent: codex-cli (GPT-5)
- UTC timestamp: 2026-05-25T00:57:49Z
