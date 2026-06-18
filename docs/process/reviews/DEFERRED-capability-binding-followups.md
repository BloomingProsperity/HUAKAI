# Deferred follow-ups — capability-binding 切片 + inert-gap 死字段 roadmap

来源: capability-binding upsert 切片 (2026-06-18) 真码 grounding 期间发现。均非本切片 block, 记 Feature Preservation roadmap。

## 死字段 (Feature Preservation roadmap — 需先建消费者再暴露写路径)

inert-gap 猎取曾把这两列报为「建了无写路径」, 但真码核实后是【死开关】(存了+校验了但**零消费者**),
接线写路径只会暴露静默无效开关 (伤 UX, 违 Owner「别 bolt-on 没验证/伤 UX 的东西」)。**不接线**, 待消费者落地。

### DK-1 models.default_request_timeout_ms (per-model 请求超时)
`models.default_request_timeout_ms` → 读入 `Resolved.RequestTimeoutMS` (postgres_registry.go:153), 但
`.RequestTimeoutMS` 字段【零读取点】—— forwarder/executor 从不读它应用为 per-model 超时; 真实流超时来自 env
`buildGatewayTimeoutConfig` (cmd/gateway/middleware.go)。对照 sibling `.ContextWindow` 有大量真消费者 (gates/dispatch/fallback)。
- **roadmap**: 先在 forwarder 真应用 `Resolved.RequestTimeoutMS` 作 per-model 上游请求超时 (覆盖 env 默认),
  再加 PATCH model-metadata 写端点暴露该列。两步缺一不可 —— 否则是死开关。

### DK-2 user_notification_settings.threshold_type (fixed/percentage)
前一切片 (notify extra_emails) 剔除: notifier (notifier.go:112-122) 只读 `BalanceThreshold` 金额, 从不读
`ThresholdType`; `percentage` 仅出现在 ValidateSettings 检查处, 无任何代码解释百分比。
- **roadmap**: 先在低余额 crossing 评估 (notifier.go:112) 建百分比解释消费者 (threshold = 总充值×pct/100,
  需总充值数据源), 再连同 read/write 暴露 threshold_type。

## S2/S3 跨切面 (非本切片回归)

### FU-1 capability 写后 SnapshotVersion 不 bump (跨切面, 同影响 updateModelCapabilities)
`ResolveModel` 每次现算 (fresh 查询), 能力绑定写入下次 resolve 即时生效。但 `SnapshotVersion`
(`registry:<tenant>:<version>`, 返回给 client 作 ETag) 在能力写入后**不 bump** —— 按 version 缓存 resolve 的
client 可能不刷新。这是既有行为, 已接线的 `updateModelCapabilities` (capabilities PUT) 同样不 bump, 故能力绑定
upsert 跟同模式保持一致 (非本切片引入)。
- **跟进**: 评估能力写 (两个写者: updateModelCapabilities + UpsertModelCapabilityBinding) 是否应 bump 租户
  SnapshotVersion 以触发 client 缓存失效; 若是, 统一加 (一处决策覆盖两写者), 而非只在新写者加致两者不一致。

### FU-3 不存在的 tenant_id (scope=tenant) 错误分类为 503 而非 4xx (审查 S3)
scope=tenant 时 store 只校验 `params.TenantID<=0`, 不校验该 tenant 行存在。给一个正但不存在的 tenant_id, INSERT 命中
FK 约束 (tenant_id REFERENCES tenants(id)), 错误非 pgx.ErrNoRows → 包成 ErrRegistryBackend → writeModelAliasStoreError
落 default 分支 → HTTP 503 model_admin_store_failed。**FK 已正确阻止幽灵租户绑定** (无坏写/无跨租户), 仅状态类错
(admin 笔误 tenant_id 被误标"后端不可用")。本切片未改 store (model_capabilities.go 未认领, 仅状态类 cosmetic)。
- **跟进**: 在 upsertModelCapabilityBinding 检测 PG FK violation (pgconn.PgError SQLSTATE 23503) → 返
  `ErrInvalidModelCapability`(或新 ErrUnknownTenant) → 现有 switch 产 400; 加判别测试 (FK-error stub → 400 非 503)。
  此改属 store 文件, 单独小切片做。

### FU-2 capability_value / capability_params 仅 GET 显示, resolve 不消费
resolve 的 `ListModelCapabilities` 只取 capability 名 (+ enabled 过滤) 进 `Resolved.Capabilities`;
`capability_value` 与 `capability_params` (jsonb, 如 reasoning_effort levels) 当前仅 ListModelCapabilityBindings
(GET admin 显示) 读取, dispatch/resolve 不应用。本切片仍持久化它们 (供 GET 回显 + 未来消费), 但提醒: 这两子字段
目前是「写了能 GET 回看但不驱动行为」。
- **跟进**: 若要让 capability_params (如 per-capability reasoning_effort 档位) 真驱动 dispatch, 需在 resolve/
  执行层建消费者读取 capability_params 并应用。届时这两子字段从「部分消费」升为「全消费」。
