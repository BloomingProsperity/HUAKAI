# 2026-05-22 深度审计 — Zone A: gatewayhttp(HTTP 层)

> Claude 亲审区。配对 codex 三区(billing-proto / routing-auth / rust)。
> 范围:`backend/internal/gatewayhttp/` chat completions 请求生命周期热路径。

## HIGH

### GW-01 L2 缓存 key 缺 endpoint family / client protocol —— 协议污染
- 证据:`cache/key.go:23-48` `KeyInput` = {TenantID, Vendor, Model, Body},preimage 不含 endpoint;`chat_completions_stream.go:60-68` `l2CacheKeyForModel` 也不传 endpoint。
- `/v1/chat/completions`、`/v1/messages`、`/v1/responses` 三端点复用同一条 pipeline,仅 `EndpointFamily` 字符串不同(`chat_completions_handler.go:341-356`),而 `EndpointFamily` 没进 key。
- 失败场景:同 tenant/vendor/model/body 跨端点 → 同 key → `serveL2CacheHit` 有 `w.Write(in.Entry.Body)` 原样返回分支(`chat_completions_handler_headers.go:203,258`)→ OpenAI 格式响应可能回给 Anthropic 客户端。
- 修复方向:`KeyInput` 加 `EndpointFamily`(+ client protocol),进 preimage,`keyVersion` 升版使旧条目失效。

### GW-02 上游错误 body 透传客户端 —— 信息泄露
- 证据:`gateway/upstream_http_error.go:24-33` `Error()` 拼接 256 字节上游 body;`chat_completions_dispatch.go:406`、`chat_completions_handler.go:272` 把 `err.Error()` 喂进 `classifiedFailureFromDecision` 的 `ClientMessage`;`chat_completions_attempt.go:324-333` `writeAttemptFailure` → `writeJSONError` → 客户端。
- 失败场景:供应商错误文案、账号线索、上游内部字段透给前端用户,重试耗尽时尤甚。
- 修复方向:公开消息(固定文案)与内部明细(仅日志)分离。

### GW-03 真实流量硬编码 EvidenceMock —— 腐蚀信任链
- 证据:`chat_completions_billing.go:216` `requestMetaSeed` 无条件 `EvidenceLabel: proto.EvidenceMock`;`:228-233` `enrichCanonicalRequestMeta` 空值回落 mock。seed 用于 buffered + streaming 两路。
- 失败场景:真实上游接通后,真实请求仍标 `mock`,信任链/反掺水审计语义失真。
- 修复方向:evidence label 由真实 dispatch 结果推导(真上游 vs mock),禁止硬编码。

### GW-10 审计写入与 pool 增改非原子 —— 全仓模式 bug
- 证据:`chat_completions...` 误植,实为 `admin_pools_handler.go:122-140`(create)、`:217-235`(update):先 `InsertPool`/`UpdatePool`,后 `writeAdminPoolAudit`,两步非同一事务。sibling `admin_pool_accounts_handler.go:188/243` 同模式。
- 失败场景:audit 写失败(DB 抖动等)时 pool 行已持久化,handler 却返回 503;管理变更生效但审计链缺条目,客户端以为失败可能重试 → 重复 pool。
- 修复方向:把 mutation + audit 包进同一 DB 事务(sqlc `WithTx`);**全仓统一修** —— admin_pools + admin_pool_accounts(及其它 audit-after-mutation handler)。属补救波,不在 P2 单独修。
- 注:codex P2 review 标 [P1];非 P2 引入的回归,P2 与既有 sibling 同模式。

## MED

### GW-04 err.Error() 直写客户端 JSON 响应(范围广)
- 证据:`chat_completions_dispatch.go:54,71,154,178,336,343,357`;`chat_completions_billing.go:47,57,66,86`;`chat_completions_handler.go:297,318,329`;`chat_completions_stream.go:45,307,317,338`。
- 失败场景:registry / router / pricing / reserve / DB 错误串(可能含 Postgres 字段名、内部状态)透给用户。
- 修复方向:三层错误模型 `public_code / public_message / internal_error`。

### GW-05 err.Error() 直写 HTTP header
- 证据:`X-Huakai-Abort-Failed`(dispatch.go:249,334,341;billing.go:45;handler.go:292,316,324;stream.go:305,315,322,336)、`X-Huakai-Forward-Error`(stream.go:211)、`X-Huakai-Settle-Error`(stream.go:234)全部塞 `*.Error()`。
- 失败场景:内部错误经响应头泄露。
- 修复方向:同 GW-04;header 只放安全 enum/code。

### GW-06 SSE 错误 header 在流开始后才 Set —— 客户端收不到
- 证据:`chat_completions_stream.go:211/234/246` 三个错误 header 在 `streamForwarder.Forward` 之后 Set;`declareStreamBillingTrailers`(:522-528)只声明了 StreamState + DeliveredTokens。
- 失败场景:排障信息丢失,客户端看到的状态与实际不一致。
- 修复方向:要么开头预声明为 trailer,要么改为仅服务端日志。

### GW-07 信任链账本静默跳过
- 证据:`chat_completions_billing.go:240-247` `submitAuditLedgerEntry` 在 `AuditLedger`/`Signer` 为 nil 时返回 `nil,nil`,仅追加一条内部 ProtocolLoss warning。
- 失败场景:signer 未配置时请求照样成功、照样计费,但无签名账本条目 —— 对"商家不能做假"是硬伤。
- 修复方向:决定 fail-closed 还是显式 degraded-mode;不允许"无账本却计费"静默发生。

### GW-08 用量可信度硬编码 1.0
- 证据:`chat_completions_billing.go:321` `nonStreamingUsageDraft` `confidence := 1.0` + `UsageSource: UsageSourceReported` 无条件。
- 失败场景:用量来自估算/mock 时,可信度仍是假的 1.0。与 GW-03 同源。
- 修复方向:可信度由真实用量来源推导。

## 观察项

- GW-09 [LOW] `readRawBufferedUpstreamBody`(handler.go:239-249)超限对非 2xx 返回截断 body 供分类 —— 设计如此,但需确保截断 body 不经 GW-02 路径外泄。

---
Source files read: backend/internal/cache/key.go; backend/internal/gateway/upstream_http_error.go; backend/internal/gatewayhttp/chat_completions_{handler,dispatch,billing,stream,attempt,handler_headers}.go。
Lane: Claude specifier(HUAKAI 内部代码,无 clean-room 约束)。
UTC: 2026-05-22
