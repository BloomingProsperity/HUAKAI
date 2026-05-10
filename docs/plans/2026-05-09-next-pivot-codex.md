# 2026-05-09 Next Pivot Plan - Codex Independent Lane

| Field | Value |
| --- | --- |
| Author | Codex independent lane |
| Branch | `claude/phase-1` |
| Owner directive | "PASR A1/A2/A3 已落今天主线。下一步 commit 应该是什么？" |
| Forbidden input | Did not open/read `docs/plans/2026-05-09-next-pivot-claude.md` |
| Upstream source discipline | Did not read sub2api / one-api / litellm / portkey / helicone / new-api source in this lane. Upstream claims below cite the 2026-05-09 source-read lane outputs using `<repo>@<sha>:<file>:<line>` form. |
| Scope | Choose the next commit direction and define a 1-week sprint plan. This is planning only; no implementation in this artifact. |
| Out of scope | Schema migrations, real secrets, auth-core implementation, billing-ledger mutation, payment logic, production deployment. |
| Success criteria | Owner can compare this plan with the Claude lane plan; recommended next commit has clear tests, acceptance criteria, risks, and Owner decision points. |
| Time estimate | Plan review: <1h. Recommended sprint: 3-5 engineering days + 1-2 days review/fix buffer. |
| Blast radius if wrong | Wrong pivot could waste the PASR A1/A2/A3 investment, leave protocol breadth stalled, or pull the team into high-risk auth/billing work before the current cache signal path is true. |

Truth-first metadata: Observed regions: 17 local docs/code/report regions. Inferences: 9. Open questions: 5.

Scoring scale: `5 = best`. For `技术风险可控性` and `clean-room 风险可控性`, `5` means lower risk / more controllable.

## Evidence Base

HUAKAI's product goal is commercial relay-station breadth: the Owner-stated differentiator is more provider/API/model coverage than Sub2API, and provider adapter coverage is promoted to a first-class L1/L2 concern (`docs/01_PROJECT_BRIEF.md:11-18`). v1 success also requires commercial Model 1 operation, provider catalog breadth, and source-verified algorithms (`docs/01_PROJECT_BRIEF.md:37-44`).

The current 5-axis table says protocol conversion is at 15% with OpenAI client adapter 0 and HCSF canonical types still shallow, while async tasks are 0% (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:89-95`). The same doc marks L0 commercial blockers and L1 adapter/DLQ items: OAuth bootstrap, API key issuance, pricing/payment/admin UI for L0; OpenAI client adapter, DLQ/orphan sweep, and fallback chain for L1 (`docs/02_HUAKAI_FUSION_ARCHITECTURE.md:116-135`).

The immediate local PASR gap is concrete and testable: OpenAI finalization already calls `ObserveByAccountWithPrefix(0, cached_tokens, tenant, account, prefix)` (`backend/internal/proto/openai_sse.go:395-414`) and maps `cached_tokens` to `CacheReadInputTokens` (`backend/internal/proto/openai_sse.go:432-440`), but PASR feedback only sets `HasCacheBitmap` when `CacheCreation > 0` and only refreshes read state on `CacheRead > 0` (`backend/internal/pool/pasr_feedback.go:80-96`). Gemini carries account/prefix/tenant fields and parses `cachedContentTokenCount` (`backend/internal/proto/gemini_sse.go:46-55`, `backend/internal/proto/gemini_sse.go:90-95`, `backend/internal/proto/gemini_sse.go:319-337`) but does not notify `cachemetrics` at stream finalization (`backend/internal/proto/gemini_sse.go:307-316`). The public observer entry returns before notifying observers on `0/0`, so the A3 miss-demote path is reachable only through direct private tests, not through real adapter calls (`backend/internal/cachemetrics/cachemetrics.go:226-240`; direct tests are in `backend/internal/pool/pasr_feedback_test.go:175-325`).

Upstream correction: HUAKAI must not claim "PASR first-mover." LiteLLM has prompt-cache locality routing around a cacheable-prefix store and pre-call deployment filter (`BerriAI/litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py:31-220`, `BerriAI/litellm@b5d3a5fc:litellm/router_utils/pre_call_checks/prompt_caching_deployment_check.py:23-100`). new-api has a channel-affinity layer around request-derived affinity values (`Calcium-Ion/new-api@d146e45e:service/channel_affinity.go:1-966`, `Calcium-Ion/new-api@d146e45e:middleware/distributor.go:1-435`, `Calcium-Ion/new-api@d146e45e:setting/operation_setting/channel_affinity_setting.go:1-121`). LiteLLM also has a mid-stream fallback pattern with usage merge and continuation flow (`BerriAI/litellm@b5d3a5fc:litellm/router.py:2032-2194`, `BerriAI/litellm@b5d3a5fc:litellm/streaming_handler.py:2268-2328`, `BerriAI/litellm@b5d3a5fc:litellm/exceptions.py:943`). Helicone's mature async log chain is evidenced by one hot-path queue send, 14 wired cold-path handlers, DLQ, priority lanes, and 15-minute timeout (`Helicone/helicone@3f4bd44b:worker/src/lib/dbLogger/DBLoggable.ts:1032`, `Helicone/helicone@3f4bd44b:valhalla/jawn/src/managers/LogManager.ts:71-230`, `Helicone/helicone@3f4bd44b:valhalla/jawn/src/lib/clients/sqsConsumers/sqsConsumers.ts:112-216`).

## 1. 推荐选项 + 理由

推荐 **C. PASR 多 vendor 接入修复** 作为下一步 commit。原因很直接：A1/A2/A3 今天已经把 PASR 做成 cache-aware，但当前有效路径仍偏 Anthropic；OpenAI 有 read signal 却不能置位 PASR locality，Gemini 有 parsed usage 却没有 observer 接入，0/0 miss-demote 公共路径又被提前 return 挡住。C 是把已经写下的算法变成真实多 vendor 行为，而不是继续堆 Anthropic-only 层。

C 也比 B 更适合作为"下一 commit"。B 协议转换主攻符合项目 North Star，但 OpenAI/Gemini client adapter 0 行意味着它是一整个 vertical slice；C 是 B 的低风险前置补丁：让 proto parser、cachemetrics、PASR observer 的信号契约先可信，之后再做 client adapter 时不会把 cache-aware 路由建在假数据上。C 同时避免 F/E/D 那种 auth-core、billing/async、mid-stream continuation 的高 blast radius。

差异化口径必须改成"升级 delta"而不是"首创"：HUAKAI 的价值不是简单有 cache locality，因为 LiteLLM 和 new-api 已有相邻能力；HUAKAI 的下一步 delta 是把 Anthropic/OpenAI/Gemini 的 usage signal 统一接入 tenant-scoped PASR segment，并让 locality 与 headroom、miss-demote 共同影响选择。这个实现只改 HUAKAI 本地 observer/parser/test，不读也不复制上游实现，clean-room 风险最低。

## 2. 候选评分

| Option | 商业价值 | 技术风险可控性 | 1 周落地概率 | PASR 协同性 | clean-room 风险可控性 | Verdict |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| A. PASR A4 继续加深 Anthropic | 3 | 4 | 4 | 4 | 5 | 可做但边际收益下降；继续单路径会掩盖 OpenAI/Gemini 接入缺口。 |
| B. 协议转换 axis 主攻 | 5 | 2 | 2 | 3 | 4 | 战略正确但下一 commit 太大；应在 C 之后启动 L1 vertical slice。 |
| C. PASR 多 vendor 接入修复 | 4 | 4 | 5 | 5 | 5 | **推荐**；直接修今天已知缺口，最能保护 A1/A2/A3 投资。 |
| D. R5/R7/R8 流式稳定层立项 | 4 | 2 | 2 | 2 | 4 | 高价值但复杂；LiteLLM mid-stream fallback 是强参照，需单独 spec/reviewer lane。 |
| E. 异步任务 axis 主攻 | 4 | 2 | 2 | 2 | 3 | 重要但会触碰 Tx2/DLQ 设计；`settler.go` 当前明确把 DLQ/outbox retry deferred（`backend/internal/billing/settler.go:78-83`）。 |
| F. sub2api OAuth 套利核心 | 5 | 1 | 1 | 1 | 2 | 商业价值最高之一，但涉及 auth core、真实凭证、mimicry/legal/audit；需要 Owner 高风险授权和独立源码 lane。 |

## 3. 次选项 + 何时切换

次选项是 **B. 协议转换 axis 主攻**，但只在 C 的验收标准通过后切换。切换条件：

- C 在 Day 3 前完成 OpenAI/Gemini/cachemetrics/PASR 单元测试，且 `go test` targeted suites 绿色。
- Owner 接受"read-hit-only provider 可以设置 PASR has-cache bit"的语义；如果 Owner 不接受，则 C 降级为 metrics-only 修补，B 延后到 adapter design 重新定义 cache signal。
- C 的 cross-review 无 HIGH；MED 可在 commit body 中记录或当场修复。

若 C 暴露 billing/audit 数据损坏风险，则临时切到 E 的最小 DLQ/outbox plan；若 Owner 明确把"立刻商业化拿 token"置为最高优先级并批准 auth-core/real-credential/legal 风险，则 F 可压过 B，但不能由当前 Codex lane直接实现。

## 4. 具体 1 周 Sprint 计划

| Day | Task | Output |
| --- | --- | --- |
| Day 1 | 锁定 cache signal 语义并先写红测试：OpenAI `CacheRead > 0` 能让 segment member 获得 locality；`ObserveByAccountWithPrefix(0,0,...)` 必须通知 observer；tenant/prefix/account 为空时 no-op。 | Red tests in `backend/internal/cachemetrics`, `backend/internal/pool`, `backend/internal/proto`. |
| Day 2 | 实现最小 observer 修复：`0/0` 不再挡住 observer；负数仍丢弃；PASR feedback 在 `CacheRead > 0` 时也可 `MarkCacheSeen`，因为 read hit 本身证明该 account 对该 prefix 有可用 cache。 | Cachemetrics + PASR feedback patch, no schema change. |
| Day 3 | OpenAI path 补验收：fixture 包含 `usage.prompt_tokens_details.cached_tokens`; finalization 触发 observer；read-only signal 更新 `HasCacheBitmap`、`LastReadAt`、reset miss。 | OpenAI SSE/PASR e2e unit tests. |
| Day 4 | Gemini path 补验收：`cachedContentTokenCount` 映射到 `CanonicalUsage.CacheReadInputTokens`; stream finalization 调 `ObserveByAccountWithPrefix`; tenant/account/prefix 注入不完整时不污染 segment。 | Gemini SSE/PASR e2e unit tests. |
| Day 5 | 回归与文档：跑 targeted tests，再跑 `go test ./backend/internal/...`; 更新必要 docs 注释/plan follow-up；stage 后运行 `codex exec review --uncommitted --full-auto`。 | Green tests + review report; fix HIGH/MED. |
| Day 6 | Buffer：处理 review findings、race/flaky、observer global-state test isolation。若 Day 1-5 全绿，开始 B 的 adapter L1 mini-plan但不混进本 commit。 | Stabilized C commit candidate. |
| Day 7 | Final cross-check：确认没有 schema/dependency/auth/billing/quota high-risk edits；准备 Owner summary and commit notes. | Merge-ready C patch. |

## 5. 失败模式 + 检测信号

| Failure mode | Why it matters | Detection signal | Mitigation |
| --- | --- | --- | --- |
| Read-hit overtrust: `CacheRead > 0` is not tied to the prefix HUAKAI thinks it routed. | Could route future traffic to an account that does not actually hold that prefix. | Unit tests require exact tenant+prefix+account match; later real provider smoke must compare repeated same-prefix hit rate. | Mark only when `PrefixHash`, `TenantID`, and `AccountID` are all present; keep real-provider smoke as pre-productization gate. |
| 0/0 observer flood demotes good accounts for non-cacheable prompts. | A3 could become negative routing pressure. | `MissObsTotal` spikes without corresponding cacheable prefix; tests cover empty prefix/tenant/account no-op. | Only finalizers with known prefix call `WithPrefix`; no prefix means no PASR segment update. |
| Gemini `cachedContentTokenCount` semantics differ from OpenAI/Anthropic read-hit semantics. | Could mix explicit cache resource hits with implicit cache hits incorrectly. | Gemini tests label it as read signal only; no billing/pricing behavior depends on it in C. | Treat as PASR locality hint, not billing source; require real Gemini smoke before cost claims. |
| Cross-tenant prefix collision. | Severe isolation bug. | Test same prefix under two tenants; only matching tenant segment changes. | Preserve `SegmentTable.Lookup(tenantID, prefix)` and no tenantID=0 segment writes. |
| Duplicate global observers cause flaky tests. | Cachemetrics observers are process-global. | Targeted tests fail only in package-order/full-suite runs. | Add/reset test helper if needed; otherwise keep observer tests serial and narrowly scoped. |
| Scope creep into DB schema or billing ledger. | Would trigger high-risk confirmation and slow sprint. | `git diff --stat` includes migrations, `billing/`, auth core, or generated sqlc. | Stop and ask Owner if such files are required; C should not need them. |
| Clean-room leakage from upstream implementation details. | Could contaminate MIT clean-room posture. | Review finds code/comment naming copied from reference projects. | Use only local HUAKAI contracts and prior paraphrased reports; no upstream source read in implementation lane. |

## 6. 测试策略 + 验收标准

Targeted test suites:

- `go test ./backend/internal/cachemetrics`
- `go test ./backend/internal/pool`
- `go test ./backend/internal/proto`
- Then `go test ./backend/internal/...`

Acceptance tests:

1. OpenAI SSE with `cached_tokens > 0` and `(tenantID, accountID, prefixHash)` updates PASR segment `HasCacheBitmap`, `LastReadAt`, and miss reset through public observer path.
2. Gemini SSE with `cachedContentTokenCount > 0` maps to canonical cache read tokens and notifies PASR through public observer path.
3. `ObserveByAccountWithPrefix(0,0,tenant,account,prefix)` reaches PASR feedback and demotes after two consecutive misses, matching `PASRDemoteThreshold` (`backend/internal/pool/prefix_segment.go:89-105`).
4. Empty tenant/account/prefix and negative cache counters do not update any segment and do not panic.
5. Same prefix in two tenants cannot update the other tenant's segment.
6. No new runtime dependency, no migration, no auth-core, no billing-ledger, no quota-enforcement changes.

Commit gate:

- Stage changes.
- Run `codex exec review --uncommitted --full-auto`.
- HIGH findings block commit; MED must be fixed or explicitly recorded.

## 7. 依赖 / 阻塞 / Owner 授权点

Owner authorization needed before execution:

- Approve C as next commit.
- Approve the semantic rule: **for read-hit-only providers, `CacheRead > 0` may set PASR has-cache/locality bit**. My recommendation is yes, because a read hit proves the chosen account had usable cache for that observed prefix, while A3 miss-demote can later correct stale bits.

No Owner approval needed if the implementation stays within:

- parser/observer/PASR feedback tests,
- comments/docs,
- no DB schema,
- no auth/billing/quota core,
- no dependency,
- no real credentials.

Owner approval required if discovered necessary:

- DB migration for provider/cache-scope dimensions,
- real provider smoke using paid accounts/budget,
- auth bootstrap / OAuth refresh / mimicry behavior,
- billing ledger or DLQ/outbox mutation,
- any production secret or deployment change.

Open questions:

1. Should PASR distinguish `seen_by_creation` vs `seen_by_read` in memory later? C can use one bitmap now; a future A4 could split flags if field evidence shows different decay behavior.
2. Should Gemini explicit cache resource hits and implicit cache hits become separate metrics? C should not decide billing semantics.
3. Should OpenAI project-level cache isolation be modeled as `cache_scope_id` before real smoke? C can proceed without schema, but Fabric/product claims cannot.
4. Should miss demote count only after a prior has-cache bit? Current A3 direct logic records miss for a member; C should avoid demoting non-marked accounts into noisy metrics.
5. Do we need a cache observation reset hook for tests? If global observer leakage appears, add test-only helper or package-local cleanup.

## 8. Fusion-Upgrade 三维 Delta

| Feature | Upstream pattern cite | HUAKAI delta | Dimension(s) | C sprint action |
| --- | --- | --- | --- | --- |
| Prompt-cache locality substrate | LiteLLM has a cacheable-prefix store + pre-call deployment filter (`BerriAI/litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py:31-220`, `BerriAI/litellm@b5d3a5fc:litellm/router_utils/pre_call_checks/prompt_caching_deployment_check.py:23-100`); new-api has channel affinity keyed by request-derived affinity values (`Calcium-Ion/new-api@d146e45e:service/channel_affinity.go:1-966`, `Calcium-Ion/new-api@d146e45e:middleware/distributor.go:1-435`). | HUAKAI routes vendor usage signals through a vendor-neutral observer into tenant-scoped PASR segments instead of treating locality as a single hard pin. Segment table already stores K=3 members, bitmap, last read/write, TTL, and miss counters (`backend/internal/pool/prefix_segment.go:48-99`). | 架构升级 + 生态升级 | Wire OpenAI/Gemini signals into the same observer path as Anthropic; keep tenant isolation. |
| Locality + capacity selection | Similar upstream locality/affinity patterns cited above. | HUAKAI selection score blends locality with headroom, so a hot but overloaded account is not the only routing signal (`backend/internal/pool/pasr_selector.go:209-240`). | 算法升级 | Ensure read-hit-only vendors can feed the locality side of the score. |
| Stale-cache correction | Similar upstream locality/affinity patterns cited above. | HUAKAI A3 tracks consecutive miss observations and demotes stale has-cache bits at threshold 2 (`backend/internal/pool/prefix_segment.go:89-105`, `backend/internal/pool/pasr_feedback.go:97-105`). | 算法升级 + 生态升级 | Fix public `0/0` observer path so miss-demote is not dead code. |
| Protocol breadth runway | Project North Star says provider/API/model breadth is a first-class L1/L2 concern (`docs/01_PROJECT_BRIEF.md:11-18`). | C does not finish adapters, but it turns OpenAI/Gemini cache telemetry into PASR-compatible signals, which is the narrow substrate B needs. | 架构升级 + 生态升级 | After C green, switch to B L1 adapter vertical slice. |
| Async reliability reference, deferred | Helicone's source-read report shows hot-path one queue send, 14 wired cold-path handlers, DLQ, priority lanes, and timeout (`Helicone/helicone@3f4bd44b:worker/src/lib/dbLogger/DBLoggable.ts:1032`, `Helicone/helicone@3f4bd44b:valhalla/jawn/src/managers/LogManager.ts:71-230`, `Helicone/helicone@3f4bd44b:valhalla/jawn/src/lib/clients/sqsConsumers/sqsConsumers.ts:112-216`). | HUAKAI should eventually use an outbox/DLQ worker for settlement durability, but that is a separate E sprint because it touches billing reliability and release gates. | 架构升级 + 生态升级 | Do not mix into C; record as next risk-driven plan if tests expose data-loss concern. |
| Streaming stability reference, deferred | LiteLLM has mid-stream fallback continuation and usage merge reference behavior (`BerriAI/litellm@b5d3a5fc:litellm/router.py:2032-2194`, `BerriAI/litellm@b5d3a5fc:litellm/streaming_handler.py:2268-2328`, `BerriAI/litellm@b5d3a5fc:litellm/exceptions.py:943`). | HUAKAI R5/R7/R8 should define a clean-room stream-stability layer, but that requires a dedicated spec because it changes retry boundaries and partial usage settlement. | 算法升级 + 架构升级 | Do not mix into C; schedule after protocol adapter baseline or when stream failures dominate acceptance tests. |

## 9. Final Recommendation

Next commit should be **C: PASR multi-vendor signal repair**.

Definition of done: OpenAI read-only cache hits, Gemini cached-content tokens, and 0/0 miss observations all affect PASR through the public observer path with tenant isolation and tests. This is the smallest commit that converts today's PASR cache-aware work from Anthropic-local into a multi-vendor substrate, while preserving the option to move next into B (protocol conversion) without rebuilding the cache signal contract.

Owner summary: 本计划的真实观察是 C 的缺口已经在 HUAKAI 本地代码中可定位、可测试、可一周内修；合理推断是 C 最能保护今天 A1/A2/A3 的投入，并为 B 协议转换主攻铺路。没有建议功能缩水；没有新增 clean-room 源码读取；主要安全风险是避免跨租户 prefix 污染和避免真实凭证/账务范围外溢。需要 Owner 确认的是下一 commit 选择 C，以及 read-hit-only provider 是否允许设置 PASR has-cache/locality bit。
