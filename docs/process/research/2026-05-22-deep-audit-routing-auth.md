# 2026-05-22 routing/auth 深度代码审计

审计范围：`backend/internal/router/`、`backend/internal/pool/`、`backend/internal/gateway/`、`backend/internal/auth/`、`backend/internal/credentialstore/`、`backend/internal/channelhealth/`、`backend/internal/registry/`。

本轮刻意排除了 Owner 已确认的 8 项问题：L2 cache key 缺 client protocol、buffered path 上游 error body 外泄、storm-controller 生产 panic、真实流量 EvidenceMock 误标、SSE 开始后错误 headers 丢失、`err.Error()` 回包外泄、Rust dead metric、RoutePlan cache disabled。

## Findings

### 1. `backend/internal/auth/antigravity_token_provider.go:278` - HIGH - Antigravity OAuth refresh endpoint 可由凭据 JSON 控制，存在 SSRF 面

失败场景：`decodeAntigravityCredential` 接受 `oauth_endpoint` 字段（`backend/internal/auth/antigravity_token_provider.go:412`、`backend/internal/auth/antigravity_token_provider.go:430`），`refresh()` 直接把它传给 `http.NewRequestWithContext`（`backend/internal/auth/antigravity_token_provider.go:278`）并向该地址 POST `refresh_token` / `client_secret`。如果租户或运维误写、恶意写入 `http://169.254.169.254/...`、loopback 或内网服务地址，网关会以服务端网络位置发起请求，并携带 OAuth secret 表单。

修复方向：对 OAuth endpoint 做生产 allowlist，只允许预期 HTTPS 主机；解析并拒绝 loopback、link-local、private CIDR、非 HTTPS 和重定向到内网的目标。

### 2. `backend/internal/auth/sanitizer.go:16` - HIGH - OAuth 错误脱敏漏掉 `client_secret`

失败场景：`labeledSecret` 只覆盖 `bearer|access_token|refresh_token|id_token|token`（`backend/internal/auth/sanitizer.go:16`），不覆盖 `client_secret`。Antigravity 刷新失败时会把上游非 2xx body 拼入错误（`backend/internal/auth/antigravity_token_provider.go:293`），随后写入 audit/log 的 `ErrorMessageRedacted` 和 `zap.String("error", ...)`（`backend/internal/auth/antigravity_token_provider.go:362`、`backend/internal/auth/antigravity_token_provider.go:363`）。如果 OAuth 服务回显 `client_secret=...` 或 JSON `"client_secret":"..."`，当前 sanitizer 不会遮蔽。

修复方向：补齐 `client_secret`、`client_assertion`、`password`、`secret` 等 JSON/form/labeled 模式，并对上游错误 body 做分类截断而不是原样拼接。

### 3. `backend/internal/auth/antigravity_token_provider.go:203` - HIGH - Antigravity refresh audit 是 best-effort，成功旋转可无审计落点

失败场景：刷新成功并更新凭据后，代码用 `_ = p.writeAudit(...)` 忽略审计写入错误（`backend/internal/auth/antigravity_token_provider.go:203`）；cache hit、CAS 失败、token malformed、refresh failure 也同样忽略（例如 `backend/internal/auth/antigravity_token_provider.go:123`、`backend/internal/auth/antigravity_token_provider.go:353`、`backend/internal/auth/antigravity_token_provider.go:362`）。`writeAudit` 在 `p.audit == nil` 时直接返回 nil（`backend/internal/auth/antigravity_token_provider.go:368`），并且还有公开的 `NoopAuditWriter`（`backend/internal/auth/audit.go:28`）。生产中 audit writer 未注入或 audit DB 短暂失败时，refresh token 已旋转、cache 已填充，但没有可靠刷新审计。

修复方向：生产 refresh 路径应要求真实 audit writer；对 token rotation / failure 这类安全事件至少 fail closed 或进入 DLQ，不能静默 `_ =`。

### 4. `backend/internal/credentialstore/postgres_store.go:229` - HIGH - 凭据生命周期变更成功后审计写入被忽略

失败场景：创建、旋转、禁用、删除、解析、refresh success/failure 的审计都以 `_ = s.InsertAuditEvent(...)` 调用，例如 create（`backend/internal/credentialstore/postgres_store.go:229`）、rotate（`backend/internal/credentialstore/postgres_store.go:308`）、delete（`backend/internal/credentialstore/postgres_store.go:462`）、refresh success（`backend/internal/credentialstore/postgres_store.go:617`）、refresh failure（`backend/internal/credentialstore/postgres_store.go:655`）。`InsertAuditEvent` 自身在 store/db 缺失或关键字段缺失时直接返回 nil（`backend/internal/credentialstore/postgres_store.go:958`）。结果是密钥创建/轮换/删除已经提交，但 audit insert 因 DB、隐私 redactor 或配置问题失败时，调用方仍收到成功。

修复方向：敏感凭据状态变更应与 audit insert 同事务提交；无法同事务时至少返回错误或写入可重放 DLQ，并去掉静默 no-op。

### 5. `backend/internal/credentialstore/postgres_store.go:429` - MED - 任意 SetState 都被审计成 `credential_disabled`

失败场景：`SetState` 接受任意合法状态（`backend/internal/credentialstore/postgres_store.go:402`），但审计事件类型固定为 `credential_disabled`（`backend/internal/credentialstore/postgres_store.go:429`）。如果管理员把凭据从 `revoked` / `operator_attention` 改回 `active`，审计流仍写“disabled”，后续安全复盘或自动审计会把启用动作误判成禁用动作。

修复方向：按状态迁移生成明确事件类型，例如 `credential_state_changed` 加 old/new state，或为 enable/disable/revoke 分别建事件。

### 6. `backend/internal/credentialstore/types.go:248` - MED - Azure 凭据允许 `mock_token_endpoint` 作为生产 payload

失败场景：Azure handler 的 `anyOf` 包含 `mock_token_endpoint`（`backend/internal/credentialstore/types.go:248`），`ValidatePayload` 只要任一字段非空就通过（`backend/internal/credentialstore/types.go:173`）。但 runtime material 只从 `api_key`、`azure_api_key`、`access_token` 取值（`backend/internal/credentialstore/types.go:203` 到 `backend/internal/credentialstore/types.go:211`），缺失真实 secret 时才在运行期报错（`backend/internal/credentialstore/types.go:229`）。这会让只含 mock endpoint 的 Azure 凭据被创建为有效配置，直到真实流量调度后失败。

修复方向：从生产 handler 移除 `mock_token_endpoint`，或把它放到明确 test-only registry；验证阶段必须要求可用 runtime secret。

### 7. `backend/internal/pool/router/pasr.go:448` - HIGH - PASR actual 可在未持有真实 slot 时写 claim 并返回成功

失败场景：PASR actual 路径在 `SlotManager.Acquire` 返回 `ErrSlotManagerUnavailable` 时直接进入 `tokenOnlyResult`（`backend/internal/pool/router/pasr.go:448`、`backend/internal/pool/router/pasr.go:449`），`p.slots == nil` 时也同样返回 token-only（`backend/internal/pool/router/pasr.go:479`、`backend/internal/pool/router/pasr.go:481`）。`tokenOnlyResult` 在 `p.claims != nil && req.ClaimID != 0` 时仍写 acquisition（`backend/internal/pool/router/pasr.go:523`、`backend/internal/pool/router/pasr.go:524`）。因此 PASR canary / primary 如果被错误注入 nil/unavailable slot manager，会生成 claim acquisition token，但没有 `pool_slot_acquisitions` 行，也没有 `provider_accounts.in_flight_count` 增量，绕过并发 cap 和后续 release/settlement 锚点。

修复方向：actual/canary/strict 模式下构造期强校验 Slots + Claims；生产模式遇到 slot unavailable 应 fail closed，不应走 token-only 兼容路径。

### 8. `backend/internal/pool/dispatcher/slot_manager.go:52` - MED - Serializable slot acquire 没有 retry，热路径会把并发冲突当请求失败

失败场景：`DBSlotManager.Acquire` 明确 TODO 说明 PostgreSQL 在同一 account 并发 `IncrementInFlightCount` 下会返回 SQLSTATE 40001，当前会作为 fatal slot error 透出（`backend/internal/pool/dispatcher/slot_manager.go:52` 到 `backend/internal/pool/dispatcher/slot_manager.go:57`）。实际代码开启 Serializable 事务（`backend/internal/pool/dispatcher/slot_manager.go:73`），执行 increment 和 insert 后直接 commit（`backend/internal/pool/dispatcher/slot_manager.go:80`、`backend/internal/pool/dispatcher/slot_manager.go:97`、`backend/internal/pool/dispatcher/slot_manager.go:111`），没有 retry。高并发下即使账号仍有容量，也会出现随机 5xx / fallback。

修复方向：对 serialization/deadlock SQLSTATE 做有界 retry + jitter，并保留 `ErrNoSlotAvailable` 的业务语义。

### 9. `backend/internal/channelhealth/failover.go:43` - HIGH - channel health gate 对 nil/missing/unknown 状态 fail-open

失败场景：`PoolGate.Allow` 在 gate、store 或 account 为 nil 时直接允许（`backend/internal/channelhealth/failover.go:42`、`backend/internal/channelhealth/failover.go:43`、`backend/internal/channelhealth/failover.go:44`）；找不到健康记录也允许（`backend/internal/channelhealth/failover.go:46` 到 `backend/internal/channelhealth/failover.go:48`）。`IsEligible` 对未知 state 的 default 分支也返回 true（`backend/internal/channelhealth/failover.go:105`、`backend/internal/channelhealth/failover.go:106`）。生产 wiring、迁移或数据污染导致 health store 不可用 / 状态拼错时，本应冷却、暂停、禁用的通道会继续被选中。

修复方向：生产 health gate 对 nil store/account 和未知 state fail closed；缺失记录应通过 service 显式初始化或作为可观测告警处理。

### 10. `backend/internal/channelhealth/store_postgres.go:295` - MED - channel health audit 可无签名 trust ledger

失败场景：`PostgresStore.AppendAudit` 成功插入 `channel_health_audit_events` 后，如果 `s.signer == nil` 就直接返回 nil（`backend/internal/channelhealth/store_postgres.go:295`、`backend/internal/channelhealth/store_postgres.go:296`）。`NewPostgresStore` 默认不带 signer（`backend/internal/channelhealth/store_postgres.go:28`）。如果生产配置漏传 signer，通道禁用、ramp rollback、manual pause 等状态变化仍成功提交，但没有追加签名 trust ledger。

修复方向：生产 store 构造应要求 signer；无 signer 只能在显式 dev/test 模式使用，并在健康检查中标红。

### 11. `backend/internal/gateway/forwarder.go:166` - HIGH - graceful stream 有输出但无 usage 时会以 reported/zero token 完成

失败场景：stream 结束时如果看到 terminal frame，就设为 `StreamEndGraceful`（`backend/internal/gateway/forwarder.go:165`、`backend/internal/gateway/forwarder.go:166`），只有没有 terminal 时才设置 `PendingReconciliation`（`backend/internal/gateway/forwarder.go:168`、`backend/internal/gateway/forwarder.go:169`）。chunk 已交付会累计 `DeliveredChunkCount`（`backend/internal/gateway/forwarder.go:203`、`backend/internal/gateway/forwarder.go:204`），但如果 provider 没有 usage frame，`canonicalUsage` 返回 false（`backend/internal/gateway/forwarder.go:547` 到 `backend/internal/gateway/forwarder.go:550`），`finishDraft` 仍把 `TokensInput/TokensOutput` 设成 0（`backend/internal/gateway/forwarder.go:395`、`backend/internal/gateway/forwarder.go:396`），并把来源从 ambiguous 改成 accumulator 的 `reported`（`backend/internal/gateway/forwarder.go:398`、`backend/internal/gateway/forwarder.go:399`）。成功流式输出但 provider 省略 usage 的场景，会形成 zero-token reported record，后续按 token 计费会漏收。

修复方向：graceful stream 只要 delivered > 0 且 usage empty，就应标记 inferred/ambiguous + pending reconciliation，或进入估算/最小计费路径。

### 12. `backend/internal/gateway/event_scanner.go:79` - MED - SSE scanner 把所有底层读错误都归类成 event overflow

失败场景：`ScanSSEEvents` 自己检测单 event 超限时返回 `ErrScannerOverflow`（`backend/internal/gateway/event_scanner.go:71`、`backend/internal/gateway/event_scanner.go:72`），但 `scanner.Err()` 的任何错误也都被转换成同一个 `ErrScannerOverflow`（`backend/internal/gateway/event_scanner.go:79`、`backend/internal/gateway/event_scanner.go:80`）。上游 TCP reset、TLS read error、context cancellation 以外的 IO 错误会被 `classifyScanError` 归成 `ResponseEventTooLarge`（`backend/internal/gateway/forwarder.go:496` 到 `backend/internal/gateway/forwarder.go:499`），导致错误分类、重试决策和 channel health 信号错误。

修复方向：只把真实 `bufio.ErrTooLong` / 自己的 size guard 映射为 overflow；其余 scanner.Err 原样传播或映射到 upstream/network error。

### 13. `backend/internal/gateway/forwarder.go:119` - HIGH - streaming trust ledger 在首个 header/body 时签名，不代表流完成

失败场景：`Forward` 创建的 `streamingLedgerHeaderWriter` 在 `WriteHeader` 或首次 `Write` 时调用 `ensureLedger(time.Now())`（`backend/internal/gateway/forwarder.go:119`、`backend/internal/gateway/forwarder.go:124`、`backend/internal/gateway/forwarder.go:600`、`backend/internal/gateway/forwarder.go:605`）。ledger 的 hop chain 用这个时间作为 response completion，并计算 duration（`backend/internal/gateway/forwarder.go:643`、`backend/internal/gateway/forwarder_hop_chain.go:48`）。长流式请求首 token 500ms、实际完成 60s 时，签名 ledger 会宣称 response hop 约 500ms 完成，审计记录与真实交付窗口不一致。

修复方向：把“header commitment”和“stream final completion”拆成两条 ledger，或在终态/finish 时追加最终完成 ledger，不要用首字节时间代表完成时间。

### 14. `backend/internal/gateway/forwarder.go:617` - HIGH - streaming ledger 缺失或 append 失败只 warning，不阻断请求

失败场景：`emitStreamingLedger` 遇到 `AuditLedger == nil`、noop ledger、`Signer == nil` 时只调用 `warnLedgerLoss` 并返回（`backend/internal/gateway/forwarder.go:617` 到 `backend/internal/gateway/forwarder.go:628`）；ledger append 失败也只是 warning 后返回（`backend/internal/gateway/forwarder.go:652` 到 `backend/internal/gateway/forwarder.go:655`）。生产 DI 漏配或 ledger DB 短暂故障时，streaming 请求照常交付，但没有 signed trust-chain entry。

修复方向：生产模式启动期强制 ledger + signer 可用；运行期 append 失败应进入明确的 fail-closed / durable DLQ 策略，而不是只回调 warning。

### 15. `backend/internal/gateway/upstream_dispatcher_hcsf.go:162` - MED - HCSF projection/control injection 失败后会回退 raw inbound body

失败场景：`buildHCSFProviderRequest` 调用 `hcsfRequestBody` 失败时，如果 `rawFallback` 非空就把 body 直接替换为原始入站 body（`backend/internal/gateway/upstream_dispatcher_hcsf.go:157` 到 `backend/internal/gateway/upstream_dispatcher_hcsf.go:163`）。而 `hcsfRequestBody` 的失败可能来自 canonical provider request marshal，也可能来自请求控制注入失败（`backend/internal/gateway/upstream_dispatcher_hcsf.go:168` 到 `backend/internal/gateway/upstream_dispatcher_hcsf.go:175`）。这会让原本应 fail-loud 的控制注入失败变成未投影、未记录 loss 的 raw dispatch。

修复方向：control injection 失败必须 fail closed；raw fallback 只能在显式兼容标志下使用，并写入协议损耗 / audit。

### 16. `backend/internal/gateway/protocol_selector.go:115` - MED - 多个 session protocol 以 OpenAI adapter 占位注册为生产可用

失败场景：代码注释说明 `cursor / antigravity / kiro / windsurf` 的 SSE 形态“待 OCAW 采集后确认”，但随后直接注册到 `openai.Adapter`（`backend/internal/gateway/protocol_selector.go:115` 到 `backend/internal/gateway/protocol_selector.go:123`），stream scanner 也把这些 family 全部注册成通用 SSE（`backend/internal/gateway/stream_scanner.go:146` 到 `backend/internal/gateway/stream_scanner.go:151`）。如果其中任一 session 协议实际不是 OpenAI Chat SSE 形态，网关会误解析、丢 usage/tool/error 语义，且路由层看起来是已支持协议。

修复方向：这些 family 应默认 feature-flag/experimental 或 fail-loud；只有有协议级 conformance tests 后才注册生产 adapter。

### 17. `backend/internal/pool/api.go:16` - MED - `VendorFromProtocolFamily` 覆盖不完整，已注册协议会得到空 vendor

失败场景：`VendorFromProtocolFamily` 只映射 anthropic/openai/codex/gemini（`backend/internal/pool/api.go:16` 到 `backend/internal/pool/api.go:27`），但 gateway registry 已注册 deepseek、mistral、groqcloud、together、perplexity、fireworks、openrouter、grok 以及多个 session family（例如 `backend/internal/gateway/protocol_selector.go:105` 到 `backend/internal/gateway/protocol_selector.go:123`）。dispatcher 对空 vendor 只是静默记录（`backend/internal/pool/dispatcher/dispatcher.go:172` 到 `backend/internal/pool/dispatcher/dispatcher.go:175`），而 channel health key 要求 vendor 非空（`backend/internal/channelhealth/types.go:89`）。使用该 helper 的新 provider 流量会丢失 vendor 维度的 mode 指标、健康绑定和后续告警切片。

修复方向：补全所有生产 protocol family 到 vendor 的映射，并在生产 request 构造处拒绝空 vendor。

### 18. `backend/internal/gateway/forwarder.go:268` - MED - streaming `event:error` payload 被原样透传给 client

失败场景：`handleEventWithAdapter` 对 `evt.Type == "error"` 的 protocol-level 错误帧绕过 adapter，直接 `writeAndFlush(w, rawSSE(evt))`（`backend/internal/gateway/forwarder.go:264` 到 `backend/internal/gateway/forwarder.go:270`）。注释里点名 Bedrock exception payload 会走这条路径（`backend/internal/gateway/forwarder.go:265`、`backend/internal/gateway/forwarder.go:266`）。如果上游 exception payload 含 provider 内部诊断、账号/请求 hint 或未脱敏 message，网关会把原始 provider 错误体作为 SSE 发给用户；这不是 Owner 已报的 buffered-path body leak，而是 streaming protocol error frame 的旁路。

修复方向：将 protocol error frame 转换为 canonical sanitized gateway error，只把稳定 code/status 暴露给 client，原始 payload 进入内部日志/audit 并做脱敏。
