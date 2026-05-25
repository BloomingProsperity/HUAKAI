# 2026-05-08 HUAKAI Cache Hit-Rate Audit (sonnet lane)

> 检查 system_rewrite / mimicry_compose / forwarder / bedrock translator
> 是否破坏 vendor (Anthropic / Bedrock) prompt cache。

## 双 lane 结论分歧（重要）

| Lane | 视角 | 结论 |
|---|---|---|
| sonnet | "现在 forwarder 调用什么" | CLEAN — system_rewrite / mimicry_compose 当前没被 forwarder 调用 |
| codex | "如果启用了会怎样" | BLOCKING_HITS — mimicry_compose:270-280 strip 掉 system cache_control + TTL；system_rewrite ReplaceAll 模式覆盖原 markers |

**两 lane 都对** — codex 视角更严: 这些代码 path 是"如果走，缓存就破"。
codex audit 实测 `forwarder.go` 当前未调 `ApplyMimicryPlan` / `RewriteSystem`，与 sonnet 同步。

**Owner 决策点**: 后续如启用 mimicry / rewrite 强伪装层（execution_boundary_c memory rule 暂停），必须先修 mimicry_compose Step 2 让它**保留**而非 strip cache_control，否则 cache 全 miss。

## 结论: 现有 wiring CLEAN — 无缓存破坏

| 文件 | 状态 |
|---|---|
| `gateway/system_rewrite.go` | ✓ 保留 cache_control / citations / type=server_tool_use 等未知字段 |
| `gateway/mimicry_compose.go` | ✓ Step 2 stripSystemCacheControl 是设计中的 clean-slate（line 19 注），Step 3 ApplyBreakpointsWithTTLOrdering 强制 long-TTL-first 顺序符合 Anthropic 要求 |
| `gateway/forwarder.go` | ✓ 纯 transport 层无 prompt mutation |
| `bedrock/anthropic_request_translator.go` | ✓ 只剥 model+stream+注 anthropic_version；message/system/tools 零修改 |

无任何破坏点 — system prompt bytes 不改、messages 不重排、tools 不动、cache_control marker 不静默丢、无时间戳/request_id 注入到 prompt。

## 命中率 sticky 优化机会（sonnet 提议 3 个）

### 1. Account-pinned cache routing （HIGHEST IMPACT）
- 位置: `upstream_dispatcher.go:124-125` `TransportFactory.For(ProviderCode, mode)`
- 思路: 同 prompt 同 account 同 TTL 窗口路由到同一 RoundTripper
- 实施: `TransportFactory.For` 加可选 `accountID`；现有 `Dispatch.in.Account.AccountID` 可传

### 2. Prompt-hash bucketing （MID）
- 位置: ChatHandler 调 `mimicry.ApplyMimicryPlan` 之前
- 思路: 计算 `{system, user content, tools}` 哈希；同 hash 同 account 走 1h TTL 内同 binding
- 实施: 新 `PromptCohesionRouter` interface, lock routing decision before step 1

### 3. Account-level cache-control cap pooling （LOW-MID）
- 位置: `cache_control_apply.go:82` 每请求各算 cap
- 思路: account 维 5min 滑窗剩余 cap，跨请求复用
- 实施: 共享 mutable state (in-process LRU per account)

## 已实施的命中率指标 (Track D)

- `backend/internal/cachemetrics/`: expvar.Map "cache_token_count" 三计数 (creation_total / read_total / request_count)
- AnthropicAdapter (含 Bedrock-on-Anthropic 通过 A4 delegate) 在 message_stop / FinalizeUpstreamStream 调 cachemetrics.Observe
- `/debug/vars` 暴露; 命中率 = read_total / (creation_total + read_total)

## 后续 sticky 实施建议序列

按 ROI:
1. 先开 D （已实施）→ 看真命中率基线数据
2. 看数据决定是否做 Sticky #1 (Account-pinned routing) — sonnet 评 HIGHEST
3. #2 prompt-hash bucketing 看流量模式有无 conversation cohesion
4. #3 cap pooling 高 impact 不显著, 暂搁置

Lane: claude (synthesis)
Time: 2026-05-08T<UTC>
