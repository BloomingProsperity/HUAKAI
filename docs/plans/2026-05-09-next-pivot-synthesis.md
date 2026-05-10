# Next Pivot — Claude × Codex 综合

**日期**: 2026-05-09
**前置 lanes**:
- `docs/plans/2026-05-09-next-pivot-claude.md`（sonnet 独立）
- `docs/plans/2026-05-09-next-pivot-codex.md`（codex xhigh+fast 独立）
**触发**: Owner 在 PASR A1/A2/A3 落今天主线后，问"下一步 commit 应该是什么"

## 综合 verdict

**两 lane 同推 Option C — PASR multi-vendor signal repair**。无分歧。可执行。

| Lane | 推荐 | 反对 |
|---|---|---|
| Sonnet | C | B 单 axis 突进会再现 vendor-isolation；F 触 `feedback_execution_boundary_c.md` 红线 |
| Codex | C | F 需 auth-core/legal 风险；E 涉 billing/release-gate；B 在 C 之后做 |

## 核心论证（两 lane 一致）

### A1/A2/A3 在 OpenAI/Gemini 路径上等于装饰品
- `pasr_feedback.go:80-96` 触发 `MarkCacheSeen` 的条件硬编码 `CacheCreation > 0`
- OpenAI 只暴露 `cached_tokens`（read），没有 creation 概念 → HasCacheBitmap 永不被设
- Gemini 已解析 `cachedContentTokenCount` 但完全没接入 `cachemetrics.ObserveByAccountWithPrefix` (`backend/internal/proto/gemini_sse.go:46-50, 319-325`)
- 结果：A2 score-based ranking + A3 miss demote 在两 vendor 路径上没数据可吃

### A3 cache-miss demote 当前是死代码
- `cachemetrics.go:226-240` 的 `0/0 → return` short-circuit 把 `pasr_feedback.go:97-105` default miss-demote 分支挡死
- A3 commit 已落但运行时不会被触发

### clean-room 风险最低
- 全部修改在 HUAKAI 自己代码内
- 不需要再读非 MIT 上游源码（Lane A/B/C 的 specifier 报告已够）

## 5 维评分对照（两 lane 取中位数）

| 候选 | 商业价值 | 技术风险 | 1 周可落 | PASR 协同 | Clean-room | 综合 |
|---|---|---|---|---|---|---|
| A. PASR A4 续 | 2 | 5 | 5 | 5 | 5 | 22 |
| B. OpenAI/Gemini adapter L1 | 4 | 2 | 2 | 3 | 4 | 15 |
| **C. PASR 多 vendor 接入修复** | **3** | **5** | **5** | **5** | **5** | **23** |
| D. R5/R7/R8 流式稳定层 | 4 | 3 | 3 | 4 | 4 | 18 |
| E. settler DLQ + outbox | 4 | 3 | 3 | 2 | 5 | 17 |
| F. sub2api OAuth 套利 | 5 | 1 | 1 | 1 | 2 | 10 |

C 总分最高且没有任何维度 < 3。

## 1 周 Sprint 计划（综合两 lane）

| Day | Task | Output | 来源 lane |
|---|---|---|---|
| Day 1 | 红测试 first：OpenAI `CacheRead > 0` 触发 segment locality；`ObserveByAccountWithPrefix(0,0,...)` 必须通知 observer；空 tenant/prefix/account 必须 no-op | `cachemetrics`、`pool`、`proto` 红测试 | codex |
| Day 2 | C-1: OpenAI cache-read → MarkCacheSeen + C-3: 0/0 short-circuit 移除 | `pasr_feedback.go` + `cachemetrics.go` patch | claude+codex |
| Day 3 | C-2: Gemini finalize → cachemetrics.ObserveByAccountWithPrefix wiring | `gemini_sse.go` patch + Gemini SSE/PASR e2e 单测 | claude+codex |
| Day 4 | 三 vendor 集成测试 + race；同 prefix 跨 tenant 隔离测试 | full integration suite green | claude+codex |
| Day 5 | 跑全包测试 `go test ./backend/internal/...`；codex review；修 HIGH/MED；准备 commit | review report + merge-ready patch | codex |
| Day 6-7 | Buffer：处理 review findings、observer global-state 测试 isolation；如全绿，开 D-0 R5/R7/R8 spec skeleton（基于 LiteLLM `MidStreamFallbackError` paraphrased） | C 已 commit；D-0 spec draft 起 | claude (D-0 提议) |

## Owner 待决策点

### Decision 1（codex 提出，claude 同意）: read-hit-only provider 是否可设 PASR has-cache bit?

- **背景**: OpenAI 只有 `cached_tokens`（read 信号），没有 `cache_creation`。当前 PASR 只在 creation > 0 时设 HasCacheBitmap。如果不变，OpenAI 永远进不了 PASR。
- **codex 推荐 yes**: read hit 本身证明该 account 对该 prefix 有可用 cache；A3 miss-demote 可纠正错误绑定
- **风险**: read-hit 可能跟实际路由的 prefix 不严格对应（OpenAI 的 prompt_cache_key 是软提示）
- **mitigation**: 仅当 `tenantID + accountID + prefixHash` 三全则更新；后续 vendor smoke test 进一步验证

**Auto mode 下默认按 yes 执行**——如要否决请截停。

### Decision 2: STALE 引用（one-api > 90 天）如何处理?

- one-api `pushed_at = 2026-01-09`，已超 #12 first-cite recency 90 天窗口
- 当前 cite 已加 STALE 警示注（per H1 fix）
- 选项: (a) 保留 STALE 注 + 在差异化表中保留行（依赖很弱）; (b) 移除 one-api 行依赖；(c) 在 GitHub 找到更活跃 fork 重新 fetch HEAD

**Auto mode 下默认 (a)**——如要 (b) 或 (c) 请告知。

## 失败模式 + 检测信号（codex 整理）

| 风险 | 检测 | mitigation |
|---|---|---|
| Read-hit overtrust（错误标 prefix→account） | 单测要求 (tenant, prefix, account) 三全；后续 smoke test 比对 same-prefix repeat hit rate | 三全才 mark；real provider smoke 是 productization gate |
| 0/0 observer flood demotes good accounts | `MissObsTotal` spike 但无对应 cacheable prefix | 仅有 prefix 的 finalizer 才 `WithPrefix`；空 prefix no-op |
| Gemini explicit cache 与 OpenAI/Anthropic 语义混淆 | Gemini 测试标 read-only signal | 不让 Gemini cache 进 billing source；real Gemini smoke 前不做成本主张 |
| 跨 tenant prefix 污染 | 同 prefix 不同 tenant 测 segment 隔离 | `SegmentTable.Lookup(tenantID, prefix)`；`tenantID=0` 不写 |
| Global observer test 间互相污染 | 全包测试 vs targeted 测试结果不一致 | reset helper 或 narrow scope serial 跑 |
| Scope creep 进 schema/auth/billing | `git diff --stat` 含 migrations / `billing/` / 生成 sqlc | 停下问 Owner，C 不应需要这些文件 |

## 测试策略 + 验收标准（综合）

Targeted suites（按顺序）:
1. `go test ./backend/internal/cachemetrics`
2. `go test ./backend/internal/pool`
3. `go test ./backend/internal/proto`
4. `go test ./backend/internal/...`

验收：
1. OpenAI SSE with `cached_tokens > 0` + 完整三 ID → 更新 PASR segment HasCacheBitmap / LastReadAt / reset miss
2. Gemini SSE with `cachedContentTokenCount > 0` → canonical cache read tokens → PASR observer 通知
3. `ObserveByAccountWithPrefix(0,0,tenant,account,prefix)` → PASR feedback → 连续两次 demote (matches `PASRDemoteThreshold`)
4. 空 tenant/account/prefix + 负数计数 → 无 segment 更新 + 无 panic
5. 同 prefix 跨 tenant → 不能更新对方 tenant segment
6. 无新依赖、无 migration、无 auth/billing/quota core 改动

## 三维 delta 分类（per CLAUDE.md #12，C 方案的升级 delta）

- **架构升级**: 把 vendor-neutral observer 接到 OpenAI/Gemini SSE → 让 PASR segment table 真成 multi-vendor 数据结构（之前是 Anthropic-only）
- **算法升级**: read-hit-only provider 的 PASR has-cache bit 语义；0/0 miss demote 在 OpenAI/Gemini 路径上激活
- **生态升级**: OpenAI/Gemini 路径的 cache 命中率 / miss demote 计数都进入 metrics（vendor 切片 expvar 已有，只是数据缺）

## 决策建议

1. **批准 C 作为下一 commit**（两 lane 同推，无分歧，5 维评分综合最高）
2. **批准 Decision 1: read-hit-only provider 可设 PASR has-cache bit**（auto mode 默认）
3. **STALE one-api 引用保留 + 注**（auto mode 默认）
4. **Day 6-7 buffer 用于 D-0 R5/R7/R8 spec draft**（不混入 C commit；以 LiteLLM `MidStreamFallbackError` paraphrased 为基线）

## Source 引用记录

- LiteLLM cache locality: `BerriAI/litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py`
- LiteLLM hard pin (no blend): `BerriAI/litellm@b5d3a5fc:litellm/router_utils/pre_call_checks/prompt_caching_deployment_check.py`
- LiteLLM MidStreamFallbackError: `BerriAI/litellm@b5d3a5fc:litellm/exceptions.py:943` + `litellm/router.py:2063+2209` + `litellm/litellm_core_utils/streaming_handler.py:2268-2328`
- new-api channel_affinity: `Calcium-Ion/new-api@d146e45e:service/channel_affinity.go`
- HUAKAI PASR selector: `backend/internal/pool/pasr_selector.go:209-240`
- HUAKAI segment table: `backend/internal/pool/prefix_segment.go:48-99`
- HUAKAI miss demote: `backend/internal/pool/prefix_segment.go:89-105`
- HUAKAI feedback path: `backend/internal/pool/pasr_feedback.go:80-105`
- HUAKAI cache observer: `backend/internal/cachemetrics/cachemetrics.go:226-240`
- HUAKAI Gemini SSE gap: `backend/internal/proto/gemini_sse.go:46-50, 90-95, 319-325, 328-334`

## Tail block (per AGENTS.md template)

Source files read: `docs/plans/2026-05-09-next-pivot-claude.md`, `docs/plans/2026-05-09-next-pivot-codex.md`, `docs/research/2026-05-09-source-read-*.md`, `backend/internal/pool/*`, `backend/internal/cachemetrics/*`, `backend/internal/proto/*` (HUAKAI internal — exempt per #12)
Lane: synthesizer
Agent: Claude opus-4-7 [1m]
UTC timestamp: 2026-05-09T08:30Z
