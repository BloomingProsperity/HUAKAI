# Next Pivot — Claude Lane Independent Plan

> **Lane**: planner (independent draft, written without reading codex lane)
> **Author**: Claude PM-Orchestrator
> **Agent**: general-purpose (sub-agent, ab95de1152a804371)
> **UTC**: 2026-05-09T~12:00Z
> **Branch**: claude/phase-1
> **Per CLAUDE.md #10 Parallel-draft rule**: codex lane drafts independently; cross-discuss after both written.

---

## TL;DR

**推荐 C（PASR 多 vendor 接入修复）作为下一周主线 commit 流。**
次推 **D（R5/R7/R8 流式稳定层立项）** 与 C 并行排队，第二个 commit 起 `phase-1.5/streaming-stability` 子分支。
**反对当前周做 B（协议转换 axis 主攻）和 F（OAuth 套利核心）**——理由见 §2.

---

## 1. 推荐选项 + 理由

### 推荐：选项 C — PASR 多 vendor 接入修复

**核心论点 1：A1/A2/A3 是 Anthropic-only 投资，C 是它的"激活键"。**

今天三连 commit 落的是 PASR cache-aware ranking + miss demote。但实际生产流量里 OpenAI / Gemini 路径上 PASR observer 永远收不到信号——A1/A2/A3 在这两个 vendor 上等于装饰品。

证据：
- `backend/internal/pool/pasr_feedback.go:80-104` 三 case 分支：`CacheCreation > 0 → MarkCacheSeen + MarkRead`、`CacheRead > 0 → MarkRead only (no MarkCacheSeen)`、`0/0 → RecordMiss → demote@2`
- OpenAI 永远走 `CacheRead > 0`-only 分支（OpenAI 协议无 cache_creation 概念，只有 cached_tokens read）。**HasCacheBitmap 永不被设**
- 结果：A2 ranking 里 locality 信号对 OpenAI 全为 0，A3 demote 死路径只有 Anthropic 触发
- Gemini 更糟：`backend/internal/proto/gemini_sse.go:46-50, 319-326` `CachedContentTokens` 字段已 carry-over 但 finalize 路径没有任何 `cachemetrics.Observe*` 调用——观察 hook 完全不存在。Gemini 上 PASR 等于关闭

修复成本极低（< 200 LoC），ROI 极高（投资乘数 = vendor 数 = 3）。

**核心论点 2：0/0 miss demote 死代码是已知正确性 bug。**

`backend/internal/cachemetrics/cachemetrics.go:226-240` `ObserveByAccountWithPrefix` 有早 return：

```go
if cacheCreation == 0 && cacheRead == 0 {
    return  // ← 0/0 case 永远到不了 notifyObservers
}
```

但 `pasr_feedback.go:97-105` 的 default 分支正是为 0/0 miss demote 写的。**两段代码逻辑互相否决**——A3 cache-miss demote 在标准 miss 场景（vendor 既没建 cache 也没命中 cache）下永远不触发。这是今天三连 commit 没补的洞，必须立刻修。

**核心论点 3：clean-room 风险最低。**

C 全程改 HUAKAI 自己的代码（observation 路径接入 + 死路径修复），无需读非 MIT 源码。LiteLLM `prompt_caching_cache.py` 已在 `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md` 里 specifier-lane 总结过；本周不需重读，paraphrased behavior 直接用。

---

### 次推：选项 D — R5/R7/R8 流式稳定层立项（并行排队）

**何时切换**：C-Day3 完成且 cross-review 通过后，第二条 commit 流并行起步。理由：

- D 主攻面是 LiteLLM `MidStreamFallbackError` 模式 paraphrased — `litellm/router.py:2052-2194` + `streaming_handler.py:2268-2328`，是"对客户感知最强的稳定层"（流式中断时不让用户重发）
- D 与 C 完全独立（C 触动 cachemetrics + pasr_feedback + sse adapter；D 触动 forwarder + retry chain）— 可平行排队
- D 是 Owner memory `feedback_stability_means_stronger.md` 明确点名的 R5/R7/R8 强伪装层之一，是 HUAKAI vs sub2api 的核心差异，必做

但 D 实现成本高（流式 partial chunk reconstruct + continuation prompt + cross-vendor prefix:True 处理），1 周内只能立项 + L0 spec + skeleton，做不完 L1 实现。所以排第二。

---

### 反对当前周做的选项

- **B（协议转换 axis 主攻）**：OpenAI/Gemini adapter L1 0 行属实，但单 axis 突进会再次出现"PASR-only 投资"那种 vendor-isolation 问题——adapter 写完发现 forwarder 没接、cachemetrics 没接、settler 没分摊。**应该等 C 把多 vendor PASR 通了之后再开 B**，否则会重做接线。
- **F（OAuth 套利核心）**：L0 商业化阻塞属实，但是 `feedback_execution_boundary_c.md` 已记录 "Anthropic 账号反转 / R3 / fingerprint 实施全暂停"。OAuth 套利触碰这条红线的概率 > 50%。需要 Owner 显式解锁后才能开。
- **A（PASR A4 继续 Anthropic 路径）**：边际收益递减。A1/A2/A3 已经把 Anthropic 路径的 cache-aware ranking 主结构落定，A4 加深前应该先把 OpenAI/Gemini 接通，否则就是在一棵树上加叶子。
- **E（异步任务 axis 主攻）**：settler DLQ 是真缺口，但 1 周不够做完。Helicone 14-handler chain 是 spec 参考但实现成本远超 1 周。降级到 roadmap，不入本周 sprint。

---

## 2. 1 周 sprint 计划（按天列任务）

主线分支 `claude/phase-1`，单条 commit 流。

### Day 1（周日 5-10）C-1：OpenAI cache observation 接 PASR observer
- **commit 标题**: `pasr/openai: cache-read 也调 MarkCacheSeen + segment hasCache 修复`
- **改动**:
  - `backend/internal/pool/pasr_feedback.go:80-104`: 把 `case obs.CacheRead > 0` 分支也调 `seg.MarkCacheSeen(idx)`（行为对齐：OpenAI 没有 creation 概念但客户端实际命中过 cache，应当占位 hasCache bit）
  - 加一个新指标 `IncCacheReadObsAsCreation()` 区分 read-as-seen 与真 creation
  - 单测：构造 OpenAI-only observation 流（只 read，无 creation），断言 segment.HasCacheBitmap 被设
- **验收**: `go test ./backend/internal/pool/... -run TestPASRFeedback -v` 全绿；新增 read-as-seen 单测覆盖 OpenAI 路径

### Day 2（周一 5-11）C-2：Gemini observation hook 接入
- **commit 标题**: `proto/gemini: finalize 路径接 cachemetrics.ObserveByAccountWithPrefix`
- **改动**:
  - `backend/internal/proto/gemini_sse.go:300-317`: finalize Gemini state 时调 `cachemetrics.ObserveByAccountWithPrefix(0, int64(state.CachedContentTokens), state.TenantID, state.AccountID, state.PrefixHash)`（与 OpenAI 路径行为对齐，line 414）
  - 消除 `gemini_sse.go:46-53` 注释里的 "future" 占位
  - 单测：构造 Gemini SSE stream with `cachedContentTokenCount > 0`, 断言 PASR observer 被回调
- **验收**: `go test ./backend/internal/proto/... -run TestGeminiSSE -v` 全绿；observer 回调断言

### Day 3（周二 5-12）C-3：0/0 miss demote 死路径修复
- **commit 标题**: `cachemetrics: ObserveByAccountWithPrefix 不再吃 0/0 + miss demote 路径打通`
- **改动**:
  - `backend/internal/cachemetrics/cachemetrics.go:226-240`: 移除 `cacheCreation == 0 && cacheRead == 0 → return` 的 short-circuit；把 0/0 case 也下发到 observer
  - 但保留对 negative 值的拒绝（`cacheCreation < 0 || cacheRead < 0`）
  - 在 expvar 里加 `cache_observation_zero_zero_total` 计数器，区分真零 vs 真 miss
  - 单测：构造 0/0 observation 三次，断言 PASR demote 在 MissCount=2 触发
- **验收**: 新单测 `TestPASRFeedback_ZeroZeroMissDemote` 绿；A3 阈值 2 端到端可触发

### Day 4（周三 5-13）C-4：Anthropic 路径回归 + 跨 vendor 集成测试
- **commit 标题**: `pasr: 三 vendor cache observation 端到端集成测试`
- **改动**:
  - 新增 `backend/internal/pool/pasr_multivendor_integration_test.go`: 同一 prefixHash 在 anthropic/openai/gemini 三 vendor segment 下的 hasCache + miss demote 行为对比
  - 跑 `go test -race -count=3` 验证并发场景下 segment 字段不会被三 vendor observation 互相踩
- **验收**: 集成测试绿；race detector 静；老的 A1/A2/A3 单测全绿不退化

### Day 5（周四 5-14）C-5：cross-review + 文档更新
- **commit 标题**: `docs/pasr: 多 vendor cache observation 接入说明 + 风险登记`
- **改动**:
  - 更新 `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` 第 5 复杂度轴 2 状态从 60% → 70%
  - 更新 `docs/templates/codex-reviewer.md` 增加 multi-vendor PASR 验证清单
  - 跑 `/cross-review` 走 codex reviewer-lane 验证 C-1..C-4 切片
  - 跑 per-commit `codex exec review --uncommitted --full-auto`（per CLAUDE.md #8 dispatch 末尾必须 `< /dev/null`）
- **验收**: codex reviewer ACCEPT；docs PR 单独走 cross-review

### Day 6-7（周五-周六 5-15/16）D-0：R5/R7/R8 立项 spec
- **commit 标题**: `docs/spec: R5/R7/R8 流式稳定层 L0 设计 + L1 skeleton`
- **改动**:
  - 写 `docs/specs/F-STREAM-001-mid-stream-fallback.md`：基于 LiteLLM `MidStreamFallbackError` 行为 summary（已在三份 source-read 报告里 paraphrased，本周不重读源码）
  - 写 `backend/internal/forwarder/streaming_fallback.go` skeleton（空 interface + TODO 注释，不实现）
  - 标注 Anthropic-specific `prefix: True` 的 cross-vendor 难点 + Owner 决策点："是否限定 R5 仅 Anthropic-streaming-→-Anthropic-streaming 同 vendor 内 fallback；跨 vendor 推后到 R7"
- **验收**: spec 通过 codex spec-reviewer lane 检查；skeleton 编译通过但未启用

---

## 3. 失败模式 + 检测信号

| 失败模式 | 检测信号 | 缓解 |
|---|---|---|
| C-1 改动后 Anthropic 路径回归（MarkCacheSeen 被多余触发） | `TestPASRFeedback_AnthropicCreation` 单测红 + locality 评分异常上扬 | C-3 集成测试覆盖；rollback 改回原 case 分支 |
| C-2 Gemini observation 触发频率过高，PASR segment 表内存涨 | `pasr_segment_count` expvar 周环比 > 30% | 加 sample rate flag (1/N)，默认 1（全采样），异常时降到 1/10 |
| C-3 0/0 case 放行后，PASR observer 在零流量场景下被噪声 demote | demote 速率/请求量比值 > 5%（基线 < 1%） | 加最小观察窗口 guard (e.g. ObservationWindow < 30s skip) |
| C-4 race detector 跑出 segment 字段竞争 | `go test -race` 红 | sync.Mutex per-segment 升级（已在 segment 结构里有 atomic 字段，需补） |
| 1 周内 cross-review REJECT | codex reviewer 输出 REJECT verdict | 不进 commit；surface 到 Owner（per CLAUDE.md required workflow #7） |

---

## 4. 测试策略 + 验收标准

### 单测层
- 每 commit 对应 ≥ 1 个新单测；覆盖 OpenAI/Gemini/Anthropic 三 vendor 的 happy path + edge case (0/0, negative usage, missing PrefixHash, missing TenantID)

### 集成测试层
- `pasr_multivendor_integration_test.go`：构造 mock SSE → 完整 finalize → cachemetrics → pasr_feedback → segment 状态 → ranking 输入；三 vendor 路径独立断言但共享 segment 表，验证 cross-vendor 不互踩

### 端到端验收
- 跑 `make test` （或等价命令）全绿
- 跑 `go test -race -count=3 ./backend/internal/pool/... ./backend/internal/cachemetrics/... ./backend/internal/proto/...` 全绿
- expvar 在测试环境暴露 multi-vendor counter 后，肉眼检查 anthropic/openai/gemini 三 vendor 的 cache_creation_obs / cache_hit_obs / miss_obs / demote 计数都非零

### Owner 验收信号（C-Day5 完成后）
- multi-vendor cache observation 三轴均非零（之前 OpenAI demote 计数 = 0，Gemini 全部计数 = 0）
- A1 aging worker 对 OpenAI/Gemini segment 也开始 ExtendedCacheTTL 续约（而不是立刻被 5min aging 清掉）
- A2 score-based ranking 在三 vendor 上都报 locality 非零信号

---

## 5. 依赖 / 阻塞（含 Owner 授权点）

### 自驱可做（无需 Owner 授权）
- C-1..C-5 全程改 HUAKAI 自己代码 + 接 expvar metric + 加 docs，per CLAUDE.md "Risk-Based Confirmation Rule" 属低风险（docs/tests/小重构/non-sensitive config）
- D-0 spec + skeleton 同样低风险
- 跑 `codex exec review --uncommitted --full-auto < /dev/null` per-commit Codex review（per CLAUDE.md #8 + memory `feedback_codex_exec_stdin_redirect.md`）

### Owner 授权点
- **D-1 实现期开始前**（下周）：Owner 确认 R5 是否限定 Anthropic-streaming-→-Anthropic-streaming 同 vendor，cross-vendor 推后。memory `feedback_execution_boundary_c.md` 已暗示 Anthropic 反转暂停，所以 R5 实施细节需 Owner 显式解锁
- **C-3 死路径修复**：移除 0/0 short-circuit 是行为变更，理论上属于"medium risk"，建议 Owner 在 commit message 看到 "removes early-return for 0/0 case" 时明确无异议（可异步 review，不阻塞 commit）

### 阻塞排查
- ❌ 不需要 sub2api/new-api/litellm 等非 MIT 源码——本会话先前三 lane 报告已 paraphrased，per CLAUDE.md #11 lane 隔离 + 时间隔离已满足
- ❌ 不需要 AWS 凭据（per memory `project_no_aws_credentials.md`）
- ❌ 不需要真实 vendor 账号——观察路径 mock SSE 即可（per memory `project_real_vendor_account_scope.md`）

---

## 6. fusion-upgrade 三维 delta（per CLAUDE.md #12）

### 架构升级（结构性差异）
- **多 vendor cache observation 统一管道**：HUAKAI 已在 `cachemetrics.ObserveByAccountWithPrefix` 抽象出 (cacheCreation, cacheRead, tenantID, accountID, prefixHash) 五元组接口，本周补足 OpenAI/Gemini 终端 hook，使 PASR observer 跨 vendor 一致
  - 对比 LiteLLM：locality 单 prefix → 单 deployment_id 硬绑（参考 `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md` §A1）
  - HUAKAI 升级：locality + headroom score 混合 + cross-account miss demote + cross-vendor 共享 segment 表
- **0/0 case 升级为一等公民**：upstream 三大 OSS gateway（one-api / Portkey / litellm）都把 0/0 当成 noop，HUAKAI 把它当成 miss 信号，是 demote 路径的关键输入

### 算法升级（行为差异）
- **read-as-seen 处理**（C-1 引入）：OpenAI 协议无 creation 概念时，cache_read > 0 视为 hasCache 等价信号，让 ranking score 信号在三 vendor 上对齐
  - 风险：read-as-seen 比真 creation 弱（因为 OpenAI 不通知"我新建了 cache"），可能虚高 hasCache bit。缓解：在 score 公式里对 read-as-seen 给 0.7 权重而非 1.0（C-Day4 集成测试时校准）
  - 对比 LiteLLM：硬绑只对 first-creation deployment 生效，后续 read 不更新 binding；HUAKAI 让 read 也续 segment，更激进

### 生态升级（接入面差异）
- **三 vendor 全管道一致性**：sub2api 在 OpenAI Responses 路径有 previous_response_id sticky（参考 `docs/research/2026-05-09-source-read-sub2api-newapi.md` §D.1），但 cache locality 完全没接 OpenAI/Gemini。HUAKAI 完成 C 后是 OSS 圈唯一三 vendor cache locality 全接的 gateway
- **expvar metric 三轴对齐**：C-3 增加 `cache_observation_zero_zero_total` 计数器，让运营看板能区分 "没流量" vs "流量但全 miss"——这是 sub2api/new-api/litellm 都没暴露的运维信号

---

## 7. 源码 cite

### HUAKAI 内部（已 verified）
- `backend/internal/proto/openai_sse.go:411-414` — OpenAI ObserveByAccountWithPrefix 已接入（creation=0, read=cached_tokens）
- `backend/internal/proto/openai_sse.go:395-417` — finalizeOpenAIState 终态触发 cachemetrics observation
- `backend/internal/proto/gemini_sse.go:46-53` — Gemini AccountID/PrefixHash 字段已加但注释 "终态触发点 future" 未接入
- `backend/internal/proto/gemini_sse.go:300-317` — finalizeGemini 路径，**当前无 cachemetrics 调用**（C-2 修复点）
- `backend/internal/proto/gemini_sse.go:319-326` — `updateGeminiUsage` 把 CachedContentTokens 累加到 state，但没下发 observer
- `backend/internal/pool/pasr_feedback.go:80-104` — 三 case 分支：CacheCreation>0 / CacheRead>0 / 0-0 default
- `backend/internal/cachemetrics/cachemetrics.go:226-240` — ObserveByAccountWithPrefix 0/0 short-circuit（C-3 修复点）
- `backend/internal/billing/settler.go:78-83` — DLQ 路径推迟到 Phase-4.5（per spec），E 选项的根证据

### 上游断言（per `docs/research/2026-05-09-*.md` 三份 lane 报告 already paraphrased）
- `BerriAI/litellm@b5d3a5fc:litellm/router_utils/prompt_caching_cache.py:31-220` — locality cache 类定义 + 300s TTL（参考 `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md` §A1）
- `BerriAI/litellm@b5d3a5fc:litellm/router_utils/pre_call_checks/prompt_caching_deployment_check.py:23-100` — 硬绑 deployment_id 的 pre-call filter
- `BerriAI/litellm@b5d3a5fc:litellm/router.py:2052-2194` — `_acompletion_streaming_iterator` MidStreamFallbackError 处理（D-0 spec 参考）
- `BerriAI/litellm@b5d3a5fc:litellm/router_utils/cooldown_handlers.py:303-320` — `router_cooldown_event_callback` 异步回调模式（PASR 未来扩展点）
- `Calcium-Ion/new-api@d146e45e:service/channel_affinity.go` — channel_affinity 已实现 prompt_cache_key + Anthropic metadata 路径硬绑（参考 `docs/research/2026-05-09-source-read-sub2api-newapi.md` §B.2）
- `Helicone/helicone@3f4bd44b:valhalla/jawn/src/managers/LogManager.ts:71-230` — 14-handler chain + 15min timeout + DLQ（E 选项立项参考，本周不做）
- `envoyproxy/ai-gateway@4d3eae8b:internal/controller/rotators/aws_oidc_rotator.go:1-120` — preRotationWindow 设计（HUAKAI credentials_rotation_state 表参考，未来 roadmap）
- `Wei-Shaw/sub2api@dbc8ae65:backend/internal/service/openai_account_scheduler.go` — sub2api 不做 cache locality；session-hash sticky 仅做 conversation-level affinity（HUAKAI 差异化基线）

---

## 8. 风险登记 + clean-room 审计

### 本周触发的 clean-room 检查点
- **C-1..C-3 全程不读非 MIT 源码**：行为参考全部来自三份已存在的 lane 报告（specifier lane 已完成 source-read，本周 planner lane 不重读）
- **D-0 spec 写作**：引用 LiteLLM `MidStreamFallbackError` 行为时，必须 paraphrase + 不 copy 函数名 / 字段名 / 注释。spec 文件头部加 lane=specifier-rephrase + 时间戳 + Source files read: list（per CLAUDE.md #11 clean-room prompt enforcement）
- **commit message 全部用中文**（per memory `feedback_chinese_comments.md`）

### 已知风险登记
| 风险 ID | 描述 | 等级 | 缓解 |
|---|---|---|---|
| RISK-PASR-OAI-1 | read-as-seen 让 OpenAI hasCache bit 虚高 | M | C-Day4 集成测试校准；score 公式给 0.7 权重 |
| RISK-PASR-GEM-1 | Gemini observation 接入后 segment 数量短期翻倍涨 | L | 限制 LRU eviction 上限 + 监控 segment_count expvar |
| RISK-PASR-MISS-1 | 0/0 放行可能放大噪声 demote | M | 加 ObservationWindow guard (30s) + demote rate alert |
| RISK-D-0-CR | LiteLLM streaming pattern paraphrase 风险 | M | spec 写作 lane 隔离 + 不读源码（用现有 lane 报告） + 必有 codex spec-reviewer lane 复核 |

---

## 9. 与 codex lane 草案的差异（待对比）

> 本节留空——待 codex lane 草案落盘后由 Owner 主持 cross-discussion 填写。
> 预期分歧点：
> - codex 可能选 B 或 E（adapter / DLQ 优先），理由可能是"PASR 已经够了，该开新 axis"
> - 我选 C 的理由是"PASR 投资 vendor 隔离修复 ROI 高于开新 axis"
> - 真正决策点：是把 PASR 在三 vendor 通了再开新 axis，还是先开新 axis 后回头补 PASR vendor 接入

---

## 10. 完成定义（DoD）

- [ ] Day 1-5 五个 commit 全部入主线 `claude/phase-1`
- [ ] 每个 commit per-commit codex review（CLAUDE.md #8）通过
- [ ] Day 5 切片整体走 `/cross-review` 通过
- [ ] D-0 spec + skeleton 入主线（不要求实现）
- [ ] `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` 复杂度轴 2 进度更新（60% → 70%）
- [ ] 风险登记 RISK-PASR-OAI-1 / GEM-1 / MISS-1 入 `docs/05_RISK_REGISTER.md`（如该文件存在；不存在则建立）
- [ ] 给 Owner 一份中文 sprint summary（C 完成 + D 立项）

---

**Lane**: planner (independent claude draft)
**Source files read in this lane**: 仅 HUAKAI 内部代码（cachemetrics / pool / proto / billing）+ 三份 docs/research/2026-05-09-*.md lane 报告。无任何非 MIT 源码读取。
**Agent**: general-purpose (sub-agent)
**UTC timestamp**: 2026-05-09T~12:00Z
