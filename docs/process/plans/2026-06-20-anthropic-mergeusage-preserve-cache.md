# 修复:Anthropic 流式 mergeUsage 整段替换抹掉 cache 字段 → 流式 cache 观测丢失

> 日期:2026-06-20 · 切片类型:核心逻辑 bug 修复(手挖 + 真码验证) · 基线 `feat/frontend-portal` @ b59abd6e
> 修复落点:`backend/internal/proto/anthropic/sse.go`(`proto/*` 不在 proxies-collision 写面,可自由修)

## 1. 缺陷(真码 + 测试已验证,非理论)

`proto/anthropic/sse.go:558` `mergeUsage(base, a, b)` 在 `a`(message_delta 顶层 usage)任一字段非零时执行 `base = a` **整段替换**,把 `base` 里 `a` 不携带的字段抹成 0。

触发链(全部用 HUAKAI 真码/fixture 核过):
1. `message_start`(:190)`state.AccumulatedUsage = env.Message.Usage`,含 `input/cache_read/cache_creation`。
2. Anthropic 的 message_delta 顶层 usage **只带 `output_tokens`**(input/cache 缺省=0)——HUAKAI 自己的 fixture `sse_test.go:57`/`:217` 即如此。
3. `message_delta`(:226)`mergeUsage` 命中 `a.OutputTokens != 0 → base = a`,`state.AccumulatedUsage` 的 `input/cache_read/cache_creation` 被抹零。
4. 消费者 `message_stop`(:241)与 `FinalizeUpstreamStream`(:275)**直接读** `state.AccumulatedUsage.CacheReadInputTokens`(已为 0)喂 `cachemetrics.ObserveByAccountWithPrefix`;`cachemetrics.go:227` 对 `(0,0)` 在 `notifyObservers` 前 short-circuit。

## 2. 后果分级(两条已自我证伪,只剩一条真)

- ❌ **不亏钱**:message_delta emit 的 usage 进 forwarder `UsageAccumulator.Update`(`forwarder_types.go:202` set-if-nonzero),input=0 被跳过、保留 message_start 值 → 计费不受影响。
- ❌ **不误 Demote**:`cachemetrics.go:227` 的 `(0,0)` short-circuit 挡在 `notifyObservers` 前,不会给 PASR 送 RecordMiss。
- ✅ **真损**:真命中缓存(cache_read>0)的**流式**请求,其 cache 命中在 message_stop 时读到 0 → 既不计入 `cache_token_count` hit-rate 指标,也不给 PASR 段 `MarkRead` 正反馈(`feedback.go:115` 确认 PASR 已 RegisterCacheObserver)。Anthropic 每条正常流都以 message_delta 收尾 → **常见路径**。等于在流式流量上饿掉 cache-aware 路由(核心省钱功能)的主要正反馈 + 运维 cache 观测失真。**零测试覆盖**。

严重度:**S2**(非直接 money/security,但常见路径劣化核心省钱功能 + 观测失真)。

## 3. 三家对照(#16,clean-room 意译)

HUAKAI 三处 usage 合并里只有 anthropic `mergeUsage` 整段替换;另两处 + 三家全是"保留/非零才覆盖":
- HUAKAI `forwarder_types.go:202` UsageAccumulator.Update —— 非零才覆盖 ✓
- HUAKAI `protosse/reconstruct.go:288` mergeNonZeroUsage —— 非零才覆盖 ✓
- HUAKAI `anthropic/sse.go:558` mergeUsage —— 整段替换 ✗(本 bug)
- `~/refs/sub2api` 网关用量旁路解析 —— v>0 才覆盖逐字段合并
- `~/refs/new-api` Claude 流式用量补丁 —— message_delta 缺字段用 message_start 回填
- `~/refs/CLIProxyAPI` Claude 用量累加器 —— 每字段 `.Exists()` 守才覆盖

结论:三家一致采用"保留"语义,正因 Anthropic message_delta 丢 input/cache;HUAKAI 此处是异类回归。

## 4. 修法(set-if-nonzero,对齐另两处 + 三家)

把 `mergeUsage` 的 `a` 整段替换改为逐字段非零叠加(与已有的 `b` 分支同语义),抽出 `overlayNonZeroUsage(dst, src)` 复用于 `a`、`b` 两层。token 7 字段(Input/Output/Total/CacheCreation/CacheRead/5m/1h)set-if-nonzero;不动 tool-call 计数(本函数本就不涉及)。`TotalTokens==0` 回填保持不变。

**blast radius 已核**:anthropic 客户端 emit message_delta 只序列化 `output_tokens`(`anthropic_messages_stream.go:175/186`),修复后客户端可见输出**不变**;计费**不变**;仅恢复 `state.AccumulatedUsage` 真实 cache 值供观测。可逆、纯修正。

## 5. 强测试(变异证 RED)

`sse_test.go` 新增:驱动 message_start(input=1000, cache_read=5000, cache_creation=200)→ content_block_delta → message_delta(只 output=50)→ 断言 message_delta 返回的 CanonicalEvent.Usage **同时**有 `CacheReadInputTokens==5000`、`CacheCreationInputTokens==200`、`OutputTokens==50`(直接测合并边界,不碰全局)。
- **变异判据**:把 `mergeUsage` 还原成 `base = a` 整段替换 → CacheRead 变 0 → 测试必 RED。
- 补充:end-to-end 经 RegisterCacheObserver 捕获 message_stop 观测值 == 5000(可选,验证 PASR 反馈链;用 -count=1 防全局缓存假绿)。

## 6. 成功标准 / blast radius / 风险

- 成功:build/vet 绿;新测试 GREEN 且变异 RED;`proto/...` + cmd/gateway 干净基线 `-count=1` fail 0;对抗审查零 S0/S1。
- blast radius:单文件 `anthropic/sse.go` + 其测试;无 schema/money/auth 改动;无新依赖。
- 风险:极低。唯一行为变化是 `state.AccumulatedUsage` 在 message_delta 后保留 cache(更正确);客户端 emit 与计费均已核实不变。
- 决策点:无需 Owner gate(非 money/schema/auth/deploy/默认翻转;纯 bug 修正)。安全网=变异测试 + 对抗审查 + 干净基线。

## 7. 后续(同源排查,本切片不做,记 follow-up)

- gemini/openai sse 的 usage 整段替换是否有同类抹零风险(初判无:二者每 chunk 带全量 usage,但交对抗 bug-hunt workflow wtmvos069 复核)。
