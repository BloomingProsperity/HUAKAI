# 2026-05-22 deep audit: billing/proto/eventbus/auditledger

审计范围：`backend/internal/billing/`、`backend/internal/proto/`、`backend/internal/eventbus/`、`backend/internal/auditledger/`，并只在需要确认生产调用链时读取相邻 HUAKAI 内部代码。

排除项：本轮未复报 Owner 已确认的 8 个问题（L2 cache key missing client protocol、buffered-path upstream error-body leak、storm-controller production panic、EvidenceMock mislabel、SSE error headers lost、response err.Error leak、dead Rust metric、RoutePlan cache disabled）。

## Findings

### 1. `UserID` 没有进入 billing 幂等指纹，可能把同一 API key 下不同用户折叠成同一个 claim

- `file:line`: `backend/internal/billing/claim_gate.go:86`、`backend/internal/billing/claim_gate.go:98`、`backend/internal/billing/claim_gate.go:145`、`backend/internal/billing/claim_gate.go:150`、`backend/internal/billing/claim_gate.go:187`
- severity: HIGH
- 失败场景：`Reserve` 先按 `(tenant_id, api_key_id, idempotency_key)` 查旧 claim，命中 committed 时直接返回旧 `ClaimID`；新建 claim 时又会保存 `UserID`。但 `ComputeIdempotencyFingerprint` 只写入 `TenantID/APIKeyID/LogicalRequestID/EndpointFamily/NormalizedPayloadHash/RequestedModel/BillingPolicyVersion/RequestClass`，不包含 `UserID`。如果一个 API key 承载多个最终用户，用户 B 复用用户 A 的 `logical_request_id` 和相同 payload，会命中用户 A 的 claim，后续 billing/replay/usage 归属继续使用旧 claim 的 `UserID`，造成跨用户响应重放或计费归属错误。
- 修复方向：把 `UserID` 纳入幂等唯一性和 fingerprint，或在 API key 明确为单主体时强制 `UserID` 为空并在 replay path 校验当前 user 与 claim user 一致。

### 2. 金钱事件允许 `NULL` 或伪造的 audit request id，billing 和 ledger 可以脱链

- `file:line`: `backend/internal/billing/settler.go:181`、`backend/internal/billing/settler.go:194`、`backend/internal/billing/settler.go:303`、`backend/internal/billing/settler.go:316`、`backend/internal/billing/settler.go:449`、`backend/internal/billing/settler.go:462`、`backend/internal/billing/settler.go:553`、`backend/internal/billing/settler.go:555`
- severity: HIGH
- 失败场景：正常 settle、abort、cache-hit 都只是 `TrimSpace(req.AuditRequestID)` 后写入 nullable 字段；refund 更严重，缺少 `AuditRequestID` 时会构造 `audit-refund-<claimID>`。因此成功扣费、退款或 abort 事件可以没有真实 ledger entry，也可以带一个看起来像审计引用但实际未写入 ledger 的字符串。账务表存在事件，但后续无法证明该事件对应哪条 signed append-only ledger 记录。
- 修复方向：所有 money-path billing event 必须要求真实 ledger append 成功后传入 ledger request id/signature fingerprint；禁止 synthetic audit id，缺失时进入可重放 DLQ/人工处理而不是提交账务事件。

### 3. slot release miss 会回滚已写入的 billing event 和 usage record，成功上游调用可能丢账

- `file:line`: `backend/internal/billing/settler.go:82`、`backend/internal/billing/settler.go:86`、`backend/internal/billing/settler.go:196`、`backend/internal/billing/settler.go:203`、`backend/internal/billing/settler.go:222`、`backend/internal/billing/settler.go:229`、`backend/internal/billing/settler.go:230`
- severity: HIGH
- 失败场景：Tx2 里先插入 `billing_events` 和 `usage_records`，随后才调用 `ReleaseSlotAndDecrementInFlight`。如果 slot 已被超时清理、重复释放或竞争路径释放，`released == 0` 返回 `ErrSlotReleaseMissed`；因为函数开头有 deferred rollback，前面已经写入的账务事件和 usage record 全部回滚。结果是上游已经成功消耗资源，但本地没有 committed claim/usage/billing 记录，形成 lost charge 和 reconciliation gap。abort path 也有同类风险。
- 修复方向：把 slot release miss 变成幂等异常记录或独立 reconciliation item，不应回滚已经确定的 usage/billing；至少先提交财务事实，再异步修复 slot 状态。

### 4. token 计数直接转 `int32`，超大或负值会静默溢出并污染 usage record

- `file:line`: `backend/internal/billing/settler.go:149`、`backend/internal/billing/settler.go:151`、`backend/internal/billing/settler.go:152`、`backend/internal/billing/settler.go:343`、`backend/internal/billing/settler.go:487`、`backend/internal/billing/settler.go:489`、`backend/internal/billing/settler.go:490`
- severity: HIGH
- 失败场景：`Draft.TokensInput`、cache token、abort observed token 都直接 `int32(...)`。如果上游 usage parser、测试注入、异常模型或恶意 provider 返回超过 `math.MaxInt32` 的 token 数，Go 转换会静默 wrap；如果返回负数，也会被写入。报表、quota、reconciliation 和后续成本审计会基于损坏 token 字段运行，而错误不会被显式记录。
- 修复方向：对所有 token count 做统一 `int64 -> int32` 边界校验：负数 reject，超上限 reject 或进入 reconciliation DLQ；不要只保护 output token。

### 5. refund 幂等 replay 返回请求金额，而不是数据库里实际写入/封顶后的退款金额

- `file:line`: `backend/internal/billing/settler.go:562`、`backend/internal/billing/settler.go:574`、`backend/internal/billing/settler.go:576`、`backend/internal/billing/settler.go:607`、`backend/internal/billing/settler.go:635`、`backend/internal/billing/settler.go:646`、`backend/internal/billing/settler.go:656`、`backend/internal/billing/settler.go:666`
- severity: MED
- 失败场景：首次 refund 会按 claim 原始成本和历史 refund 把 `refundMicros` cap 到剩余额度，再写入 `billing_events.actual_cost_signed`。但同一个 `audit_request_id` 的幂等命中只查到旧 event id，然后返回 `RefundMicroUSD: req.AmountMicroUSD`。如果首次请求 1000 micros 实际只剩 200 micros 可退，第二次重放同一个 audit id 仍会向调用方报告退了 1000 micros，导致操作台、自动化对账或上游补偿系统看到错误退款金额。
- 修复方向：幂等命中时读取并返回既有 billing event 的实际 signed amount，或在同一 idempotency key 下请求金额不一致时返回冲突。

### 6. canonical streaming tool argument delta 类型不一致，跨协议流式 tool args 会被丢弃

- `file:line`: `backend/internal/proto/openai/sse.go:363`、`backend/internal/proto/openai/sse.go:367`、`backend/internal/proto/anthropic/sse.go:485`、`backend/internal/proto/anthropic/sse.go:486`、`backend/internal/proto/openai_chat_stream.go:153`、`backend/internal/proto/anthropic_messages_stream.go:271`、`backend/internal/proto/openai_responses_stream.go:144`
- severity: HIGH
- 失败场景：OpenAI/Anthropic upstream adapter 把 tool argument chunk 规范化成 `CanonicalContentDelta.Type == "tool_input_delta"`；但 client-side renderers 只识别 `"input_json_delta"`。流式 tool call 会先发 `content_block_start`，随后所有参数 delta 进入 unknown/pending path 或被 loss 掉，客户端收到空参数 tool call。生产上表现为模型要求调用工具，但下游工具参数缺失，可能触发错误工具调用或人工 retry。
- 修复方向：定义唯一 canonical delta enum，统一 upstream adapter 和所有 client renderer；加 OpenAI Chat、Anthropic Messages、OpenAI Responses 三条交叉协议流式 tool-use 测试。

### 7. OpenAI Chat client renderer 用 block index 当 tool slot，且空参数默认写 `{}`，会错绑或污染工具参数

- `file:line`: `backend/internal/proto/openai_chat_stream.go:108`、`backend/internal/proto/openai_chat_stream.go:109`、`backend/internal/proto/openai_chat_stream.go:153`、`backend/internal/proto/openai_chat_stream.go:155`、`backend/internal/proto/openai_chat_stream.go:156`、`backend/internal/proto/openai_chat_stream.go:158`、`backend/internal/proto/openai/sse.go:381`、`backend/internal/proto/openai/sse.go:382`
- severity: HIGH
- 失败场景：`content_block_start` 建了 `CallID -> slot` map，但 `input_json_delta` 渲染时没有反查该 map，而是直接 `slot := evt.Index`。OpenAI upstream adapter 的 tool `BlockIndex` 来自 canonical content block 序号；如果前面已有 text block，tool block index 可能是 1，而 OpenAI `tool_calls` slot 是 0。参数 delta 会发到不存在的 slot 1，slot 0 保持空参数。另一个问题是空 `PartialJSON` 被改成 `{}`，会把“还没有参数字节”伪造成完整空对象，污染流式累积语义。
- 修复方向：在 canonical event 中带 call id 或维护 block index -> tool slot 映射；空 partial 应跳过或发空字符串，不能注入 `{}`。

### 8. tool call id 翻译只接受十六进制后缀，合法 opaque id 会丢失，流式工具链无法关联

- `file:line`: `backend/internal/proto/tool_call_id.go:11`、`backend/internal/proto/tool_call_id.go:16`、`backend/internal/proto/tool_call_id.go:43`、`backend/internal/proto/tool_call_id.go:61`、`backend/internal/proto/tool_call_id.go:67`、`backend/internal/proto/tool_call_id.go:76`、`backend/internal/proto/openai/sse.go:467`、`backend/internal/proto/openai/sse.go:474`、`backend/internal/proto/anthropic/sse.go:287`、`backend/internal/proto/anthropic/sse.go:290`、`backend/internal/proto/gemini/sse.go:379`、`backend/internal/proto/gemini/sse.go:384`
- severity: HIGH
- 失败场景：`ToCanonicalCallID` 去掉供应商前缀后要求剩余部分必须是 hex。上游若返回合法但非 hex 的 opaque tool id，OpenAI/Anthropic/Gemini streaming adapter 会记录 lossy 并返回空 call id；Anthropic streaming block 甚至保留 `Name/Input` 但没有 `CallID`。后续 client renderer 要么报 missing call_id/name，要么无法把 tool_result 绑定回 tool_use，流式工具调用链断裂。
- 修复方向：把供应商 tool id 当 opaque value 处理，用可逆 escape/base64url 或带 hash 的稳定映射；失败时必须生成唯一 fallback id 并保存映射，不能返回空 id。

### 9. OpenAI Responses continuation 带 `previous_response_id` 时，合法的 tool output-only 请求会被拒绝

- `file:line`: `backend/internal/proto/openai_responses_request.go:43`、`backend/internal/proto/openai_responses_request.go:44`、`backend/internal/proto/openai_responses_request.go:121`、`backend/internal/proto/openai_responses_request.go:176`、`backend/internal/proto/openai_responses_request.go:200`、`backend/internal/proto/openai_responses_request.go:202`
- severity: HIGH
- 失败场景：adapter 把 `previous_response_id` 保存为 `SessionHash`，但 `function_call_output` 仍然要求同一个 request payload 里先出现对应 `function_call`，因为 `toolCallIDToNodeID` 只是本次请求的本地 map。OpenAI Responses 的 continuation 工作流常见形态是“上一轮 response 产生 function_call，本轮带 previous_response_id 只提交 function_call_output”。这条合法请求会被 `references unknown call_id` 拒绝，导致工具回填对话无法继续。
- 修复方向：当 `previous_response_id` 存在时允许外部 prior call id，生成 session-scoped placeholder edge 或从持久 session graph 查回原 tool call；只对同请求内确实无上下文的 orphan tool output 报错。

### 10. OpenAI Responses streaming 的 tool-use 路径仍是 pending stub，生产流式工具调用被静默降级为 loss

- `file:line`: `backend/internal/proto/openai_responses_stream.go:118`、`backend/internal/proto/openai_responses_stream.go:119`、`backend/internal/proto/openai_responses_stream.go:120`、`backend/internal/proto/openai_responses_stream.go:144`、`backend/internal/proto/openai_responses_stream.go:145`、`backend/internal/proto/openai_responses_stream.go:146`
- severity: HIGH
- 失败场景：OpenAI Responses client renderer 遇到 canonical `tool_use` start 只返回 `responses_streaming_tool_use_d11_pending` loss，不发任何 `response.output_item.added`；遇到 `input_json_delta` 也只返回 pending loss。对生产客户端来说，流式 response 里工具调用 item 和参数都消失，只剩文本路径可用；如果上游模型选择工具，客户端无法看到工具调用，流程会挂起或误判完成。
- 修复方向：实现 Responses streaming function_call item/add/delta/done 事件；在实现前对不支持的 tool streaming 入口返回明确的 pre-stream 4xx/feature-gated error，而不是流中静默丢弃。

### 11. eventbus 把 raw handler error 写入 state 和 DLQ，同时忽略 DLQ enqueue 失败

- `file:line`: `backend/internal/eventbus/bus.go:201`、`backend/internal/eventbus/bus.go:205`、`backend/internal/eventbus/bus.go:256`、`backend/internal/eventbus/bus.go:262`、`backend/internal/eventbus/types.go:246`、`backend/internal/eventbus/types.go:256`、`backend/internal/eventbus/types.go:260`
- severity: MED
- 失败场景：handler error 原文同时进入内存 state、DLQ `FailureReason` 和 DLQ payload 的 `failure_reason`。billing/audit handler error 往往会 wrap SQL、表名、列名、provider/account 上下文；这些原文会沉淀到运维表或导出日志，扩大内部结构和可能敏感上下文的泄露面。更坏的是 `writeDLQ` 对 `b.dlq.Enqueue` 使用 `_, _ =`，DLQ 写入失败时没有任何返回或状态标记；关键 handler 失败后，operator 可能既拿不到可重放 DLQ，也看不到 DLQ 丢失事实。
- 修复方向：DLQ/state 写 typed sanitized failure code，敏感 detail 只进受控 trace；DLQ enqueue 失败必须记录单独的 durable alert/metric，并让关键路径携带“DLQ persist failed”状态。

### 12. RequestCompletionEvent 的有效性校验不要求 claim/audit/signature，critical audit handler 默认也不强制 ledger ref

- `file:line`: `backend/internal/eventbus/types.go:58`、`backend/internal/eventbus/types.go:62`、`backend/internal/eventbus/types.go:71`、`backend/internal/eventbus/types.go:72`、`backend/internal/eventbus/types.go:212`、`backend/internal/eventbus/types.go:226`、`backend/cmd/gateway/middleware.go:192`、`backend/cmd/gateway/middleware.go:194`、`backend/internal/observability/audit_logger_handler.go:51`、`backend/internal/observability/audit_logger_handler.go:69`
- severity: HIGH
- 失败场景：eventbus 的 completion event 结构有 `ClaimID/AuditLedgerID/AuditSignatureFingerprint`，但 `normalized()` 只要求 `ID` 和 `TenantID`。gateway 注册了 billing persister 和 audit logger 两个 critical handler，但 audit logger 用默认构造，没有启用 `WithRequiredAuditRef`；handler 只在 `requireRef` 为 true 时拒绝空 `AuditLedgerID`。因此 request completion 事件可以被视为 valid 并触发 billing settle，但 audit ledger ref/signature 为空仍不阻断。这是 trust-chain bypass，不是单纯观测缺字段。
- 修复方向：对 money-path `RequestCompletionEvent` 强制 `ClaimID > 0`、`AuditLedgerID != ""`、`AuditSignatureFingerprint != ""`；gateway 默认启用 required audit ref，灰度时只能 feature-flag 降级并写 mandatory reconciliation item。

### 13. audit ledger sanitizer error 被忽略，redaction 失败时仍可能签名和存储未净化 payload

- `file:line`: `backend/internal/auditledger/privacy.go:17`、`backend/internal/auditledger/privacy.go:23`、`backend/internal/auditledger/privacy.go:36`、`backend/internal/auditledger/postgres.go:108`、`backend/internal/auditledger/postgres.go:113`、`backend/internal/auditledger/ledger.go:85`、`backend/internal/auditledger/ledger.go:88`
- severity: MED
- 失败场景：`sanitizeLedgerEntry` 在 redactor 返回错误或无法产生 raw payload 时会返回原始 `entry` 和 error；Postgres append 与 Memory append 都用 `entry, _ = sanitizeLedgerEntry(...)` 丢弃错误。只要 redactor 因 payload shape、context cancel、内部错误失败，ledger 仍会继续签名和写入原始 `TenantScopeRef/HopChain/ModelChain`。这会把本该隐私净化的链路数据固化进 append-only ledger，后续无法删除。
- 修复方向：sanitize error 必须使 append 失败，或写入最小化 fallback payload 并把原始 payload 丢弃；禁止在 append-only ledger 中忽略 redaction failure。

### 14. audit verify 的 request-id 查询缺少 tenant scope，调用方不提供 `tenant_scope_ref` 时可返回其他租户 ledger entry

- `file:line`: `backend/internal/auditledger/ledger.go:19`、`backend/internal/auditledger/postgres.go:214`、`backend/internal/auditledger/postgres.go:221`、`backend/internal/gatewayhttp/audit_verify_handler.go:55`、`backend/internal/gatewayhttp/audit_verify_handler.go:105`、`backend/internal/gatewayhttp/audit_verify_handler.go:115`、`backend/internal/gatewayhttp/audit_verify_handler.go:119`
- severity: HIGH
- 失败场景：ledger interface 和 Postgres 实现只按 `request_id = $1` 查询，没有 tenant 参数。HTTP verify handler 允许 `tenant_scope_ref` 为空；为空时不会做租户匹配，直接返回 `auditVerifyResponse`。如果 request id 可预测、客户端自带、或不同租户发生重复，攻击者只要知道/猜到 request_id 就能拿到其他租户的 ledger entry、tenant scope ref、hop/model chain 和签名材料。即使 request id 设计上应全局唯一，当前接口没有强制这个安全边界。
- 修复方向：新增 tenant-scoped lookup，例如 `GetByRequestIDAndTenantScope`；公开 verify API 必须要求 `tenant_scope_ref` 或签名 receipt token，Postgres 查询层也应把 tenant/scope 纳入 WHERE。

### 15. Postgres ledger 读取时吞掉 JSON 和 Merkle root 结构错误，会把损坏行当成正常 entry 返回

- `file:line`: `backend/internal/auditledger/postgres.go:362`、`backend/internal/auditledger/postgres.go:363`、`backend/internal/auditledger/postgres.go:365`、`backend/internal/auditledger/postgres.go:366`、`backend/internal/auditledger/postgres.go:368`、`backend/internal/auditledger/postgres.go:371`、`backend/internal/auditledger/postgres.go:374`
- severity: MED
- 失败场景：`scanLedgerEntry` 对 `hop_chain` 和 `model_chain` 的 JSON unmarshal error 全部忽略；`prev_merkle_root` 和 `merkle_root` 长度不是 32 时也只是保留 zero root。数据库行被部分写坏、迁移类型错误、手工修复误写或存储层损坏时，读取方会拿到一个看似成功的 `LedgerEntry`，但链路内容为空/错误、root 可能为零。后续 verify 失败会变成“签名不匹配”类症状，而不是在数据读取边界报告 ledger corruption。
- 修复方向：扫描时对 JSON decode failure 和 root length mismatch 返回明确 corruption error；verify API 将其映射为 500/ledger_corrupt 并触发告警。

## 总结

本轮最重的风险集中在三类：billing 与 ledger 脱链、跨协议 streaming tool-use 丢参、audit/verify 的租户边界不足。上述问题都来自 HUAKAI 内部代码实读，没有使用参考项目源码；没有提出删除功能的建议，修复方向均是保留功能并补强账务、协议和信任链完整性。
