# Vendor Documentation Drift Audit — 2026-05-06

Purpose: HUAKAI 反代核心代码 + spec 对照 5 家官方当前文档的 drift 检测。

Method: 5 个并行 document-specialist agent 各自抓取一家 vendor 当前文档（不依赖训练时记忆），对照 HUAKAI 现 R6/R7.1/F-RATE-001/F-AUTH-005/A04/codename-mapping 等已落地资产。

Fetch timestamp: 2026-05-06 UTC（5 家同日抓取）。

## CRITICAL DRIFT（必须修，HUAKAI 现代码会失效）

### D1. Anthropic rate-limit headers — F-RATE-001 §Phase C
**Severity: P0 — 现 parser 完全 miss**

HUAKAI 假设：
```
anthropic-ratelimit-unified-{5h,7d}-{reset,utilization,surpassed-threshold}
anthropic-ratelimit-unified-reset
```

Anthropic 当前文档（[platform.claude.com/docs/en/api/rate-limits](https://platform.claude.com/docs/en/api/rate-limits)）：
```
anthropic-ratelimit-{requests,tokens,input-tokens,output-tokens}-{limit,remaining,reset}
anthropic-priority-{input-tokens,output-tokens}-{limit,remaining,reset}    # Priority Tier only
```
Reset format: **RFC 3339**.

**Unified / 5h / 7d / surpassed-threshold / utilization 这套 header family 不存在于当前 Anthropic 文档**。

修复：F-RATE-001 §Phase C 全部 5 层 fallback 提取代码 dead；用 per-type (requests/tokens/input/output) 4 维 header 重写。

---

### D2. AWS Bedrock R-018 status code 完全错位 — error_normalize.go
**Severity: P0 — 当前 R6 规则永不匹配真实 throttling**

HUAKAI R-018:
```
provider="bedrock", HTTPStatus="503", BodyKeyword="throttling" → rate_limited
```

Bedrock 当前文档：
```
HTTP 429 = ThrottlingException (账号 quota 超限 = rate limiting)
HTTP 503 = ServiceUnavailableException (服务容量 ≠ rate limiting)
```
官方明示：**"503 errors ... is not related to your account-level quotas or rate limits (which return 429 ThrottlingException)"**。

修复：R-018 改为 `HTTPStatus="429" BodyKeyword="ThrottlingException"`；新增 R-020 处理 503=service_unavailable 单独 class（不同 retry 策略）。

---

### D3. OpenAI 402 → 不再用 — error_normalize.go R-007/R-008/R-005
**Severity: P0 — 规则永不命中**

HUAKAI 规则 R-007（402 + "credit"）/ R-008（400 + "credit balance"）/ R-005（402 + "deactivated_workspace"）依赖 OpenAI 用 402 表示账单失败。

OpenAI 当前文档（[developers.openai.com/api/docs/guides/error-codes](https://developers.openai.com/api/docs/guides/error-codes)）：
```
402 不在文档错误码列表
配额耗尽 → 429 + rate_limit_error
billing 失败 → ?（未明确文档化为单独 type）
```

修复：R-007/R-008 改 trigger（429+特定 message）或降为 ambiguous；R-005 keywords 验证（`deactivated_workspace` 当前未出现在文档作为 error.code）。

---

### D4. OpenAI x-codex-* headers — F-RATE-001 §Phase C Layer 1
**Severity: P0 — dead code**

HUAKAI parser：解析 `x-codex-*` 头计算 5h/7d window。

OpenAI 当前文档：`x-codex-*` 头**完全不存在**。openai/codex#2131 issue 明确 Codex CLI 用标准 `x-ratelimit-*`。

修复：F-RATE-001 §Phase C Layer 1 删除；用 `x-ratelimit-{limit,remaining,reset}-{requests,tokens}` 标准头 + `_usage_based` 变体。

---

## HIGH DRIFT（应当修，影响精度）

### D5. Anthropic prompt_caching ttl 字段 — R7.1 cache_control.go
**Severity: P1 — 缺失 ttl 维度**

Anthropic 当前：
```json
{"type": "ephemeral"}              // 5min default
{"type": "ephemeral", "ttl": "1h"} // 1 hour TTL
```
另有约束：长 TTL 项必须排在短 TTL 项之前。

HUAKAI R7.1 `CacheControlLocation.Type` 只有一个 string，未跟踪 ttl。

修复：增 `TTL string` 字段于 CacheControlLocation；inspector 解析 ttl；suggester 默认 5m + 可配置；validator 加序约束。

---

### D6. Anthropic min cacheable threshold 模型相关 — R7.1 + spec
**Severity: P1 — 假设单值已过时**

Anthropic 当前：
- Opus 4.5/4.6/4.7, Haiku 4.5: **4096 tokens**
- Sonnet 4.6: **2048 tokens**
- Sonnet 4.5 / 早 4 / 3.7: **1024 tokens**
- Haiku 3.5: **2048 tokens**

修复：建 `model_min_cacheable_tokens` 表 + 注入到 R7.1 inspector inputs；suggester 跳过低于阈值的 block。

---

### D7. OpenAI reasoning 形态 — F-PROTO/protocol-translation.md
**Severity: P1 — top-level legacy**

OpenAI 当前：
```json
{"reasoning": {"effort": "medium", "summary": "auto"}}
```
HUAKAI 文档若假设 top-level `reasoning_effort` string = legacy/deprecated。

修复：protocol-translation spec 改为 nested `reasoning.effort`；保留 legacy 兼容输入。

---

### D8. Anthropic 402/504 新增 error type — R6 error_normalize.go
**Severity: P1 — 缺规则**

Anthropic 当前：
- 402 → `billing_error`（新 typed error class）
- 504 → `timeout_error`（新）
- 413 → `request_too_large`（新）

HUAKAI R6 当前未覆盖这 3 个。

修复：R-021 (402+anthropic, billing_error → permanent or counted) / R-022 (504, timeout_error → cooldown) / R-023 (413, request_too_large → fail to client no_charge)。

---

### D9. OpenRouter sticky routing 假设错 — synthesis A04
**Severity: P1 — 设计前提错**

HUAKAI A04 sticky migration manifest 假设 OpenRouter 有 sticky session 机制（header / cookie）。

OpenRouter 当前：**无独立 sticky 机制**。粘性靠 `provider.order` + `allow_fallbacks: false` 实现。

修复：A04 spec extend 注明：跨 OpenRouter 的 sticky 必须由 HUAKAI 自己用 provider.order 配置实现，不能依赖上游 session 机制。

---

### D10. OpenAI OAuth 假设错 — F-AUTH-005 Vendor-X1
**Severity: P1 — 上游能力假设错**

HUAKAI F-AUTH-005 Phase C 为 OpenAI OAuth 设计 refresh skew / refresh-storm 控制。

OpenAI 当前：**OpenAI 自己不发 service account JWT**；OAuth 2.1 / Apps SDK 走第三方 IdP；machine-to-machine 客户端凭据流明确不支持。API 用 API key（不 expire 不 refresh）。

修复：F-AUTH-005 Provider Policy Matrix 删除 OpenAI 列的 OAuth refresh 字段；标注 `auth_kind=api_key`；A07 storm 控制器对 OpenAI account 不触发（无 refresh）。

---

## MEDIUM DRIFT（应当审视）

### D11. Vertex Gemini caching min token DRIFT
- Was 32k assumed; now 2048（Gemini 2.x/2.5）/ 4096（Gemini 3.x）。

### D12. Vertex 429 reset timestamp UNKNOWN
- F-RATE-001 假设可解出 reset；当前 Vertex 429 body 无 reset timestamp 文档化。HUAKAI 可能要 fallback 到固定 cooldown。

### D13. Bedrock prompt caching shape — HUAKAI 未实现
- Bedrock prompt caching 已 GA（Claude Opus 4 / 3.7 Sonnet）+ 5min/1h TTL。HUAKAI R7.1 当前只针对 Anthropic 直连，未含 Bedrock 入口的 cachePoint shape。

### D14. Bedrock Global CRI tier — HUAKAI synthesis A19 假设单 dimension
- 现有 Geographic + Global 两维。Global CRI ≈ 10% cost saving。A19 capacity graph 应分维。

### D15. OpenRouter provider 字段集大幅扩展 — synthesis Vendor-Meta
- 新字段：`zdr` / `only` / `sort` (object form) / `preferred_min_throughput` / `preferred_max_latency` / `max_price` / `enforce_distillable_text`。
- 旧 URL 全 404；docs 已迁到 `/docs/guides/` + `/docs/api/reference/`。

---

## 总结

15 项 drift，其中 4 P0（D1/D2/D3/D4 — HUAKAI 已落代码 dead 或误判），6 P1（D5-D10），5 P2（D11-D15）。

**P0 修复必须先做**——否则 R6 + F-RATE-001 在生产环境上**完全不工作**对 OpenAI/Anthropic/Bedrock。

**根因**：HUAKAI spec 写于 2026-04-28，参考 sub2api 源（其 commit 时间更早）。3-4 周内 vendor 文档发生变动。需要持续 drift detection（已在 backlog `F-TOS-DRIFT-001`）。

## 修复优先级建议

```
Phase X (~30h, P0 4 项):
  - D1 F-RATE-001 §Phase C 重写为新 header 集
  - D2 R6 R-018 改 429 + ThrottlingException
  - D3 R6 R-007/R-008/R-005 改 trigger 或降级
  - D4 F-RATE-001 §Phase C Layer 1 删 x-codex-*
  
Phase Y (~25h, P1 6 项):
  - D5 R7.1 加 ttl 字段
  - D6 R7.1 + spec 加 per-model threshold
  - D7 protocol-translation reasoning nested
  - D8 R6 加 402/504/413 规则
  - D9 A04 sticky 实现注记
  - D10 F-AUTH-005 OpenAI 删 OAuth refresh

Phase Z (~10h, P2):
  - D11-D15 文档对齐
```

总修复 effort ≈ 65h；P0 ≈ 30h 必做。
